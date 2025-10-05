package uploader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"time"

	"github.com/valandreev/tigrisfs/log"
	"github.com/valandreev/tigrisfs/pkg/cache/index"
)

// ErrETagMismatch indicates the remote object changed while a queued upload was pending.
var ErrETagMismatch = errors.New("cache uploader: etag mismatch")

const (
	metricReasonMaxAttempts  = "max_attempts"
	metricReasonOpenChunk    = "open_chunk"
	metricReasonETagMismatch = "etag_mismatch"
	metricReasonBackendError = "backend_error"
	metricReasonContext      = "context_cancel"
)

// ReadSeekCloser combines read, seek, and close semantics for chunk data streams.
type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

// ChunkProvider supplies chunk data to upload for a given record.
type ChunkProvider interface {
	OpenChunk(ctx context.Context, record index.UploadRecord) (ReadSeekCloser, error)
}

// Backend represents the remote storage client responsible for applying uploads.
type Backend interface {
	Upload(ctx context.Context, record index.UploadRecord, data ReadSeekCloser) error
}

// Logger captures structured log output for uploader operations.
type Logger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}

// Sleeper abstracts time.Sleep for deterministic tests.
type Sleeper interface {
	Sleep(d time.Duration)
}

// Metrics captures uploader telemetry.
type Metrics interface {
	RecordQueued(record index.UploadRecord)
	RecordStarted(record index.UploadRecord)
	RecordRetried(record index.UploadRecord)
	RecordCompleted(record index.UploadRecord)
	RecordFailed(record index.UploadRecord, reason string)
}

// Config controls uploader runtime behaviour.
type Config struct {
	MaxConcurrentUploads int
	MaxAttempts          int
	BaseRetryDelay       time.Duration
	MaxRetryDelay        time.Duration
	PollInterval         time.Duration
}

// Option customises uploader construction.
type Option func(*Uploader)

// WithLogger overrides the default logger.
func WithLogger(logger Logger) Option {
	return func(u *Uploader) {
		u.logger = logger
	}
}

// WithSleeper overrides the sleep implementation (useful for tests).
func WithSleeper(sleeper Sleeper) Option {
	return func(u *Uploader) {
		u.sleeper = sleeper
	}
}

// WithChunkProvider sets the chunk provider used to stream data to the backend.
func WithChunkProvider(provider ChunkProvider) Option {
	return func(u *Uploader) {
		u.provider = provider
	}
}

// WithMetrics sets a custom metrics collector.
func WithMetrics(metrics Metrics) Option {
	return func(u *Uploader) {
		u.metrics = metrics
	}
}

// RetryableError wraps an underlying error and marks it retryable.
type RetryableError struct {
	Err error
}

// Error implements the error interface.
func (e RetryableError) Error() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

// Unwrap exposes the wrapped error.
func (e RetryableError) Unwrap() error { return e.Err }

// Retryable marks this error as safe to retry.
func (RetryableError) Retryable() bool { return true }

// Uploader coordinates background uploads from the cache to the backing store.
type Uploader struct {
	cfg      Config
	idx      index.CacheIndex
	backend  Backend
	logger   Logger
	sleeper  Sleeper
	provider ChunkProvider
	metrics  Metrics

	mu       sync.Mutex
	queued   map[string]struct{}
	inFlight map[string]struct{}
	tasks    chan index.UploadRecord
	running  bool
}

// New constructs a Uploader with the provided configuration.
func New(cfg Config, idx index.CacheIndex, backend Backend, opts ...Option) (*Uploader, error) {
	if idx == nil {
		return nil, errors.New("cache uploader: cache index is required")
	}
	if backend == nil {
		return nil, errors.New("cache uploader: backend client is required")
	}

	cfg = applyDefaults(cfg)

	u := &Uploader{
		cfg:      cfg,
		idx:      idx,
		backend:  backend,
		logger:   defaultLogger(),
		sleeper:  realSleeper{},
		metrics:  noopMetrics{},
		queued:   make(map[string]struct{}),
		inFlight: make(map[string]struct{}),
	}

	for _, opt := range opts {
		opt(u)
	}

	if u.logger == nil {
		u.logger = defaultLogger()
	}
	if u.sleeper == nil {
		u.sleeper = realSleeper{}
	}
	if u.metrics == nil {
		u.metrics = noopMetrics{}
	}
	if u.provider == nil {
		return nil, errors.New("cache uploader: chunk provider is required")
	}

	return u, nil
}

