#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROJECT_DIR="$ROOT_DIR/gui/extensions/macos/TigrisFSFinderSync"
DERIVED_DATA_PATH="$PROJECT_DIR/build"
SCHEME="TigrisFSFinderHost"
CONFIGURATION="Release"
GUI_BINARY="${1:-$ROOT_DIR/tigrisfs-gui}"
SIGN_IDENTITY="${SIGN_IDENTITY:--}"
BUILD_DIR="${BUILD_DIR:-$DERIVED_DATA_PATH/Build/Products/$CONFIGURATION}"
HOST_APP_NAME="TigrisFS Finder Host.app"
OUTPUT_DIR="$(cd "$(dirname "$GUI_BINARY")" && pwd)"

if [[ ! -x "$(command -v xcodegen)" ]]; then
  echo "xcodegen not found. Install it first: brew install xcodegen" >&2
  exit 1
fi

if [[ ! -x "$(command -v xcodebuild)" ]]; then
  echo "xcodebuild not found. Install Xcode command line tools." >&2
  exit 1
fi

if [[ ! -f "$PROJECT_DIR/project.yml" ]]; then
  echo "Finder Sync project definition not found: $PROJECT_DIR/project.yml" >&2
  exit 1
fi

echo "[1/5] Generating Xcode project"
(
  cd "$PROJECT_DIR"
  xcodegen generate
)

echo "[2/5] Building Finder host + appex (scheme=$SCHEME config=$CONFIGURATION)"
(
  cd "$PROJECT_DIR"
  xcodebuild \
    -project TigrisFSFinderSync.xcodeproj \
    -scheme "$SCHEME" \
    -configuration "$CONFIGURATION" \
    -derivedDataPath "$DERIVED_DATA_PATH" \
    CODE_SIGNING_ALLOWED=YES \
    CODE_SIGN_IDENTITY="$SIGN_IDENTITY" \
    DEVELOPMENT_TEAM=""
)

HOST_APP_SRC="$BUILD_DIR/$HOST_APP_NAME"
if [[ ! -d "$HOST_APP_SRC" ]]; then
  echo "Built host app not found: $HOST_APP_SRC" >&2
  exit 1
fi

echo "[3/5] Verifying signatures"
codesign --verify --deep --strict "$HOST_APP_SRC"

mkdir -p "$OUTPUT_DIR"
HOST_APP_DST="$OUTPUT_DIR/$HOST_APP_NAME"

echo "[4/5] Installing packaged host next to tigrisfs-gui"
rm -rf "$HOST_APP_DST"
cp -R "$HOST_APP_SRC" "$HOST_APP_DST"

echo "[5/5] Final validation"
codesign --verify --deep --strict "$HOST_APP_DST"

echo "Finder Sync host packaged at: $HOST_APP_DST"
echo "Now run: $GUI_BINARY --install-file-manager-integration"
