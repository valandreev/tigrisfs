# TigrisFS

## Project Overview

Tigrisfs is a high-performance FUSE-based file system for S3-compatible object storage written in Go. It is a fork of [GeeseFS](https://github.com/yandex-cloud/geesefs), which is a fork of [Goofys](https://github.com/kahing/goofys). Tigrisfs allows you to mount an S3 or compatible object store bucket as a local file system.

The project is structured as a Go application with the main entry point in `main.go`. The core file system logic is implemented in `core/goofys.go`, and it supports multiple backends, including S3, Azure Blob Storage, and Google Cloud Storage. The configuration is handled through command-line flags, defined in `core/cfg/config.go` and `core/cfg/flags.go`.

## Building and Running

The project uses a `Makefile` to manage the build, test, and linting processes.

### Building

To build the project, run the following command:

```bash
make build
```

This will create a `tigrisfs` binary in the root directory. To build with race detection enabled, use:

```bash
make build-debug
```

### Running Tests

The project has a suite of tests that can be run using the following commands:

*   **Unit Tests:**
    ```bash
    make run-test
    ```
*   **xfstests:**
    ```bash
t
    make run-xfstests
    ```
*   **Cluster Tests:**
    ```bash
    make run-cluster-test
    ```

### Linting

To run the linter, use the following command:

```bash
make run-lint
```

This will run `shellcheck` and `golangci-lint`.

## Development Conventions

*   **Dependencies:** The project's dependencies are managed using Go modules. The `go.mod` file lists the project's dependencies.
*   **Protobuf:** The project uses Protocol Buffers for gRPC communication. The `.proto` files are located in `core/pb`. To generate the Go code from the Protobuf definitions, run:
    ```bash
    make protoc
    ```
*   **Logging:** The project uses the `zerolog` library for logging.
*   **Configuration:** The application is configured through command-line flags. The available flags are defined in `core/cfg/flags.go`.
