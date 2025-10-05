package bbolt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/tigrisdata/tigrisfs/pkg/cache/index"
)

const (
	currentSchemaVersion = 1
	bucketStats          = "stats"
	bucketFiles          = "files"
	bucketUploads        = "uploads"

	keySchemaVersion = "schema_version"
	keyUploadSeq     = "upload_seq"
)

var (
	errUnknownSchema = errors.New("cache index: unknown schema version")
)

// Options configures Open behaviour.
type Options struct {
	// Timeout controls bbolt file open timeout. If zero, a sensible default is used.
	Timeout time.Duration
}

// Index implements index.CacheIndex backed by bbolt.
type Index struct {
	db *bolt.DB
}

// Open creates (or reopens) a bbolt-backed cache index at path.
func Open(path string, opts Options) (*Index, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create index dir: %w", err)
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 100 * time.Millisecond
	}

	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: timeout})
	if err != nil {
		return nil, fmt.Errorf("open bbolt: %w", err)
	}

	idx := &Index{db: db}
	if err := idx.ensureSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return idx, nil
}

// Close releases the underlying database handle.
func (i *Index) Close() error {
	if i.db == nil {
		return nil
	}
	return i.db.Close()
}

func (i *Index) Put(ctx context.Context, meta index.FileMeta) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if meta.Path == "" {
		return errors.New("cache index: path must not be empty")
	}

	normalized := normalizeFileMeta(meta)
	return i.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketFiles))
		if bucket == nil {
			return fmt.Errorf("missing bucket %s", bucketFiles)
		}
		data, err := encodeFileMeta(normalized)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(normalized.Path), data)
	})
}

func (i *Index) Get(ctx context.Context, path string) (index.FileMeta, error) {
	if err := ctx.Err(); err != nil {
		return index.FileMeta{}, err
	}
	if path == "" {
		return index.FileMeta{}, errors.New("cache index: path must not be empty")
	}

	var result index.FileMeta
	err := i.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketFiles))
		if bucket == nil {
			return fmt.Errorf("missing bucket %s", bucketFiles)
		}
		raw := bucket.Get([]byte(path))
		if raw == nil {
			return index.ErrNotFound
		}
		meta, err := decodeFileMeta(raw)
		if err != nil {
			return err
		}
		meta.AtimeLocal = time.Now().UTC()
		encoded, err := encodeFileMeta(meta)
		if err != nil {
			return err
		}
		if err := bucket.Put([]byte(path), encoded); err != nil {
			return err
		}
		result = meta
		return nil
	})
	return result, err
}

func (i *Index) Update(ctx context.Context, path string, fn func(index.FileMeta) (index.FileMeta, error)) (index.FileMeta, error) {
	if err := ctx.Err(); err != nil {
		return index.FileMeta{}, err
	}
	if path == "" {
		return index.FileMeta{}, errors.New("cache index: path must not be empty")
	}

	var result index.FileMeta
	err := i.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketFiles))
		if bucket == nil {
			return fmt.Errorf("missing bucket %s", bucketFiles)
		}
		key := []byte(path)
		raw := bucket.Get(key)
		if raw == nil {
			return index.ErrNotFound
		}
		current, err := decodeFileMeta(raw)
		if err != nil {
			return err
		}
		updated, err := fn(cloneFileMeta(current))
		if err != nil {
			return err
		}
		if updated.Path == "" {
			updated.Path = path
		}
		if updated.Path != path {
			updated.Path = path
		}
		normalized := normalizeFileMeta(updated)
		encoded, err := encodeFileMeta(normalized)
		if err != nil {
			return err
		}
		if err := bucket.Put(key, encoded); err != nil {
			return err
		}
		result = normalized
		return nil
	})
	return result, err
}

func (i *Index) Delete(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if path == "" {
		return errors.New("cache index: path must not be empty")
	}
	return i.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketFiles))
		if bucket == nil {
			return fmt.Errorf("missing bucket %s", bucketFiles)
		}
		return bucket.Delete([]byte(path))
	})
}

func (i *Index) ListLRU(ctx context.Context, limit int) ([]index.FileMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	metas := make([]index.FileMeta, 0)
	err := i.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(bucketFiles))
		if bucket == nil {
			return fmt.Errorf("missing bucket %s", bucketFiles)
		}
		return bucket.ForEach(func(k, v []byte) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			meta, err := decodeFileMeta(v)
			if err != nil {
				return err
			}
			metas = append(metas, meta)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sortFileMetasByAtime(metas)
	if limit > 0 && limit < len(metas) {
		metas = metas[:limit]
	}
	return metas, nil
}

func (i *Index) AddUpload(ctx context.Context, entry index.UploadRecord) (index.UploadRecord, error) {
	if err := ctx.Err(); err != nil {
		return index.UploadRecord{}, err
	}
	var result index.UploadRecord
	err := i.db.Update(func(tx *bolt.Tx) error {
		uploads := tx.Bucket([]byte(bucketUploads))
		stats := tx.Bucket([]byte(bucketStats))
		if uploads == nil || stats == nil {
			return fmt.Errorf("missing upload buckets")
		}
		now := time.Now().UTC()
		if entry.CreatedAt.IsZero() {
			entry.CreatedAt = now
		}
		entry.UpdatedAt = now
		if entry.ID == "" {
			seq, err := nextSequence(stats)
			if err != nil {
				return err
			}
			entry.ID = formatUploadID(seq)
		}
		data, err := encodeUpload(entry)
		if err != nil {
			return err
		}
		if err := uploads.Put([]byte(entry.ID), data); err != nil {
			return err
		}
		result = entry
		return nil
	})
	return result, err
}

