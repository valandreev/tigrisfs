package cleaner_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/valandreev/tigrisfs/pkg/cache/cleaner"
	"github.com/valandreev/tigrisfs/pkg/cache/index"
	"github.com/valandreev/tigrisfs/pkg/cache/index/indextest"
)

func TestCleanerEvictsLRUToMeetCapacity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()

	idx := indextest.MemoryIndexFactory()(t)

	mustWriteFile(t, root, "objects/a.bin", 40)
	mustWriteFile(t, root, "objects/b.bin", 30)
	mustWriteFile(t, root, "objects/c.bin", 20)

	putMeta(t, ctx, idx, "objects/a.bin", 40)
	putMeta(t, ctx, idx, "objects/b.bin", 30)
	putMeta(t, ctx, idx, "objects/c.bin", 20)

	setAtime(t, ctx, idx, "objects/c.bin", time.Now().Add(2*time.Minute))
	setAtime(t, ctx, idx, "objects/b.bin", time.Now().Add(1*time.Minute))

	cfg := cleaner.Config{
		CacheDir:       root,
		MaxCacheBytes:  60,
		MinFreePercent: 0,
	}

	c, err := cleaner.New(cfg, idx, cleaner.WithDiskUsage(fakeDisk{capacity: 500}))
	if err != nil {
		t.Fatalf("new cleaner: %v", err)
	}

	report, err := c.RunOnce(ctx, cleaner.Trigger{Reason: cleaner.TriggerReasonMaintenance})
	if err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}

	if report.TotalAfter > cfg.MaxCacheBytes {
		t.Fatalf("expected usage <= %d, got %d", cfg.MaxCacheBytes, report.TotalAfter)
	}

	if len(report.Evicted) != 1 || report.Evicted[0] != "objects/a.bin" {
		t.Fatalf("expected evicted [objects/a.bin], got %v", report.Evicted)
	}

	if _, err := os.Stat(filepath.Join(root, "objects", "a.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected a.bin removed, got err=%v", err)
	}

	if _, err := idx.Get(ctx, "objects/a.bin"); !errors.Is(err, index.ErrNotFound) {
		t.Fatalf("expected index entry removed, err=%v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "objects", "b.bin")); err != nil {
		t.Fatalf("expected b.bin to remain, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "objects", "c.bin")); err != nil {
		t.Fatalf("expected c.bin to remain, err=%v", err)
	}
}

func TestCleanerEmergencyFreesSpace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	idx := indextest.MemoryIndexFactory()(t)

	mustWriteFile(t, root, "a.bin", 40)
	mustWriteFile(t, root, "b.bin", 35)
	mustWriteFile(t, root, "c.bin", 15)

	putMeta(t, ctx, idx, "a.bin", 40)
	putMeta(t, ctx, idx, "b.bin", 35)
	putMeta(t, ctx, idx, "c.bin", 15)

	setAtime(t, ctx, idx, "c.bin", time.Now().Add(3*time.Minute))
	setAtime(t, ctx, idx, "b.bin", time.Now().Add(2*time.Minute))

	cfg := cleaner.Config{
		CacheDir:       root,
		MaxCacheBytes:  200,
		MinFreePercent: 30,
	}

	disk := fakeDisk{capacity: 120}
	c, err := cleaner.New(cfg, idx, cleaner.WithDiskUsage(disk))
	if err != nil {
		t.Fatalf("new cleaner: %v", err)
	}

	report, err := c.RunOnce(ctx, cleaner.Trigger{Reason: cleaner.TriggerReasonENOSPC})
	if err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}

	if !report.Emergency {
		t.Fatalf("expected emergency flag set")
	}

	totalAfter := report.TotalAfter
	totalBefore := report.TotalBefore
	if totalAfter >= totalBefore {
		t.Fatalf("expected usage to drop, before=%d after=%d", totalBefore, totalAfter)
	}

	total, free, err := disk.Stat(root)
	if err != nil {
		t.Fatalf("stat disk: %v", err)
	}
	freePercent := (float64(free) / float64(total)) * 100
	if freePercent < float64(cfg.MinFreePercent) {
		t.Fatalf("expected free >= %d%%, got %.2f%%", cfg.MinFreePercent, freePercent)
	}
}

func TestCleanerEmergencyFatalWhenInsufficientSpace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	idx := indextest.MemoryIndexFactory()(t)

	mustWriteFile(t, root, "cleanable.bin", 30)
	mustWriteFile(t, root, "dirty.bin", 60)

	putMeta(t, ctx, idx, "cleanable.bin", 30)
	putMeta(t, ctx, idx, "dirty.bin", 60)
	markDirty(t, ctx, idx, "dirty.bin")

	cfg := cleaner.Config{
		CacheDir:       root,
		MaxCacheBytes:  500,
		MinFreePercent: 70,
	}

	disk := fakeDisk{capacity: 120}
	c, err := cleaner.New(cfg, idx, cleaner.WithDiskUsage(disk))
	if err != nil {
		t.Fatalf("new cleaner: %v", err)
	}

	report, err := c.RunOnce(ctx, cleaner.Trigger{Reason: cleaner.TriggerReasonENOSPC})
	if !errors.Is(err, cleaner.ErrFatalCondition) {
		t.Fatalf("expected ErrFatalCondition, got %v", err)
	}

	if len(report.Evicted) != 1 || report.Evicted[0] != "cleanable.bin" {
		t.Fatalf("expected cleanable.bin evicted, report=%v", report.Evicted)
	}

	if _, err := idx.Get(ctx, "dirty.bin"); err != nil {
		t.Fatalf("expected dirty.bin to remain in index: %v", err)
	}
}

type fakeDisk struct {
	capacity uint64
}

func (f fakeDisk) Stat(path string) (uint64, uint64, error) {
	var used uint64
	err := filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		used += uint64(info.Size())
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	if used > f.capacity {
		used = f.capacity
	}
	free := f.capacity - used
	return f.capacity, free, nil
}

func mustWriteFile(t *testing.T, root, relative string, size int64) {
	t.Helper()

	fullPath := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}
	if err := os.WriteFile(fullPath, data, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func putMeta(t *testing.T, ctx context.Context, idx index.CacheIndex, path string, size int64) {
	t.Helper()

	meta := index.FileMeta{
		Path:        filepath.ToSlash(path),
		Size:        size,
		ETag:        "etag",
		MtimeRemote: time.Now().UTC(),
		AtimeLocal:  time.Now().UTC(),
		Chunks: []index.ChunkMeta{
			{Offset: 0, Length: size, Dirty: false},
		},
	}
	if err := idx.Put(ctx, meta); err != nil {
		t.Fatalf("Put meta failed: %v", err)
	}
}

func setAtime(t *testing.T, ctx context.Context, idx index.CacheIndex, path string, when time.Time) {
	t.Helper()

	_, err := idx.Update(ctx, filepath.ToSlash(path), func(meta index.FileMeta) (index.FileMeta, error) {
		meta.AtimeLocal = when
		return meta, nil
	})
	if err != nil {
		t.Fatalf("set Atime failed: %v", err)
	}
}

func markDirty(t *testing.T, ctx context.Context, idx index.CacheIndex, path string) {
	t.Helper()

	_, err := idx.Update(ctx, filepath.ToSlash(path), func(meta index.FileMeta) (index.FileMeta, error) {
		for i := range meta.Chunks {
			meta.Chunks[i].Dirty = true
		}
		return meta, nil
	})
	if err != nil {
		t.Fatalf("mark dirty failed: %v", err)
	}
}
