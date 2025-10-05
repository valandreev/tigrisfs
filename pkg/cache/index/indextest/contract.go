package indextest

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/tigrisdata/tigrisfs/pkg/cache/index"
)

type CacheIndexFactory func(tb testing.TB) index.CacheIndex

type contractTestCase struct {
	name   string
	testFn func(t *testing.T, idx index.CacheIndex)
}

// RunCacheIndexContract exercises the CacheIndex interface against a supplied factory.
func RunCacheIndexContract(t *testing.T, factory CacheIndexFactory) {
	t.Helper()

	cases := []contractTestCase{
		{
			name: "put and get round trip",
			testFn: func(t *testing.T, idx index.CacheIndex) {
				t.Helper()

				ctx := context.Background()
				meta := sampleMeta("/docs/report.pdf", "etag-123", 4096, time.Unix(10, 0))
				if err := idx.Put(ctx, meta); err != nil {
					t.Fatalf("Put returned error: %v", err)
				}

				fetched, err := idx.Get(ctx, meta.Path)
				if err != nil {
					t.Fatalf("Get returned error: %v", err)
				}
				assertMetasEqual(t, meta, fetched, withDynamicTimes())
			},
		},
		{
			name: "get missing returns ErrNotFound",
			testFn: func(t *testing.T, idx index.CacheIndex) {
				t.Helper()

				ctx := context.Background()
				_, err := idx.Get(ctx, "/missing.txt")
				if !errors.Is(err, index.ErrNotFound) {
					t.Fatalf("expected ErrNotFound, got %v", err)
				}
			},
		},
		{
			name: "put overwrites existing metadata",
			testFn: func(t *testing.T, idx index.CacheIndex) {
				t.Helper()

				ctx := context.Background()
				original := sampleMeta("/file.bin", "etag-a", 1024, time.Unix(11, 0))
				updated := sampleMeta("/file.bin", "etag-b", 2048, time.Unix(12, 0))
				updated.Chunks[0].Dirty = true

				if err := idx.Put(ctx, original); err != nil {
					t.Fatalf("Put original failed: %v", err)
				}
				if err := idx.Put(ctx, updated); err != nil {
					t.Fatalf("Put updated failed: %v", err)
				}

				fetched, err := idx.Get(ctx, original.Path)
				if err != nil {
					t.Fatalf("Get returned error: %v", err)
				}
				assertMetasEqual(t, updated, fetched, withDynamicTimes())
			},
		},
		{
			name: "update applies atomic mutation",
			testFn: func(t *testing.T, idx index.CacheIndex) {
				t.Helper()

				ctx := context.Background()
				meta := sampleMeta("/data/chunk.bin", "etag-1", 10_485_760, time.Unix(13, 0))
				if err := idx.Put(ctx, meta); err != nil {
					t.Fatalf("Put failed: %v", err)
				}

				updated, err := idx.Update(ctx, meta.Path, func(existing index.FileMeta) (index.FileMeta, error) {
					if len(existing.Chunks) == 0 {
						return existing, nil
					}
					existing.Chunks[0].Dirty = true
					existing.ETag = "etag-2"
					existing.Size += 512
					return existing, nil
				})
				if err != nil {
					t.Fatalf("Update returned error: %v", err)
				}

				fetched, err := idx.Get(ctx, meta.Path)
				if err != nil {
					t.Fatalf("Get returned error: %v", err)
				}
				assertMetasEqual(t, updated, fetched, withDynamicTimes())
				if len(fetched.Chunks) == 0 || !fetched.Chunks[0].Dirty {
					t.Fatalf("expected chunk 0 to be dirty after update")
				}
			},
		},
		{
			name: "update missing returns ErrNotFound",
			testFn: func(t *testing.T, idx index.CacheIndex) {
				t.Helper()

				ctx := context.Background()
				_, err := idx.Update(ctx, "/missing.bin", func(meta index.FileMeta) (index.FileMeta, error) {
					return meta, nil
				})
				if !errors.Is(err, index.ErrNotFound) {
					t.Fatalf("expected ErrNotFound, got %v", err)
				}
			},
		},
		{
			name: "delete removes metadata and is idempotent",
			testFn: func(t *testing.T, idx index.CacheIndex) {
				t.Helper()

				ctx := context.Background()
				meta := sampleMeta("/tmp/foo", "etag", 128, time.Unix(14, 0))
				if err := idx.Put(ctx, meta); err != nil {
					t.Fatalf("Put failed: %v", err)
				}

				if err := idx.Delete(ctx, meta.Path); err != nil {
					t.Fatalf("Delete returned error: %v", err)
				}
				if err := idx.Delete(ctx, meta.Path); err != nil {
					t.Fatalf("Delete should be idempotent, got error: %v", err)
				}

				if _, err := idx.Get(ctx, meta.Path); !errors.Is(err, index.ErrNotFound) {
					t.Fatalf("expected ErrNotFound after delete, got %v", err)
				}
			},
		},
		{
			name: "list LRU orders by atime and honors limit",
			testFn: func(t *testing.T, idx index.CacheIndex) {
				t.Helper()

				ctx := context.Background()
				base := time.Unix(100, 0)
				metas := []index.FileMeta{
					sampleMeta("/a", "etag-a", 100, base.Add(time.Second)),
					sampleMeta("/b", "etag-b", 200, base.Add(2*time.Second)),
					sampleMeta("/c", "etag-c", 300, base.Add(3*time.Second)),
				}
				for _, meta := range metas {
					if err := idx.Put(ctx, meta); err != nil {
						t.Fatalf("Put failed: %v", err)
					}
				}

				if _, err := idx.Get(ctx, "/c"); err != nil {
					t.Fatalf("Get on /c failed: %v", err)
				}

				results, err := idx.ListLRU(ctx, 2)
				if err != nil {
					t.Fatalf("ListLRU returned error: %v", err)
				}
				if len(results) != 2 {
					t.Fatalf("expected 2 entries, got %d", len(results))
				}
				if results[0].Path != "/a" || results[1].Path != "/b" {
					t.Fatalf("expected [/a /b], got [%s %s]", results[0].Path, results[1].Path)
				}

				if results[0].AtimeLocal.After(results[1].AtimeLocal) {
					t.Fatalf("expected first result to be least recently used")
				}
			},
		},
		{
			name: "uploads lifecycle",
			testFn: func(t *testing.T, idx index.CacheIndex) {
				t.Helper()

				ctx := context.Background()
				upload := index.UploadRecord{
					Path:   "/uploads/video.mp4",
					Offset: 0,
					Length: 64 << 20,
					Status: index.UploadStatusQueued,
				}
				created, err := idx.AddUpload(ctx, upload)
				if err != nil {
					t.Fatalf("AddUpload failed: %v", err)
				}
				if created.ID == "" {
					t.Fatalf("expected AddUpload to assign ID")
				}
				if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
					t.Fatalf("expected timestamps set on AddUpload")
				}

				uploads, err := idx.ListUploads(ctx)
				if err != nil {
					t.Fatalf("ListUploads failed: %v", err)
				}
				if len(uploads) != 1 {
					t.Fatalf("expected 1 upload, got %d", len(uploads))
				}
				if uploads[0].ID != created.ID {
					t.Fatalf("expected upload ID %s, got %s", created.ID, uploads[0].ID)
				}

				progressed, err := idx.UpdateUploadStatus(ctx, created.ID, index.UploadStatusInProgress, "")
				if err != nil {
					t.Fatalf("UpdateUploadStatus failed: %v", err)
				}
				if progressed.Status != index.UploadStatusInProgress {
					t.Fatalf("expected status %s, got %s", index.UploadStatusInProgress, progressed.Status)
				}
				if progressed.Attempts != 1 {
					t.Fatalf("expected attempts to increment, got %d", progressed.Attempts)
				}

				failed, err := idx.UpdateUploadStatus(ctx, created.ID, index.UploadStatusFailed, "network err")
				if err != nil {
					t.Fatalf("UpdateUploadStatus failed: %v", err)
				}
				if failed.Status != index.UploadStatusFailed {
					t.Fatalf("expected failed status, got %s", failed.Status)
				}
				if failed.LastError != "network err" {
					t.Fatalf("expected last error recorded")
				}
				if failed.Attempts != 2 {
					t.Fatalf("expected attempts to increment again, got %d", failed.Attempts)
				}
				if !failed.UpdatedAt.After(failed.CreatedAt) {
					t.Fatalf("expected updated timestamp to be newer than created")
				}

				if _, err := idx.UpdateUploadStatus(ctx, "missing", index.UploadStatusQueued, ""); !errors.Is(err, index.ErrNotFound) {
					t.Fatalf("expected ErrNotFound on missing upload, got %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			idx := factory(t)
			defer func() {
				if closer, ok := idx.(interface{ Close() error }); ok {
					_ = closer.Close()
				}
			}()
			tc.testFn(t, idx)
		})
	}
}

// MemoryIndexFactory returns a factory producing the in-memory reference implementation.
func MemoryIndexFactory() CacheIndexFactory {
	return func(tb testing.TB) index.CacheIndex {
		tb.Helper()

		idx := newMemoryIndex()
		tb.Cleanup(func() {
			_ = idx.Close()
		})
		return idx
	}
}

func withDynamicTimes() cmpOption {
	return func(expected, actual *index.FileMeta) {
		if !actual.AtimeLocal.IsZero() {
			expected.AtimeLocal = actual.AtimeLocal
		}
		if !actual.MtimeRemote.IsZero() {
			expected.MtimeRemote = actual.MtimeRemote
		}
	}
}

type cmpOption func(expected, actual *index.FileMeta)

func assertMetasEqual(t *testing.T, expected, actual index.FileMeta, opts ...cmpOption) {
	t.Helper()

	for _, opt := range opts {
		opt(&expected, &actual)
	}

	if expected.Path != actual.Path {
		t.Fatalf("path mismatch: expected %s got %s", expected.Path, actual.Path)
	}
	if expected.ETag != actual.ETag {
		t.Fatalf("etag mismatch: expected %s got %s", expected.ETag, actual.ETag)
	}
	if expected.Size != actual.Size {
		t.Fatalf("size mismatch: expected %d got %d", expected.Size, actual.Size)
	}
	if !expected.MtimeRemote.Equal(actual.MtimeRemote) {
		t.Fatalf("mtime mismatch: expected %s got %s", expected.MtimeRemote, actual.MtimeRemote)
	}
	if !expected.AtimeLocal.Equal(actual.AtimeLocal) {
		t.Fatalf("atime mismatch: expected %s got %s", expected.AtimeLocal, actual.AtimeLocal)
	}

	if len(expected.Chunks) != len(actual.Chunks) {
		t.Fatalf("chunks length mismatch: expected %d got %d", len(expected.Chunks), len(actual.Chunks))
	}
	for i := range expected.Chunks {
		exp := expected.Chunks[i]
		got := actual.Chunks[i]
		if exp.Offset != got.Offset || exp.Length != got.Length || exp.Dirty != got.Dirty {
			t.Fatalf("chunk[%d] mismatch: expected %+v got %+v", i, exp, got)
		}
	}
}

func sampleMeta(path, etag string, size int64, atime time.Time) index.FileMeta {
	return index.FileMeta{
		Path:        path,
		ETag:        etag,
		Size:        size,
		MtimeRemote: time.Unix(0, 0),
		AtimeLocal:  atime,
		Chunks: []index.ChunkMeta{
			{
				Offset: 0,
				Length: size,
				Dirty:  false,
			},
		},
	}
}

type memoryIndex struct {
	files       map[string]index.FileMeta
	uploads     map[string]index.UploadRecord
	uploadOrder []string
	nextUpload  int
}

func newMemoryIndex() *memoryIndex {
	return &memoryIndex{
		files:   make(map[string]index.FileMeta),
		uploads: make(map[string]index.UploadRecord),
	}
}

func (m *memoryIndex) Close() error {
	return nil
}

func (m *memoryIndex) Put(ctx context.Context, meta index.FileMeta) error {
	if meta.Path == "" {
		return errors.New("path must not be empty")
	}
	if meta.AtimeLocal.IsZero() {
		meta.AtimeLocal = time.Now().UTC()
	}
	if meta.MtimeRemote.IsZero() {
		meta.MtimeRemote = time.Now().UTC()
	}
	m.files[meta.Path] = cloneMeta(meta)
	return nil
}

func (m *memoryIndex) Get(ctx context.Context, path string) (index.FileMeta, error) {
	meta, ok := m.files[path]
	if !ok {
		return index.FileMeta{}, index.ErrNotFound
	}
	meta.AtimeLocal = time.Now().UTC()
	m.files[path] = cloneMeta(meta)
	return cloneMeta(meta), nil
}

func (m *memoryIndex) Update(ctx context.Context, path string, fn func(index.FileMeta) (index.FileMeta, error)) (index.FileMeta, error) {
	current, ok := m.files[path]
	if !ok {
		return index.FileMeta{}, index.ErrNotFound
	}
	updated, err := fn(cloneMeta(current))
	if err != nil {
		return index.FileMeta{}, err
	}
	if updated.Path != path {
		updated.Path = path
	}
	if updated.AtimeLocal.IsZero() {
		updated.AtimeLocal = time.Now().UTC()
	}
	m.files[path] = cloneMeta(updated)
	return cloneMeta(updated), nil
}

func (m *memoryIndex) Delete(ctx context.Context, path string) error {
	delete(m.files, path)
	return nil
}

func (m *memoryIndex) ListLRU(ctx context.Context, limit int) ([]index.FileMeta, error) {
	items := make([]index.FileMeta, 0, len(m.files))
	for _, meta := range m.files {
		items = append(items, cloneMeta(meta))
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].AtimeLocal.Before(items[j].AtimeLocal)
	})

	if limit > 0 && limit < len(items) {
		items = items[:limit]
	}

	return items, nil
}

