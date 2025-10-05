package bbolt

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/valandreev/tigrisfs/pkg/cache/index"
	"github.com/valandreev/tigrisfs/pkg/cache/index/indextest"
)

func TestCacheIndexContractWithBbolt(t *testing.T) {
	indextest.RunCacheIndexContract(t, func(tb testing.TB) index.CacheIndex {
		tb.Helper()

		dir := tb.TempDir()
		path := filepath.Join(dir, "index.db")
		idx, err := Open(path, Options{})
		if err != nil {
			tb.Fatalf("failed to open bbolt index: %v", err)
		}
		tb.Cleanup(func() {
			_ = idx.Close()
		})
		return idx
	})
}

func TestOpenInitializesSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.db")

	idx, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	version := readSchemaVersion(t, path)
	if version != currentSchemaVersion {
		t.Fatalf("expected schema version %d, got %d", currentSchemaVersion, version)
	}
}

func TestOpenUpgradesLegacySchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.db")

	createLegacySchema(t, path)

	idx, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	version := readSchemaVersion(t, path)
	if version != currentSchemaVersion {
		t.Fatalf("expected schema version %d after upgrade, got %d", currentSchemaVersion, version)
	}
}

func TestUploadsPersistAcrossReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "index.db")

	idx, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("first Open returned error: %v", err)
	}

	upload := index.UploadRecord{
		Path:   "/objects/video.mp4",
		Offset: 1024,
		Length: 2048,
		Status: index.UploadStatusQueued,
	}
	created, err := idx.AddUpload(ctx, upload)
	if err != nil {
		t.Fatalf("AddUpload failed: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("expected AddUpload to assign ID")
	}
	progressed, err := idx.UpdateUploadStatus(ctx, created.ID, index.UploadStatusInProgress, "")
	if err != nil {
		t.Fatalf("UpdateUploadStatus failed: %v", err)
	}
	if progressed.Attempts != 1 {
		t.Fatalf("expected attempts to be 1 after first update, got %d", progressed.Attempts)
	}
	if !progressed.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("expected CreatedAt to remain stable across updates")
	}

	if err := idx.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	idx, err = Open(path, Options{})
	if err != nil {
		t.Fatalf("re-open returned error: %v", err)
	}
	defer func() { _ = idx.Close() }()

	uploads, err := idx.ListUploads(ctx)
	if err != nil {
		t.Fatalf("ListUploads failed: %v", err)
	}
	if len(uploads) != 1 {
		t.Fatalf("expected 1 upload after reopen, got %d", len(uploads))
	}
	persisted := uploads[0]
	if persisted.ID != created.ID {
		t.Fatalf("expected persisted ID %s, got %s", created.ID, persisted.ID)
	}
	if persisted.Status != index.UploadStatusInProgress {
		t.Fatalf("expected status %s after reopen, got %s", index.UploadStatusInProgress, persisted.Status)
	}
	if !persisted.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("expected CreatedAt to persist across reopen")
	}
	if persisted.Attempts != progressed.Attempts {
		t.Fatalf("expected attempts %d after reopen, got %d", progressed.Attempts, persisted.Attempts)
	}
	if persisted.UpdatedAt.IsZero() {
		t.Fatalf("expected UpdatedAt to be set on persisted record")
	}

	completed, err := idx.UpdateUploadStatus(ctx, created.ID, index.UploadStatusComplete, "")
	if err != nil {
		t.Fatalf("UpdateUploadStatus after reopen failed: %v", err)
	}
	if completed.Status != index.UploadStatusComplete {
		t.Fatalf("expected status complete, got %s", completed.Status)
	}
	if completed.Attempts != progressed.Attempts+1 {
		t.Fatalf("expected attempts to increment to %d, got %d", progressed.Attempts+1, completed.Attempts)
	}
	if !completed.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("expected CreatedAt unchanged after second update")
	}
	if !completed.UpdatedAt.After(persisted.UpdatedAt) {
		t.Fatalf("expected UpdatedAt to advance after status change")
	}
}

func readSchemaVersion(t *testing.T, path string) int {
	t.Helper()

	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("failed to open db for inspection: %v", err)
	}
	defer func() { _ = db.Close() }()

	var version int
	if err := db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketStats))
		if bucket == nil {
			return nil
		}
		data := bucket.Get([]byte(keySchemaVersion))
		if len(data) == 0 {
			return nil
		}
		v, err := strconv.Atoi(string(data))
		if err != nil {
			return err
		}
		version = v
		return nil
	}); err != nil {
		t.Fatalf("failed to read schema version: %v", err)
	}
	return version
}

func createLegacySchema(t *testing.T, path string) {
	t.Helper()

	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.Update(func(tx *bolt.Tx) error {
		stats, err := tx.CreateBucketIfNotExists([]byte(bucketStats))
		if err != nil {
			return err
		}
		if err := stats.Put([]byte(keySchemaVersion), []byte("0")); err != nil {
			return err
		}
		// legacy schema may lack uploads bucket; ensure files bucket exists to simulate data
		if _, err := tx.CreateBucketIfNotExists([]byte(bucketFiles)); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("failed to write legacy schema: %v", err)
	}
}
