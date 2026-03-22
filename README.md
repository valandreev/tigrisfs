[![unittests](https://github.com/tigrisdata/tigrisfs/actions/workflows/test.yaml/badge.svg)]()
[![xfstests](https://github.com/tigrisdata/tigrisfs/actions/workflows/xfstests.yaml/badge.svg)]()
[![cluster-test](https://github.com/tigrisdata/tigrisfs/actions/workflows/cluster_test.yaml/badge.svg)]()


TigrisFS is a high-performance FUSE-based file system for S3-compatible object storage written in Go.

This fork is focused on turning TigrisFS into a production desktop client experience for S3 object storage:
- fast streaming access with local cache and selective pinning
- profile-based mount management
- native Finder/Explorer integration
- operational visibility (cache/disk usage, transfer rates, health, logs)

# Overview

TigrisFS allows you to mount an S3 or compatible object store bucket as a local file system.

TigrisFS is based on [GeeseFS](https://github.com/yandex-cloud/geesefs), which is a fork of Goofys.
GeeseFS focused on solving performance problems which FUSE file systems based on S3 typically have,
especially with small files and metadata operations.
It solves these problems by using aggressive parallelism and asynchrony.

The goal of TigrisFS is to further improve on performance and reliability especially in distributed cluster setup.

In the first release we focused on improving security and reliability of the code base:
 * Improved security by:
   * removing bundled, outdated AWS SDK with critical vulnerabilities.
   * upgrading dependencies to fix security vulnerabilities.
 * Improved reliability by:
   * fixing all race conditions found by race detector, which is now enabled by default in tests.
   * fixing all linter issues and enabling linting by default in CI.
   * running more extensive tests and enabling them by default in CI.

## Tigris specific features

When mounted with the [Tigris](https://www.tigrisdata.com) backend TigrisFS supports:
  * POSIX permissions, special files, symbolic links.
  * Auto-preload content of small files on directory list in single request.
  * Allows to auto prefetch directory data to the region on list.

## Desktop GUI

The repository also includes a desktop app (`tigrisfs-gui`) for profile-based connection management, bucket discovery, unified namespace mounts, cache controls, pin/unpin workflows, integration health, and advanced logs.

See the full usage guide: [docs/gui-app-usage.md](docs/gui-app-usage.md).

## What Changed In This Fork

Major areas added/improved:

- Security hardening:
  - credentials are stored in OS keychain/credential manager (not persisted in `config.json`)
  - TLS verification is enabled by default (`Skip SSL` defaults to off)
- Profile-driven GUI workflows:
  - create/save/delete connection profiles
  - probe S3 endpoint for accessible buckets
  - mount/unmount directly from GUI
- Multi-bucket unified namespace mounts:
  - one mountpoint with buckets as top-level folders
  - optional per-profile auto-mount behavior
- Cache and transfer observability:
  - mount root/cache root disk usage
  - per-mount and aggregate upload/download rates
  - pin/unpin controls for files and folders
- Native file-manager integration:
  - macOS Finder integration (actions + Finder Sync host/appex packaging)
  - Windows Explorer integration (context actions + overlay extension registration path)
  - GUI extension health checks and repair actions
- Advanced diagnostics:
  - live log stream with module/level/text filters
  - runtime log-level control and export

## What This Is Supposed To Be

The target is a solid, professional S3 filesystem client:
- reliable under long-running mount workloads
- cache-aware and predictable under disk pressure
- secure by default
- operator-friendly with built-in health and telemetry
- easy to use for both single-bucket and unified multi-bucket workflows

# Run This Branch

## 1) Clone and checkout this fork branch

```bash
git clone https://github.com/dr1pvfx/tigrisfs.git
cd tigrisfs
git checkout codex/finn-gui-usage-docs
```

## 2) Prerequisites

- Go toolchain installed (same version required by `go.mod`).
- macOS:
  - [macFUSE](https://osxfuse.github.io)
  - optional for Finder Sync packaging: `xcodebuild`, `xcodegen`
- Windows:
  - [WinFsp](https://winfsp.dev)
  - optional for Explorer extension build/register: .NET SDK + Administrator PowerShell

## 3) Build binaries from source

```bash
go build -o tigrisfs .
go build -o tigrisfs-gui ./gui
```

## 4) Run the desktop app

```bash
./tigrisfs-gui
```

In the app:
- create/save a connection profile
- probe S3 buckets
- mount buckets individually or as one unified namespace
- monitor cache/disk usage and transfer rates
- use advanced logs and extension health in `Settings`/`Logs`

Full GUI walkthrough: [docs/gui-app-usage.md](docs/gui-app-usage.md).

## 5) Run the CLI directly

```bash
AWS_ACCESS_KEY_ID="<access-key>" \
AWS_SECRET_ACCESS_KEY="<secret-key>" \
./tigrisfs <bucket> <mountpoint> --endpoint https://s3.example.com
```

Example:

```bash
mkdir -p ~/TigrisFS/my-bucket
AWS_ACCESS_KEY_ID="..." AWS_SECRET_ACCESS_KEY="..." \
./tigrisfs my-bucket ~/TigrisFS/my-bucket --endpoint https://s3.example.com
```

## 6) Install Finder / Explorer integration

From the GUI:
- open `Settings`
- click `Install Finder Integration` (macOS) or `Install Windows Explorer Integration` (Windows)

Or via CLI switch:

```bash
./tigrisfs-gui --install-file-manager-integration
```

Remove integration:

```bash
./tigrisfs-gui --uninstall-file-manager-integration
```

## 7) Native extension packaging (maintainers)

macOS Finder host + appex:

```bash
./scripts/package_finder_sync.sh ./tigrisfs-gui
```

macOS Developer ID signing + notarization (for distribution):

```bash
CODESIGN_IDENTITY="Developer ID Application: <Name> (<TEAM_ID>)" \
NOTARY_PROFILE="tigrisfs-notary" \
./scripts/notarize_macos_release.sh dist/release-<timestamp>-darwin-arm64
```

Detailed guide: [docs/macos-release-signing.md](docs/macos-release-signing.md).

Windows Explorer extension build/register/restart (run as Administrator on Windows):

```powershell
cd gui\extensions\windows\TigrisFS.ExplorerExtension\tools
.\build-register-restart.ps1 -Configuration Release
```
 
# License

Licensed under the Apache License, Version 2.0

See `LICENSE` and `AUTHORS`
