package index

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when a requested entry is not present in the index.
var ErrNotFound = errors.New("cache index: entry not found")

// ChunkMeta describes a contiguous byte range cached on disk.
type ChunkMeta struct {
	Offset int64
	Length int64
	Dirty  bool
}

// FileMeta stores metadata for a cached object, including chunk layout and timestamps.
type FileMeta struct {
	Path        string
	ETag        string
	Size        int64
	Chunks      []ChunkMeta
	MtimeRemote time.Time
	AtimeLocal  time.Time
}

// UploadStatus represents the lifecycle state for a pending background upload.
type UploadStatus string

const (
	// UploadStatusQueued indicates the entry is waiting for uploader pickup.
	UploadStatusQueued UploadStatus = "queued"
	// UploadStatusInProgress indicates an upload is currently executing.
	UploadStatusInProgress UploadStatus = "in_progress"
	// UploadStatusComplete marks a successful upload that may be pruned.
	UploadStatusComplete UploadStatus = "complete"
	// UploadStatusFailed marks an upload that exhausted retries and requires intervention.
	UploadStatusFailed UploadStatus = "failed"
)

// UploadRecord tracks a pending or completed chunk upload stored in the index.
type UploadRecord struct {
	ID        string
	Path      string
	Offset    int64
	Length    int64
	Status    UploadStatus
	Attempts  int
	LastError string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CacheIndex expresses the minimal persistence requirements for the cache metadata store.
type CacheIndex interface {
	// Put inserts or replaces metadata for the provided path.
	Put(ctx context.Context, meta FileMeta) error
	// Get retrieves metadata for the provided path.
	Get(ctx context.Context, path string) (FileMeta, error)
	// Update atomically mutates metadata for the provided path.
	Update(ctx context.Context, path string, fn func(FileMeta) (FileMeta, error)) (FileMeta, error)
	// Delete removes metadata for the provided path. Missing entries are ignored.
	Delete(ctx context.Context, path string) error
	// ListLRU returns metadata ordered by least-recently-used (AtimeLocal ascending).
	ListLRU(ctx context.Context, limit int) ([]FileMeta, error)

	// AddUpload records a new upload entry. If entry.ID is empty, an ID must be assigned.
	AddUpload(ctx context.Context, entry UploadRecord) (UploadRecord, error)
	// ListUploads returns all upload entries in deterministic order (oldest first).
	ListUploads(ctx context.Context) ([]UploadRecord, error)
	// UpdateUploadStatus updates status information for an existing upload entry.
	UpdateUploadStatus(ctx context.Context, id string, status UploadStatus, lastError string) (UploadRecord, error)
}
