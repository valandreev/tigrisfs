# macOS Signing and Notarization

This repository includes a release helper script for macOS artifacts:

- `/Users/finn/tigris/tigrisfs/scripts/notarize_macos_release.sh`

It performs:

1. Developer ID signing for:
   - `tigrisfs-darwin-arm64`
   - `tigrisfs-gui-darwin-arm64`
   - `TigrisFS Finder Host.app` and nested `.appex`
2. Notarization submission for the app and binary zip archives.
3. Stapling of the app notarization ticket.
4. SHA-256 checksum regeneration.
5. Optional upload to a GitHub Release tag.

## Prerequisites

- Apple Developer account with a valid `Developer ID Application` certificate installed in Keychain.
- Xcode command line tools.
- `notarytool` credentials stored in keychain profile:

```bash
xcrun notarytool store-credentials tigrisfs-notary \
  --apple-id "<apple-id>" \
  --team-id "<team-id>" \
  --password "<app-specific-password>"
```

Alternative API-key based profiles also work.

## Run

```bash
CODESIGN_IDENTITY="Developer ID Application: <Name> (<TEAM_ID>)" \
NOTARY_PROFILE="tigrisfs-notary" \
scripts/notarize_macos_release.sh dist/release-<timestamp>-darwin-arm64
```

## Upload to GitHub Release (optional)

```bash
CODESIGN_IDENTITY="Developer ID Application: <Name> (<TEAM_ID>)" \
NOTARY_PROFILE="tigrisfs-notary" \
UPLOAD_RELEASE=1 \
GH_RELEASE_REPO="dr1pvfx/tigrisfs" \
GH_RELEASE_TAG="codex-preview-<tag>" \
scripts/notarize_macos_release.sh dist/release-<timestamp>-darwin-arm64
```

## Sign-only mode (skip notarization)

```bash
CODESIGN_IDENTITY="Developer ID Application: <Name> (<TEAM_ID>)" \
SKIP_NOTARIZE=1 \
scripts/notarize_macos_release.sh dist/release-<timestamp>-darwin-arm64
```
