# TigrisFS Explorer Extension

Native Windows Explorer integration with **overlay badges** and **context menu actions** using SharpShell.

## Features

- Overlay badges:
  - Pinned
  - Syncing
  - Cached/Partial
- Context menu actions:
  - Pin in TigrisFS Cache
  - Unpin from TigrisFS Cache
  - Unmount TigrisFS Mount
  - Open TigrisFS Dashboard

## Integration contract

This extension reads daemon state from:

- `%APPDATA%\\TigrisFS\\integration_server.json`

and calls local authenticated endpoints:

- `GET /v1/path/status` (via query string)
- `POST /v1/command`

## Build

```powershell
cd gui/extensions/windows/TigrisFS.ExplorerExtension
dotnet build -c Release
```

## Register

Run as Administrator (recommended):

```powershell
powershell -ExecutionPolicy Bypass -File .\tools\register-extension.ps1
```

One-shot build + register + Explorer restart:

```powershell
powershell -ExecutionPolicy Bypass -File .\tools\build-register-restart.ps1
```

Unregister:

```powershell
powershell -ExecutionPolicy Bypass -File .\tools\register-extension.ps1 -Unregister
```

After registration changes, restart Explorer or sign out/in.

## Notes

- Windows has a global icon-overlay slot limit. Keep competing overlay handlers minimal.
- This project targets `x64` and `net48` for broad Explorer compatibility.
