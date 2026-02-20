#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/notarize_macos_release.sh <release_dir>

Required environment:
  CODESIGN_IDENTITY   Code signing identity, usually:
                      "Developer ID Application: <Name> (<TEAM_ID>)"

For notarization (default enabled):
  NOTARY_PROFILE      Keychain profile created with:
                      xcrun notarytool store-credentials <profile> ...

Optional environment:
  SKIP_NOTARIZE       Set to 1 to skip notarization/stapling
  UPLOAD_RELEASE      Set to 1 to upload artifacts to GitHub release
  GH_RELEASE_REPO     e.g. "dr1pvfx/tigrisfs" (required when UPLOAD_RELEASE=1)
  GH_RELEASE_TAG      e.g. "codex-preview-20260220-200215" (required when UPLOAD_RELEASE=1)

Expected files in <release_dir>:
  tigrisfs-darwin-arm64
  tigrisfs-gui-darwin-arm64
  TigrisFS Finder Host.app
EOF
}

require_tool() {
  local tool="$1"
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "Missing required tool: $tool" >&2
    exit 1
  fi
}

ensure_file() {
  local path="$1"
  if [[ ! -f "$path" ]]; then
    echo "Expected file not found: $path" >&2
    exit 1
  fi
}

ensure_dir() {
  local path="$1"
  if [[ ! -d "$path" ]]; then
    echo "Expected directory not found: $path" >&2
    exit 1
  fi
}

extract_entitlements() {
  local target="$1"
  local out="$2"

  # codesign prints diagnostics to stderr; capture only plist payload from stdout.
  if ! codesign -d --entitlements :- "$target" >"$out" 2>/dev/null; then
    true
  fi

  if ! grep -q "<plist" "$out" 2>/dev/null; then
    cat >"$out" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict/></plist>
EOF
  fi
}

sign_binary() {
  local path="$1"
  echo "Signing binary: $path"
  codesign --force --sign "$CODESIGN_IDENTITY" --options runtime --timestamp "$path"
}

sign_bundle_with_entitlements() {
  local path="$1"
  local entitlements="$2"
  echo "Signing bundle: $path"
  codesign \
    --force \
    --sign "$CODESIGN_IDENTITY" \
    --options runtime \
    --timestamp \
    --entitlements "$entitlements" \
    "$path"
}

verify_signed_target() {
  local path="$1"
  echo "Verifying signature: $path"
  codesign --verify --deep --strict "$path"
}

submit_for_notarization() {
  local artifact="$1"
  local out_json="$2"

  echo "Submitting for notarization: $artifact"
  xcrun notarytool submit "$artifact" \
    --keychain-profile "$NOTARY_PROFILE" \
    --wait \
    --output-format json >"$out_json"

  if ! grep -q '"status"[[:space:]]*:[[:space:]]*"Accepted"' "$out_json"; then
    echo "Notarization failed for $artifact" >&2
    cat "$out_json" >&2
    exit 1
  fi
}