func (m *memoryIndex) AddUpload(ctx context.Context, entry index.UploadRecord) (index.UploadRecord, error) {
	if entry.ID == "" {
		m.nextUpload++
		entry.ID = makeUploadID(m.nextUpload)
	}
	now := time.Now().UTC()
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = now
	}
	entry.UpdatedAt = now
	m.uploads[entry.ID] = cloneUpload(entry)
	m.uploadOrder = append(m.uploadOrder, entry.ID)
	return cloneUpload(entry), nil
}

func (m *memoryIndex) ListUploads(ctx context.Context) ([]index.UploadRecord, error) {
	items := make([]index.UploadRecord, 0, len(m.uploads))
	for _, id := range m.uploadOrder {
		if entry, ok := m.uploads[id]; ok {
			items = append(items, cloneUpload(entry))
		}
	}
	return items, nil
}

func (m *memoryIndex) UpdateUploadStatus(ctx context.Context, id string, status index.UploadStatus, lastError string) (index.UploadRecord, error) {
	entry, ok := m.uploads[id]
	if !ok {
		return index.UploadRecord{}, index.ErrNotFound
	}
	entry.Status = status
	entry.Attempts++
	entry.LastError = lastError
	now := time.Now().UTC()
	if !now.After(entry.CreatedAt) {
		now = entry.CreatedAt.Add(time.Nanosecond)
	}
	entry.UpdatedAt = now
	m.uploads[id] = cloneUpload(entry)
	return cloneUpload(entry), nil
}

func cloneMeta(meta index.FileMeta) index.FileMeta {
	clone := meta
	if len(meta.Chunks) > 0 {
		clone.Chunks = make([]index.ChunkMeta, len(meta.Chunks))
		copy(clone.Chunks, meta.Chunks)
	}
	return clone
}

func cloneUpload(entry index.UploadRecord) index.UploadRecord {
	return entry
}

func makeUploadID(seq int) string {
	return "mem-" + strconv.Itoa(seq)
}
