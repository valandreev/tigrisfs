# TigrisFS Finder Sync Extension

This project adds a **real Finder Sync extension** (badge overlays + contextual actions) for mounted TigrisFS paths.

## What it does

- Shows Finder badges based on live path state from TigrisFS GUI daemon:
  - `Pinned`
  - `Syncing`
  - `Partially Cached`
  - `Cached`
  - `Remote`
- Adds Finder contextual actions:
  - `Pin in TigrisFS Cache`
  - `Unpin from TigrisFS Cache`
  - `Unmount TigrisFS Mount`
  - `Open TigrisFS Dashboard`
- Automatically tracks active mount roots using `/v1/mounts`.

## Integration contract

The extension reads local daemon state from:

- `~/Library/Application Support/TigrisFS/integration_server.json`

and calls the local authenticated API exposed by `tigrisfs-gui`:

- `GET /v1/mounts`
- `POST /v1/path/status`
- `POST /v1/command`

## Build (XcodeGen + Xcode)

1. Install [XcodeGen](https://github.com/yonaskolb/XcodeGen)
2. Generate the project:

```bash
cd gui/extensions/macos/TigrisFSFinderSync
xcodegen generate
```

3. Open in Xcode and build `TigrisFSFinderHost`.
4. Run `TigrisFSFinderHost` once, then enable the extension in:
   - System Settings -> Privacy & Security -> Extensions -> Finder Extensions

## Optional packaging

If you package `TigrisFS Finder Host.app` next to `tigrisfs-gui`, the GUI integration installer can discover and deploy it automatically.

From repo root you can do this in one step:

```bash
./scripts/package_finder_sync.sh ./tigrisfs-gui
```
