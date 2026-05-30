#!/usr/bin/env bash
# Build Clipfan.app from the SwiftPM executable. Produces a double-clickable,
# LSUIElement (menubar-only) app bundle under .build/Clipfan.app.
#
# Codesigning / notarization is a 1.0 distribution concern (roadmap Phase 8);
# this just makes a runnable local bundle.
set -euo pipefail
cd "$(dirname "$0")"

CONFIG=${CONFIG:-release}
swift build -c "$CONFIG"

BIN=".build/$CONFIG/Clipfan"
APP=".build/Clipfan.app"

# The app self-installs the daemon on first run by shelling out to install.sh,
# so the full dist payload (all-arch binaries + helpers + install.sh) must ship
# inside the bundle. This is also the payload "Add Peer…" stages for remotes.
DIST="../../../dist"
REQUIRED=(
    install.sh
    com.primeradiant.clipfan.plist
    clipfan-darwin-amd64 clipfan-darwin-arm64
    clipfan-linux-amd64 clipfan-linux-arm64
    clipfan-pasteboard-helper-darwin-amd64 clipfan-pasteboard-helper-darwin-arm64
    clipfan-shim-linux-amd64 clipfan-shim-linux-arm64
    clipfan.service tmux.conf.snippet
)
missing=()
for f in "${REQUIRED[@]}"; do
    [[ -e "$DIST/$f" ]] || missing+=("$f")
done
if (( ${#missing[@]} )); then
    echo "ERROR: dist payload incomplete — run dist/build-all.sh first." >&2
    printf '  missing: %s\n' "${missing[@]}" >&2
    exit 1
fi

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources/dist"
cp Info.plist "$APP/Contents/Info.plist"
cp "$BIN" "$APP/Contents/MacOS/Clipfan"
cp Resources/AppIcon.icns "$APP/Contents/Resources/AppIcon.icns"
for f in "${REQUIRED[@]}"; do
    cp "$DIST/$f" "$APP/Contents/Resources/dist/$f"
done

# Ad-hoc sign so Gatekeeper lets it run locally without quarantine prompts.
codesign --force --deep --sign - "$APP" 2>/dev/null || \
  echo "(codesign skipped — app still runs locally)"

echo "Built $APP"
echo "Run with: open $APP   (or copy to /Applications)"