// Run starts the uploader loop until the provided context is cancelled.
func (u *Uploader) Run(ctx context.Context) error {
	workerCount := u.cfg.MaxConcurrentUploads
	if workerCount <= 0 {
		workerCount = 1
	}

	tasks := make(chan index.UploadRecord, workerCount*2)
	var wg sync.WaitGroup

	u.mu.Lock()
	if u.running {
		u.mu.Unlock()
		return errors.New("cache uploader: already running")
	}
	u.running = true
	u.tasks = tasks
	u.mu.Unlock()

	defer func() {
		close(tasks)
		wg.Wait()
		u.mu.Lock()
		u.tasks = nil
		u.running = false
		u.queued = make(map[string]struct{})
		u.inFlight = make(map[string]struct{})
		u.mu.Unlock()
	}()

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u.worker(ctx, tasks)
		}()
	}

	if err := u.scanAndQueue(ctx); err != nil {
		u.logger.Warnf("initial upload scan failed: %v", err)
	}

	ticker := time.NewTicker(u.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := u.scanAndQueue(ctx); err != nil {
				u.logger.Warnf("upload scan failed: %v", err)
			}
		}
	}
}

func (u *Uploader) worker(ctx context.Context, tasks <-chan index.UploadRecord) {
	for {
		select {
		case <-ctx.Done():
			return
		case record, ok := <-tasks:
			if !ok {
				return
			}
			if !u.startProcessing(record.ID) {
				continue
			}
			u.processRecord(ctx, record)
		}
	}
}

func (u *Uploader) scanAndQueue(ctx context.Context) error {
	uploads, err := u.idx.ListUploads(ctx)
	if err != nil {
		return err
	}
	for _, record := range uploads {
		if !u.shouldProcess(record) {
			continue
		}
		u.enqueue(ctx, record)
	}
	return nil
}

func (u *Uploader) shouldProcess(record index.UploadRecord) bool {
	switch record.Status {
	case index.UploadStatusComplete:
		return false
	case index.UploadStatusFailed:
		return false
	default:
		return true
	}
}

func (u *Uploader) enqueue(ctx context.Context, record index.UploadRecord) {
	u.mu.Lock()
	if !u.running || u.tasks == nil {
		u.mu.Unlock()
		return
	}
	if _, exists := u.queued[record.ID]; exists {
		u.mu.Unlock()
		return
	}
	if _, exists := u.inFlight[record.ID]; exists {
		u.mu.Unlock()
		return
	}
	u.queued[record.ID] = struct{}{}
	tasks := u.tasks
	u.mu.Unlock()

	u.metrics.RecordQueued(record)

	select {
	case <-ctx.Done():
		u.mu.Lock()
		delete(u.queued, record.ID)
		u.mu.Unlock()
	case tasks <- record:
	}
}

func (u *Uploader) startProcessing(id string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()

	if _, queued := u.queued[id]; queued {
		delete(u.queued, id)
		u.inFlight[id] = struct{}{}
		return true
	}
	if _, running := u.inFlight[id]; running {
		return false
	}
	u.inFlight[id] = struct{}{}
	return true
}

func (u *Uploader) finishProcessing(id string) {
	u.mu.Lock()
	delete(u.inFlight, id)
	u.mu.Unlock()
}