if [[ $# -ne 1 ]]; then
  usage
  exit 1
fi

RELEASE_DIR="$(cd "$1" && pwd)"
CODESIGN_IDENTITY="${CODESIGN_IDENTITY:-}"
SKIP_NOTARIZE="${SKIP_NOTARIZE:-0}"
UPLOAD_RELEASE="${UPLOAD_RELEASE:-0}"
GH_RELEASE_REPO="${GH_RELEASE_REPO:-}"
GH_RELEASE_TAG="${GH_RELEASE_TAG:-}"
NOTARY_PROFILE="${NOTARY_PROFILE:-}"

if [[ -z "$CODESIGN_IDENTITY" ]]; then
  echo "CODESIGN_IDENTITY is required." >&2
  exit 1
fi

require_tool codesign
require_tool ditto
require_tool shasum
require_tool xcrun

if [[ "$UPLOAD_RELEASE" == "1" ]]; then
  require_tool gh
  if [[ -z "$GH_RELEASE_REPO" || -z "$GH_RELEASE_TAG" ]]; then
    echo "GH_RELEASE_REPO and GH_RELEASE_TAG are required when UPLOAD_RELEASE=1." >&2
    exit 1
  fi
fi

if [[ "$SKIP_NOTARIZE" != "1" ]]; then
  if [[ -z "$NOTARY_PROFILE" ]]; then
    echo "NOTARY_PROFILE is required unless SKIP_NOTARIZE=1." >&2
    exit 1
  fi
fi

CLI_BIN="$RELEASE_DIR/tigrisfs-darwin-arm64"
GUI_BIN="$RELEASE_DIR/tigrisfs-gui-darwin-arm64"
HOST_APP="$RELEASE_DIR/TigrisFS Finder Host.app"
HOST_APPEX="$HOST_APP/Contents/PlugIns/TigrisFSFinderSync.appex"
HOST_ZIP="$RELEASE_DIR/TigrisFS-Finder-Host.app.zip"
CLI_ZIP="$RELEASE_DIR/tigrisfs-darwin-arm64.zip"
GUI_ZIP="$RELEASE_DIR/tigrisfs-gui-darwin-arm64.zip"
CHECKSUMS="$RELEASE_DIR/SHA256SUMS.txt"

ensure_file "$CLI_BIN"
ensure_file "$GUI_BIN"
ensure_dir "$HOST_APP"
ensure_dir "$HOST_APPEX"

TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

APPEX_ENT="$TMP_DIR/appex.entitlements.plist"
APP_ENT="$TMP_DIR/app.entitlements.plist"

extract_entitlements "$HOST_APPEX" "$APPEX_ENT"
extract_entitlements "$HOST_APP" "$APP_ENT"

sign_binary "$CLI_BIN"
sign_binary "$GUI_BIN"
sign_bundle_with_entitlements "$HOST_APPEX" "$APPEX_ENT"
sign_bundle_with_entitlements "$HOST_APP" "$APP_ENT"

verify_signed_target "$CLI_BIN"
verify_signed_target "$GUI_BIN"
verify_signed_target "$HOST_APP"

echo "Creating notarization archives..."
rm -f "$HOST_ZIP" "$CLI_ZIP" "$GUI_ZIP"
ditto -c -k --sequesterRsrc --keepParent "$HOST_APP" "$HOST_ZIP"
ditto -c -k --keepParent "$CLI_BIN" "$CLI_ZIP"
ditto -c -k --keepParent "$GUI_BIN" "$GUI_ZIP"

if [[ "$SKIP_NOTARIZE" != "1" ]]; then
  submit_for_notarization "$HOST_ZIP" "$TMP_DIR/host.notary.json"
  submit_for_notarization "$CLI_ZIP" "$TMP_DIR/cli.notary.json"
  submit_for_notarization "$GUI_ZIP" "$TMP_DIR/gui.notary.json"

  echo "Stapling app notarization ticket..."
  xcrun stapler staple "$HOST_APP"
  xcrun stapler validate "$HOST_APP"

  # Re-package after stapling so the stapled ticket is inside the distributed zip.
  rm -f "$HOST_ZIP"
  ditto -c -k --sequesterRsrc --keepParent "$HOST_APP" "$HOST_ZIP"
fi

echo "Regenerating checksums..."
(
  cd "$RELEASE_DIR"
  shasum -a 256 \
    tigrisfs-darwin-arm64 \
    tigrisfs-gui-darwin-arm64 \
    TigrisFS-Finder-Host.app.zip \
    tigrisfs-darwin-arm64.zip \
    tigrisfs-gui-darwin-arm64.zip > SHA256SUMS.txt
)

if [[ "$UPLOAD_RELEASE" == "1" ]]; then
  echo "Uploading assets to GitHub release $GH_RELEASE_REPO@$GH_RELEASE_TAG..."
  gh release upload "$GH_RELEASE_TAG" \
    "$CLI_BIN" \
    "$GUI_BIN" \
    "$HOST_ZIP" \
    "$CLI_ZIP" \
    "$GUI_ZIP" \
    "$CHECKSUMS" \
    --repo "$GH_RELEASE_REPO" \
    --clobber
fi

echo "Done."
echo "Release dir: $RELEASE_DIR"
echo "Signed identity: $CODESIGN_IDENTITY"
if [[ "$SKIP_NOTARIZE" == "1" ]]; then
  echo "Notarization: skipped"
else
  echo "Notarization: complete (profile: $NOTARY_PROFILE)"
fi
