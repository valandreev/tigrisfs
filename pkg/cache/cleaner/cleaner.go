package cleaner

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/valandreev/tigrisfs/log"
	"github.com/valandreev/tigrisfs/pkg/cache/index"
)

// ErrFatalCondition indicates that the cleaner could not restore the cache to a safe state.
var ErrFatalCondition = errors.New("cache cleaner: fatal condition")

// ErrCapacityNotReduced indicates that capacity constraints remain unmet after a maintenance run.
var ErrCapacityNotReduced = errors.New("cache cleaner: capacity not reduced")

// TriggerReason represents the source motivating a cleaner run.
type TriggerReason string

const (
	// TriggerReasonMaintenance is the periodic maintenance pass.
	TriggerReasonMaintenance TriggerReason = "maintenance"
	// TriggerReasonENOSPC is an emergency triggered by an out-of-space condition.
	TriggerReasonENOSPC TriggerReason = "enospc"
)

// Trigger describes a request to execute the cleaner.
type Trigger struct {
	Reason TriggerReason
}

// Config controls cleaner behaviour.
type Config struct {
	CacheDir       string
	MaxCacheBytes  int64
	MinFreePercent int
	CleanInterval  time.Duration
}

// Report summarises a cleaner run.
type Report struct {
	Trigger     Trigger
	TotalBefore int64
	TotalAfter  int64
	BytesFreed  int64
	Evicted     []string
	Emergency   bool
}

// Logger captures structured output for the cleaner.
type Logger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}

// diskUsage reports disk capacity and free space for the cache directory.
type diskUsage interface {
	Stat(path string) (total, free uint64, err error)
}

// Option customises cleaner construction.
type Option func(*Cleaner)

// WithLogger overrides the default logger.
func WithLogger(logger Logger) Option {
	return func(c *Cleaner) {
		c.logger = logger
	}
}

// WithDiskUsage swaps the disk usage inspector (primarily for tests).
func WithDiskUsage(usage diskUsage) Option {
	return func(c *Cleaner) {
		c.disk = usage
	}
}

// Cleaner coordinates cache eviction to honour capacity and fail-safe thresholds.
type Cleaner struct {
	cfg    Config
	idx    index.CacheIndex
	disk   diskUsage
	logger Logger

	mu sync.Mutex
}

// New constructs a cleaner.
func New(cfg Config, idx index.CacheIndex, opts ...Option) (*Cleaner, error) {
	if idx == nil {
		return nil, errors.New("cache cleaner: cache index is required")
	}
	if cfg.CacheDir == "" {
		return nil, errors.New("cache cleaner: cache directory is required")
	}
	if cfg.MinFreePercent < 0 || cfg.MinFreePercent > 100 {
		return nil, fmt.Errorf("cache cleaner: min free percent must be within [0,100], got %d", cfg.MinFreePercent)
	}
	if cfg.CleanInterval <= 0 {
		cfg.CleanInterval = 30 * time.Minute
	}

	c := &Cleaner{
		cfg:    cfg,
		idx:    idx,
		disk:   &dirDiskUsage{capacity: capacityFromConfig(cfg)},
		logger: defaultLogger(),
	}

	for _, opt := range opts {
		opt(c)
	}

	if c.logger == nil {
		c.logger = defaultLogger()
	}
	if c.disk == nil {
		c.disk = &dirDiskUsage{capacity: capacityFromConfig(cfg)}
	}

	return c, nil
}

// RunOnce executes a single cleaner pass for the provided trigger.
func (c *Cleaner) RunOnce(ctx context.Context, trigger Trigger) (Report, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	report := Report{Trigger: trigger, Emergency: trigger.Reason == TriggerReasonENOSPC}

	metas, err := c.idx.ListLRU(ctx, 0)
	if err != nil {
		return report, err
	}

	usage := totalSize(metas)
	report.TotalBefore = usage

	limit := c.cfg.MaxCacheBytes
	if limit <= 0 {
		limit = math.MaxInt64
	}

	totalCap, freeCap, err := c.disk.Stat(c.cfg.CacheDir)
	if err != nil {
		return report, err
	}

	requiredFree := requiredFreeBytes(totalCap, c.cfg.MinFreePercent)
	targetFree := requiredFree
	emergency := trigger.Reason == TriggerReasonENOSPC && requiredFree > 0

	for _, meta := range metas {
		if err := ctx.Err(); err != nil {
			return report, err
		}

		if usage <= limit && (!emergency || freeCap >= targetFree) {
			break
		}

		if !isEvictable(meta) {
			continue
		}

		freed, evictErr := c.evict(ctx, meta)
		if evictErr != nil {
			c.logger.Errorf("cleaner: evict %s failed: %v", meta.Path, evictErr)
			continue
		}

		usage -= freed
		if usage < 0 {
			usage = 0
		}
		report.BytesFreed += freed
		report.Evicted = append(report.Evicted, meta.Path)

		if freed > 0 && freeCap < math.MaxUint64 {
			freeCap += uint64(freed)
		}
	}

	report.TotalAfter = usage

	if usage > limit {
		return report, ErrCapacityNotReduced
	}

	if emergency {
		totalCap, freeCap, err = c.disk.Stat(c.cfg.CacheDir)
		if err != nil {
			return report, err
		}
		if totalCap > 0 && freeCap < targetFree {
			return report, ErrFatalCondition
		}
	}

	return report, nil
}

