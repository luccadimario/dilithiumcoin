#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

APP_PATH="build/bin/dilithium-wallet.app"

echo "Building Dilithium Wallet..."

# Find wails binary
WAILS="$(which wails 2>/dev/null || echo "$(go env GOPATH)/bin/wails")"
if [ ! -x "$WAILS" ]; then
    echo "Error: wails CLI not found. Install with: go install github.com/wailsapp/wails/v2/cmd/wails@latest"
    exit 1
fi

# Clean previous build
rm -rf build/bin

# Build with Wails
"$WAILS" build

# macOS: ad-hoc codesign the .app bundle so Gatekeeper doesn't flag it as broken
if [ "$(uname)" = "Darwin" ] && [ -d "$APP_PATH" ]; then
    echo "Signing macOS app bundle..."
    codesign --force --deep --sign - "$APP_PATH"
    xattr -cr "$APP_PATH"
    echo "Done. App ready at: $APP_PATH"
else
    echo "Done. Binary at: build/bin/"
fi
