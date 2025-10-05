package bbolt

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/tigrisdata/tigrisfs/pkg/cache/index"
	"github.com/tigrisdata/tigrisfs/pkg/cache/index/indextest"
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
