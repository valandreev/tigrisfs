# Native File Manager Extensions

This folder contains native shell-extension projects that consume the local
TigrisFS GUI integration API (`integration_server.json` + localhost HTTP API).

- macOS Finder Sync: `gui/extensions/macos/TigrisFSFinderSync`
- Windows Explorer shell extension: `gui/extensions/windows/TigrisFS.ExplorerExtension`

Packaging helpers:

- macOS package/sign/copy host app: `scripts/package_finder_sync.sh`
- Windows build/register/restart (admin): `gui/extensions/windows/TigrisFS.ExplorerExtension/tools/build-register-restart.ps1`

The GUI daemon now exposes extension-facing endpoints:

- `GET /v1/mounts`
- `GET|POST /v1/path/status`
- `POST /v1/path/statuses`
- `POST /v1/command`

All endpoints require `X-TigrisFS-Token` from the state file.
