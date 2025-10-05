# TigrisFS Development Guide for AI Agents

TigrisFS is a high-performance FUSE filesystem for S3-compatible storage built on GeeseFS. It extends traditional S3 filesystems with cluster mode, enhanced Tigris backend features, and aggressive parallelism for performance.

## Architecture Overview

### Core Components
- **`core/goofys.go`**: Main filesystem implementation with buffer pools, inode management, and close-to-open consistency
- **`core/backend*.go`**: Storage backend abstraction supporting S3, Azure Blob/ADL, Google Cloud Storage
- **`core/cluster_*.go`**: Distributed cluster mode with gRPC communication and inode ownership management
- **`core/buffer_pool.go`**: Memory-managed buffer system for async I/O operations
- **`core/dir.go`**: Directory operations with "slurping" (bulk metadata prefetching)

### Multi-Backend Support
Backend detection is automatic via URL schemes:
- `s3://` or bare bucket → S3Backend 
- `wasb://` → Azure Blob Storage
- `adl://` → Azure Data Lake v1/v2
- `gs://` → Google Cloud Storage (with S3 compatibility layer)

Configuration lives in `core/cfg/conf_*.go` files with environment variable and credential file support.

### Cluster Mode (Linux only)
Enabled via `--cluster` flag with distributed inode ownership:
- Each node owns subset of inodes, requests route via gRPC (`core/pb/*.proto`)
- Ownership stealing protocol for load balancing
- Recovery service for graceful shutdowns
- Connection pooling in `cluster_conn_pool.go`

## Key Patterns

### Inode Management
```go
// Always check for ownership in cluster mode
inode.KeepOwnerLock()
defer inode.KeepOwnerUnlock()
if inode.owner != fs.Conns.id {
    // Route to correct owner via gRPC
}
```

### Backend Initialization
Use `StorageBackendInitWrapper` which lazy-initializes connections and handles errors gracefully. Backend selection happens in `NewBackend()` based on config type.

### Buffer Management  
The `BufferPool` tracks memory usage and triggers GC when approaching limits. Always use `fs.bufferPool.RequestBuffer()` and `buf.Free()`.

### Async Operations
Most I/O is asynchronous with parallel prefetching. Directory listings use "slurping" to batch metadata operations.

## Development Workflows

### Testing
- **Unit tests**: `make run-test` (uses s3proxy for S3 compatibility)
- **FUSE tests**: Shell scripts in `test/fuse-test.sh` with mount/unmount lifecycle
- **Cluster tests**: `make run-cluster-test` - multi-node scenarios
- **XFS tests**: `make run-xfstests` - POSIX compliance subset
- Test environment uses `SAME_PROCESS_MOUNT=1` for debugging

### Build & Debug
- **Build**: `go build -o tigrisfs` (CGO_ENABLED=0 for static builds)
- **Debug flags**: `--debug-fuse`, `--debug-s3`, `--debug-main` for component logging
- **Logging**: Uses zerolog with structured output, configure via `log.GetLogger(component)`
- **Platform**: Use build tags `//go:build !windows` for Unix-specific code

### Tigris-Specific Features
When `TigrisDetected()` returns true:
- POSIX permissions and special files supported
- Symlinks work (`CreateSymlink`, `ReadSymlink` operations)  
- Auto-preload small file content during directory listings
- Extended attributes for metadata storage

## Configuration Patterns

### Flag Structure
Flags defined in `core/cfg/flags.go` with categories (tuning, aws, misc). Backend-specific config in `core/cfg/conf_*.go`.

### Credential Loading
Each backend has auto-detection:
- S3: AWS credential chain (env vars, files, IAM roles)
- Azure: Multiple auth methods (env vars, CLI, managed identity)
- GCS: Service account JSON or application default credentials

### Performance Tuning
Key flags: `--stat-cache-ttl`, `--type-cache-ttl`, buffer pool sizes, parallel download settings.

## Common Pitfalls

- **Race conditions**: Always use appropriate locks, especially in cluster mode
- **Windows compatibility**: Many features are Unix-only (use build tags)
- **Backend errors**: Wrap in `StorageBackendInitWrapper` for lazy initialization
- **Memory management**: Buffer pools prevent OOM, always call `buf.Free()`
- **FUSE compliance**: Limited POSIX support by design (e.g., no hard links)

## Testing Strategy

Use `test/` shell scripts for integration testing. The test framework automatically handles s3proxy setup, mount/unmount cycles, and cleanup. For cluster testing, use `NUM_ITER` and `SAME_PROCESS_MOUNT` environment variables.