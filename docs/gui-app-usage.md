# TigrisFS Desktop App Guide

This guide covers day-to-day use of `tigrisfs-gui` on macOS and Windows, including profiles, mounting, cache controls, file-manager integration, and troubleshooting.

## 1) Prerequisites

- macOS:
  - Install [macFUSE](https://osxfuse.github.io).
- Windows:
  - Install [WinFsp](https://winfsp.dev).

If the driver is missing, the app shows a startup error and does not continue.

## 2) Start the GUI

- Run the binary:
  - `./tigrisfs-gui`
- The app opens with tabs:
  - `Connection`
  - `Buckets`
  - `Mounts`
  - `Settings`
  - `Logs`

## 3) Connection Profiles

Use the `Connection` tab to create and manage profiles.

- Fields:
  - `Endpoint` (required)
  - `Access Key`
  - `Secret Key`
  - `Skip SSL verification (insecure)`
- Buttons:
  - `New Profile`
  - `Delete Profile`
  - `Save Profile`
  - `Clear Stored Credentials`
  - `Probe S3 Buckets`

### Security defaults and credential storage

- TLS verification is ON by default (`Skip SSL verification` is unchecked).
- Credentials are stored in OS keychain/credential manager, not in `config.json`.
- `Clear Stored Credentials` removes credentials for the selected profile.

## 4) Discover Buckets

Use `Probe S3 Buckets` in `Connection` or `Probe/Refresh Buckets` in `Buckets`.

- The app lists buckets the current profile can access.
- You can filter the list with `Filter buckets...`.

## 5) Mounting Modes

The app supports two mount strategies per profile.

### Per-bucket mount mode

- Mounts each bucket to:
  - `<Mount Root>/<bucket>`
- Use per-row `Mount`/`Unmount` or `Mount Visible`.

### Unified namespace mount mode

- Enable `Unified namespace mount (one mountpoint, buckets as subfolders)` in `Buckets` (or `Prefer unified namespace mount` in `Settings`).
- Mount once to:
  - `<Mount Root>`
- Buckets appear as top-level folders:
  - `<Mount Root>/<bucket1>`
  - `<Mount Root>/<bucket2>`
- Use `Unmount Profile` or `Unmount Namespace` to remove it.

## 6) Auto-mount

In `Buckets`, each bucket has an `Auto-mount` checkbox.

- On app start, the active profile auto-mounts selected buckets.
- In unified mode, auto-mount uses one unified namespace mount.

## 7) Mount and Cache Telemetry

The `Mounts` tab provides live operational status.

- Mount root filesystem usage:
  - usage text and progress bar
- Cache filesystem usage:
  - usage text and progress bar
- Live transfer summary:
  - download/upload rate
- Per-mount telemetry:
  - endpoint, mountpoint
  - current and total transfer bytes
  - cache path and size
  - uptime and status

Controls:

- `Refresh`
- `Unmount` per mount
- `Unmount All`
- `Pin Path` / `Unpin Path` with optional `Recursive` for directories

## 8) Settings and Advanced Tuning

The `Settings` tab is profile-specific.

### Mount and cache

- `Mount Root`
- `Memory Limit (MB)`
- `Cache Directory` (empty disables disk cache)
- `Cache Size (GB)`
- `Enable writeback`
- `Prefer unified namespace mount`

### Advanced performance

- `Entry Limit`
- `Max Flushers`
- `Read Ahead (KB)`
- `Stat Cache TTL (seconds)`
- `HTTP Timeout (seconds)`
- `Retry Interval (seconds)`
- `Cost-optimized mode (Cheap)`
- `Disable directory preload on file open`
- `Assume directories exist (ExplicitDir)`
- `Ignore fsync calls`
- `Sync file on close`
- `Disable xattr support`

Use `Reset Advanced Defaults` to restore defaults, then `Save Settings`.

## 9) Finder / Explorer Integration

The `Settings` tab contains an integration card with install and health actions.

- `Install <Finder|Windows Explorer> Integration`
- `Remove <Finder|Windows Explorer> Integration`
- `Check Health`
- `Enable Extension`
- `Restart <Finder|Windows Explorer>`

### macOS behavior

- Installs Finder actions:
  - Pin
  - Unpin
  - Unmount
- Installs Finder Sync host app and extension (if bundled nearby).
- Finder overlays and context actions communicate with the local GUI integration API.

### Windows behavior

- Installs Explorer context actions:
  - Pin
  - Unpin
  - Unmount
- Registers overlay shell-extension DLL when available.
- Explorer restart is usually required after registration changes.

## 10) Advanced Logs

Use the `Logs` tab for runtime diagnostics.

- Filter by:
  - level (`trace`, `debug`, `info`, `warn`, `error`)
  - module
  - free-text search
- Runtime log-level control.
- `Pause stream`, `Clear`, and `Export` logs.
- Mount count/health is shown inline.

## 11) CLI Integration Commands

The GUI can be invoked by Finder/Explorer actions:

- `--integration-action show|pin|unpin|unmount`
- `--integration-path <absolute mounted path>`
- `--integration-recursive` (for folder pin/unpin)
- `--install-file-manager-integration`
- `--uninstall-file-manager-integration`

## 12) Config and State Locations

- macOS:
  - config: `~/Library/Application Support/TigrisFS/config.json`
- Windows:
  - config: `%APPDATA%\TigrisFS\config.json`

Credentials are persisted in keychain/credential-manager entries (service `com.tigrisdata.tigrisfs`).

## 13) Packaging Native Extensions (maintainers)

### macOS Finder host + appex

- Build/sign/package:
  - `./scripts/package_finder_sync.sh ./tigrisfs-gui`
- Output:
  - `TigrisFS Finder Host.app` next to `tigrisfs-gui`

### Windows Explorer extension

- Build/register/restart (run as Administrator on Windows):
  - `gui/extensions/windows/TigrisFS.ExplorerExtension/tools/build-register-restart.ps1 -Configuration Release`

## 14) Troubleshooting Quick Checks

- Cannot connect/probe:
  - verify endpoint, access key, secret key, TLS settings.
- Mount fails:
  - check macFUSE/WinFsp installation.
  - confirm mount root is writable.
- No Finder/Explorer overlays:
  - install integration from `Settings`.
  - run `Check Health`.
  - click `Enable Extension`, then `Restart Finder/Windows Explorer`.
- Credentials issue:
  - use `Clear Stored Credentials`, re-enter keys, `Save Profile`.
