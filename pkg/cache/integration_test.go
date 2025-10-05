package cache_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/valandreev/tigrisfs/pkg/cache/files"
	"github.com/valandreev/tigrisfs/pkg/cache/index"
	indexbbolt "github.com/valandreev/tigrisfs/pkg/cache/index/bbolt"
)

func TestFileStoreAndIndexIntegration(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()

	indexPath := filepath.Join(baseDir, "index.db")
	dataPath := filepath.Join(baseDir, "cache", "objects", "file.bin")

	container, err := files.OpenContainer(dataPath)
	if err != nil {
		t.Fatalf("open container: %v", err)
	}

	chunkA := []byte("hello world")
	chunkB := []byte("goodbye cache")

	if _, err := container.WriteAt(chunkA, 0); err != nil {
		t.Fatalf("write chunkA: %v", err)
	}
	if _, err := container.WriteAt(chunkB, 512); err != nil {
		t.Fatalf("write chunkB: %v", err)
	}
	if err := container.Fsync(); err != nil {
		t.Fatalf("fsync container: %v", err)
	}
	if err := container.Close(); err != nil {
		t.Fatalf("close container: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	expectedMeta := index.FileMeta{
		Path: "objects/file.bin",
		ETag: "etag-123",
		Size: 512 + int64(len(chunkB)),
		Chunks: []index.ChunkMeta{
			{Offset: 0, Length: int64(len(chunkA)), Dirty: false},
			{Offset: 512, Length: int64(len(chunkB)), Dirty: true},
		},
		MtimeRemote: now,
		AtimeLocal:  now,
	}

	idx, err := indexbbolt.Open(indexPath, indexbbolt.Options{})
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	if err := idx.Put(ctx, expectedMeta); err != nil {
		t.Fatalf("put meta: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("close index: %v", err)
	}

	idx, err = indexbbolt.Open(indexPath, indexbbolt.Options{})
	if err != nil {
		t.Fatalf(" reopen index: %v", err)
	}
	defer func() {
		_ = idx.Close()
	}()

	loaded, err := idx.Get(ctx, expectedMeta.Path)
	if err != nil {
		t.Fatalf("get meta: %v", err)
	}

	if loaded.Path != expectedMeta.Path {
		t.Fatalf("path mismatch: got %q want %q", loaded.Path, expectedMeta.Path)
	}
	if loaded.ETag != expectedMeta.ETag {
		t.Fatalf("etag mismatch: got %q want %q", loaded.ETag, expectedMeta.ETag)
	}
	if loaded.Size != expectedMeta.Size {
		t.Fatalf("size mismatch: got %d want %d", loaded.Size, expectedMeta.Size)
	}
	if !loaded.MtimeRemote.Equal(expectedMeta.MtimeRemote) {
		t.Fatalf("mtime mismatch: got %v want %v", loaded.MtimeRemote, expectedMeta.MtimeRemote)
	}
	if len(loaded.Chunks) != len(expectedMeta.Chunks) {
		t.Fatalf("chunk len mismatch: got %d want %d", len(loaded.Chunks), len(expectedMeta.Chunks))
	}
	for i := range expectedMeta.Chunks {
		if loaded.Chunks[i] != expectedMeta.Chunks[i] {
			t.Fatalf("chunk %d mismatch: got %+v want %+v", i, loaded.Chunks[i], expectedMeta.Chunks[i])
		}
	}
	if loaded.AtimeLocal.Before(expectedMeta.AtimeLocal) {
		t.Fatalf("expected AtimeLocal to advance, got %v <= %v", loaded.AtimeLocal, expectedMeta.AtimeLocal)
	}

	reopened, err := files.OpenContainer(dataPath)
	if err != nil {
		t.Fatalf("reopen container: %v", err)
	}

	bufA := make([]byte, len(chunkA))
	if _, err := reopened.ReadAt(bufA, 0); err != nil {
		t.Fatalf("read chunkA: %v", err)
	}
	if !bytes.Equal(bufA, chunkA) {
		t.Fatalf("chunkA data mismatch: got %q want %q", string(bufA), string(chunkA))
	}

	bufB := make([]byte, len(chunkB))
	if _, err := reopened.ReadAt(bufB, 512); err != nil {
		t.Fatalf("read chunkB: %v", err)
	}
	if !bytes.Equal(bufB, chunkB) {
		t.Fatalf("chunkB data mismatch: got %q want %q", string(bufB), string(chunkB))
	}

	if err := reopened.Close(); err != nil {
		t.Fatalf("close reopened container: %v", err)
	}
}