func (i *Index) ListUploads(ctx context.Context) ([]index.UploadRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	records := make([]index.UploadRecord, 0)
	err := i.db.View(func(tx *bolt.Tx) error {
		uploads := tx.Bucket([]byte(bucketUploads))
		if uploads == nil {
			return fmt.Errorf("missing bucket %s", bucketUploads)
		}
		c := uploads.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			rec, err := decodeUpload(v)
			if err != nil {
				return err
			}
			records = append(records, rec)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

func (i *Index) UpdateUploadStatus(ctx context.Context, id string, status index.UploadStatus, lastError string) (index.UploadRecord, error) {
	if err := ctx.Err(); err != nil {
		return index.UploadRecord{}, err
	}
	if id == "" {
		return index.UploadRecord{}, errors.New("cache index: upload id must not be empty")
	}

	var result index.UploadRecord
	err := i.db.Update(func(tx *bolt.Tx) error {
		uploads := tx.Bucket([]byte(bucketUploads))
		if uploads == nil {
			return fmt.Errorf("missing bucket %s", bucketUploads)
		}
		raw := uploads.Get([]byte(id))
		if raw == nil {
			return index.ErrNotFound
		}
		rec, err := decodeUpload(raw)
		if err != nil {
			return err
		}
		rec.Status = status
		rec.Attempts++
		rec.LastError = lastError
		now := time.Now().UTC()
		if !now.After(rec.CreatedAt) {
			now = rec.CreatedAt.Add(time.Nanosecond)
		}
		rec.UpdatedAt = now
		data, err := encodeUpload(rec)
		if err != nil {
			return err
		}
		if err := uploads.Put([]byte(id), data); err != nil {
			return err
		}
		result = rec
		return nil
	})
	return result, err
}

func (i *Index) ensureSchema() error {
	return i.db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte(bucketFiles)); err != nil {
			return fmt.Errorf("ensure files bucket: %w", err)
		}
		if _, err := tx.CreateBucketIfNotExists([]byte(bucketUploads)); err != nil {
			return fmt.Errorf("ensure uploads bucket: %w", err)
		}
		stats, err := tx.CreateBucketIfNotExists([]byte(bucketStats))
		if err != nil {
			return fmt.Errorf("ensure stats bucket: %w", err)
		}
		versionBytes := stats.Get([]byte(keySchemaVersion))
		if len(versionBytes) == 0 {
			return stats.Put([]byte(keySchemaVersion), []byte(strconv.Itoa(currentSchemaVersion)))
		}
		version, err := strconv.Atoi(string(versionBytes))
		if err != nil {
			return fmt.Errorf("parse schema version: %w", err)
		}
		if version == currentSchemaVersion {
			return nil
		}
		if version > currentSchemaVersion {
			return fmt.Errorf("%w: %d", errUnknownSchema, version)
		}
		if err := migrate(tx, version, currentSchemaVersion); err != nil {
			return err
		}
		return stats.Put([]byte(keySchemaVersion), []byte(strconv.Itoa(currentSchemaVersion)))
	})
}

func migrate(tx *bolt.Tx, from, to int) error {
	version := from
	for version < to {
		switch version {
		case 0:
			if _, err := tx.CreateBucketIfNotExists([]byte(bucketFiles)); err != nil {
				return fmt.Errorf("migrate v0 files: %w", err)
			}
			if _, err := tx.CreateBucketIfNotExists([]byte(bucketUploads)); err != nil {
				return fmt.Errorf("migrate v0 uploads: %w", err)
			}
			version = 1
		default:
			return fmt.Errorf("%w: %d", errUnknownSchema, version)
		}
	}
	return nil
}

func nextSequence(stats *bolt.Bucket) (int, error) {
	raw := stats.Get([]byte(keyUploadSeq))
	var seq int
	if len(raw) > 0 {
		v, err := strconv.Atoi(string(raw))
		if err != nil {
			return 0, fmt.Errorf("parse upload sequence: %w", err)
		}
		seq = v
	}
	seq++
	if err := stats.Put([]byte(keyUploadSeq), []byte(strconv.Itoa(seq))); err != nil {
		return 0, err
	}
	return seq, nil
}

func formatUploadID(seq int) string {
	return fmt.Sprintf("upl-%020d", seq)
}

func normalizeFileMeta(meta index.FileMeta) index.FileMeta {
	clone := cloneFileMeta(meta)
	if clone.AtimeLocal.IsZero() {
		clone.AtimeLocal = time.Now().UTC()
	}
	if clone.MtimeRemote.IsZero() {
		clone.MtimeRemote = time.Now().UTC()
	}
	return clone
}

func cloneFileMeta(meta index.FileMeta) index.FileMeta {
	clone := meta
	if len(meta.Chunks) > 0 {
		clone.Chunks = make([]index.ChunkMeta, len(meta.Chunks))
		copy(clone.Chunks, meta.Chunks)
	}
	return clone
}

func encodeFileMeta(meta index.FileMeta) ([]byte, error) {
	return json.Marshal(meta)
}

func decodeFileMeta(data []byte) (index.FileMeta, error) {
	var meta index.FileMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return index.FileMeta{}, err
	}
	return meta, nil
}

func encodeUpload(entry index.UploadRecord) ([]byte, error) {
	return json.Marshal(entry)
}

func decodeUpload(data []byte) (index.UploadRecord, error) {
	var entry index.UploadRecord
	if err := json.Unmarshal(data, &entry); err != nil {
		return index.UploadRecord{}, err
	}
	return entry, nil
}

func sortFileMetasByAtime(metas []index.FileMeta) {
	sort.Slice(metas, func(i, j int) bool {
		return metas[i].AtimeLocal.Before(metas[j].AtimeLocal)
	})
}