func (u *Uploader) processRecord(ctx context.Context, record index.UploadRecord) {
	finished := false
	defer func() {
		if !finished {
			u.finishProcessing(record.ID)
		}
	}()

	attemptsBefore := record.Attempts / 2
	if attemptsBefore >= u.cfg.MaxAttempts {
		u.logger.Warnf("upload %s reached max attempts", record.ID)
		if failed, err := u.idx.UpdateUploadStatus(ctx, record.ID, index.UploadStatusFailed, "max attempts reached"); err != nil {
			u.logger.Errorf("mark upload %s failed: %v", record.ID, err)
		} else {
			u.metrics.RecordFailed(failed, metricReasonMaxAttempts)
		}
		return
	}

	updated, err := u.idx.UpdateUploadStatus(ctx, record.ID, index.UploadStatusInProgress, "")
	if err != nil {
		if errors.Is(err, index.ErrNotFound) {
			u.logger.Warnf("upload %s missing from index", record.ID)
		} else {
			u.logger.Errorf("set upload %s in-progress failed: %v", record.ID, err)
		}
		return
	}
	u.metrics.RecordStarted(updated)

	chunk, err := u.provider.OpenChunk(ctx, updated)
	if err != nil {
		msg := fmt.Sprintf("open chunk: %v", err)
		u.logger.Errorf("upload %s open chunk failed: %v", record.ID, err)
		if failed, updateErr := u.idx.UpdateUploadStatus(ctx, record.ID, index.UploadStatusFailed, msg); updateErr != nil {
			u.logger.Errorf("mark upload %s failed after open chunk: %v", record.ID, updateErr)
		} else {
			u.metrics.RecordFailed(failed, metricReasonOpenChunk)
		}
		return
	}
	defer func() {
		_ = chunk.Close()
	}()

	currentAttempt := attemptsBefore + 1

	if err := u.backend.Upload(ctx, updated, chunk); err != nil {
		if errors.Is(err, ErrETagMismatch) {
			msg := err.Error()
			u.logger.Warnf("etag mismatch for %s: %s", record.Path, msg)
			if failed, updateErr := u.idx.UpdateUploadStatus(ctx, record.ID, index.UploadStatusFailed, msg); updateErr != nil {
				u.logger.Errorf("mark etag mismatch for %s failed: %v", record.ID, updateErr)
			} else {
				u.metrics.RecordFailed(failed, metricReasonETagMismatch)
			}
			return
		}

		if isContextError(err) {
			u.logger.Warnf("upload %s cancelled: %v", record.ID, err)
			u.metrics.RecordRetried(updated)
			if _, updateErr := u.idx.UpdateUploadStatus(ctx, record.ID, index.UploadStatusQueued, err.Error()); updateErr != nil {
				u.logger.Errorf("requeue upload %s after cancel failed: %v", record.ID, updateErr)
			}
			return
		}

		if isRetryable(err) && currentAttempt < u.cfg.MaxAttempts {
			delay := u.backoffDelay(currentAttempt)
			u.logger.Warnf("retrying upload %s in %s: %v", record.ID, delay, err)
			u.metrics.RecordRetried(updated)
			updatedRecord, updateErr := u.idx.UpdateUploadStatus(ctx, record.ID, index.UploadStatusQueued, err.Error())
			if updateErr != nil {
				u.logger.Errorf("requeue upload %s failed: %v", record.ID, updateErr)
				return
			}
			u.finishProcessing(record.ID)
			finished = true
			u.sleeper.Sleep(delay)
			u.enqueue(ctx, updatedRecord)
			return
		}

		msg := err.Error()
		u.logger.Warnf("upload %s failed: %s", record.ID, msg)
		if failed, updateErr := u.idx.UpdateUploadStatus(ctx, record.ID, index.UploadStatusFailed, msg); updateErr != nil {
			u.logger.Errorf("mark upload %s failed state: %v", record.ID, updateErr)
		} else {
			u.metrics.RecordFailed(failed, metricReasonBackendError)
		}
		return
	}

	if completed, err := u.idx.UpdateUploadStatus(ctx, record.ID, index.UploadStatusComplete, ""); err != nil {
		u.logger.Errorf("mark upload %s complete failed: %v", record.ID, err)
	} else {
		u.metrics.RecordCompleted(completed)
	}
}

func (u *Uploader) backoffDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	base := u.cfg.BaseRetryDelay
	if base <= 0 {
		base = 100 * time.Millisecond
	}
	pow := math.Pow(2, float64(attempt-1))
	delay := time.Duration(float64(base) * pow)
	if delay > u.cfg.MaxRetryDelay {
		return u.cfg.MaxRetryDelay
	}
	if delay < base {
		return base
	}
	return delay
}

func applyDefaults(cfg Config) Config {
	if cfg.MaxConcurrentUploads <= 0 {
		cfg.MaxConcurrentUploads = 2
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.BaseRetryDelay <= 0 {
		cfg.BaseRetryDelay = 100 * time.Millisecond
	}
	if cfg.MaxRetryDelay <= 0 {
		cfg.MaxRetryDelay = 5 * time.Second
	}
	if cfg.MaxRetryDelay < cfg.BaseRetryDelay {
		cfg.MaxRetryDelay = cfg.BaseRetryDelay
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 200 * time.Millisecond
	}
	return cfg
}

func defaultLogger() Logger {
	return logHandleAdapter{handle: log.GetLogger("cache-uploader")}
}

// realSleeper calls time.Sleep directly.
type realSleeper struct{}

func (realSleeper) Sleep(d time.Duration) { time.Sleep(d) }

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

type noopMetrics struct{}

func (noopMetrics) RecordQueued(index.UploadRecord) {}

func (noopMetrics) RecordStarted(index.UploadRecord) {}

func (noopMetrics) RecordRetried(index.UploadRecord) {}

func (noopMetrics) RecordCompleted(index.UploadRecord) {}

func (noopMetrics) RecordFailed(index.UploadRecord, string) {}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	type retryable interface {
		Retryable() bool
	}
	type temporary interface {
		Temporary() bool
	}
	var r retryable
	if errors.As(err, &r) && r.Retryable() {
		return true
	}
	var t temporary
	if errors.As(err, &t) && t.Temporary() {
		return true
	}
	return false
}

func isContextError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// DebugString returns a concise summary for logging and testing.
func (u *Uploader) DebugString() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return fmt.Sprintf("queued=%d inFlight=%d", len(u.queued), len(u.inFlight))
}
