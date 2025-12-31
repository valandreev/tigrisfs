# TigrisFS

## Project Overview
TigrisFS is a high-performance, FUSE-based file system written in Go that allows mounting S3-compatible object storage buckets as local file systems. It is based on GeeseFS (a fork of Goofys) and is optimized for performance (aggressive parallelism, asynchrony) and reliability in distributed cluster setups.

**Key Features:**
*   **S3 Compatible:** Supports AWS S3, Azure Blob Storage, Google Cloud Storage, and Tigris.
*   **Performance:** Uses asynchronous I/O and parallelism to improve throughput, especially for small files and metadata operations.
*   **Tigris Backend Enhancements:** When used with Tigris, supports POSIX permissions, special files, symbolic links, and intelligent preloading/prefetching.
*   **Reliability:** Focused on thread safety (race detector enabled in tests) and code quality (strict linting).

## Architecture & Structure
*   **`main.go`**: Entry point for the application.
*   **`core/`**: Contains the core logic of the file system.
    *   **`core/backend_*.go`**: Implementations for different storage backends (S3, Azure, GCS, ADL).
    *   **`core/cluster_*.go`**: Logic for distributed cluster operations and gRPC communication.
    *   **`core/goofys.go` / `core/geesefs.go`**: Core FUSE filesystem logic inherited from predecessors.
    *   **`core/buffer_*.go`**: Memory management, buffer pools, and caching mechanisms.
    *   **`core/pb/`**: Protocol Buffer definitions and generated Go code for gRPC.
*   **`bench/`**: Benchmarking tools and scripts (Goofys, GeeseFS, s3fs comparisons).
*   **`test/`**: Integration tests, FUSE tests, and scripts for running `xfstests`.
*   **`pkg/`**: Packaging resources, including systemd service files.

## Development Workflow

### Prerequisites
*   Go (version specified in `go.mod`)
*   `make`
*   `protoc` (for regenerating gRPC code)
*   `s3proxy` (automatically handled by test scripts)

### Setup
Initialize the local development environment (installs dependencies and git hooks):
```bash
make setup
```

### Build
*   **Standard Build:**
    ```bash
    make build
    ```
*   **Debug Build (Race Detector Enabled):**
    ```bash
    make build-debug
    ```
*   **Install to `$GOPATH/bin`:**
    ```bash
    make install
    ```

### Testing
*   **Unit & Integration Tests:**
    ```bash
    make run-test
    ```
*   **Cluster Tests:**
    ```bash
    make run-cluster-test
    ```
*   **XFSTests (File System Verification):**
    ```bash
    make run-xfstests
    ```
*   **Linting:**
    ```bash
    make run-lint
    ```

### Code Generation
Regenerate Protocol Buffers code:
```bash
make protoc
```

## Conventions
*   **Commit Messages:** Follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) (e.g., `feat(fs): ...`, `fix(cluster): ...`).
*   **Linting:** `golangci-lint` is strictly enforced.
*   **Race Detection:** Tests run with the Go race detector enabled by default to catch concurrency issues.