// RunBackground executes RunOnce on a schedule until ctx is cancelled.
func (c *Cleaner) RunBackground(ctx context.Context, triggers <-chan Trigger) error {
	ticker := time.NewTicker(c.cfg.CleanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := c.RunOnce(ctx, Trigger{Reason: TriggerReasonMaintenance}); err != nil && !errors.Is(err, ErrCapacityNotReduced) {
				c.logger.Warnf("cleaner maintenance run failed: %v", err)
			}
		case trigger, ok := <-triggers:
			if !ok {
				triggers = nil
				continue
			}
			if _, err := c.RunOnce(ctx, trigger); err != nil && !errors.Is(err, ErrCapacityNotReduced) {
				c.logger.Warnf("cleaner trigger %s failed: %v", trigger.Reason, err)
			}
		}
	}
}

func (c *Cleaner) evict(ctx context.Context, meta index.FileMeta) (int64, error) {
	path := filepath.Join(c.cfg.CacheDir, filepath.FromSlash(meta.Path))

	size := meta.Size
	info, err := os.Stat(path)
	if err == nil {
		size = info.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("stat file: %w", err)
	}

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("remove file: %w", err)
	}

	if err := c.idx.Delete(ctx, meta.Path); err != nil && !errors.Is(err, index.ErrNotFound) {
		return 0, fmt.Errorf("delete index entry: %w", err)
	}

	c.cleanupEmptyDirs(path)

	return size, nil
}

func (c *Cleaner) cleanupEmptyDirs(path string) {
	dir := filepath.Dir(path)
	cacheRoot := filepath.Clean(c.cfg.CacheDir)
	for dir != cacheRoot && len(dir) > len(cacheRoot) {
		if err := os.Remove(dir); err != nil {
			break
		}
		dir = filepath.Dir(dir)
	}
}

func totalSize(metas []index.FileMeta) int64 {
	var total int64
	for _, meta := range metas {
		total += meta.Size
	}
	return total
}

func requiredFreeBytes(total uint64, percent int) uint64 {
	if percent <= 0 || total == 0 {
		return 0
	}
	return (total * uint64(percent)) / 100
}

func isEvictable(meta index.FileMeta) bool {
	for _, chunk := range meta.Chunks {
		if chunk.Dirty {
			return false
		}
	}
	return true
}

func capacityFromConfig(cfg Config) uint64 {
	if cfg.MaxCacheBytes <= 0 {
		return 0
	}
	if cfg.MaxCacheBytes > math.MaxInt64 {
		return math.MaxInt64
	}
	return uint64(cfg.MaxCacheBytes)
}

func defaultLogger() Logger {
	return logHandleAdapter{handle: log.GetLogger("cache-cleaner")}
}

type logHandleAdapter struct {
	handle *log.LogHandle
}

func (l logHandleAdapter) Debugf(format string, args ...any) {
	if l.handle != nil {
		l.handle.Debug().Msgf(format, args...)
	}
}

func (l logHandleAdapter) Infof(format string, args ...any) {
	if l.handle != nil {
		l.handle.Info().Msgf(format, args...)
	}
}

func (l logHandleAdapter) Warnf(format string, args ...any) {
	if l.handle != nil {
		l.handle.Warn().Msgf(format, args...)
	}
}

func (l logHandleAdapter) Errorf(format string, args ...any) {
	if l.handle != nil {
		l.handle.Error().Msgf(format, args...)
	}
}

type dirDiskUsage struct {
	capacity uint64
}

func (d *dirDiskUsage) Stat(path string) (uint64, uint64, error) {
	var used uint64
	err := filepath.WalkDir(path, func(p string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		used += uint64(info.Size())
		return nil
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			used = 0
		} else {
			return 0, 0, err
		}
	}

	capacity := d.capacity
	if capacity == 0 {
		capacity = used
	}
	if capacity == 0 {
		capacity = 1
	}
	if used > capacity {
		used = capacity
	}
	free := capacity - used
	return capacity, free, nil
}
