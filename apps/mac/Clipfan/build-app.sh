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

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp Info.plist "$APP/Contents/Info.plist"
cp "$BIN" "$APP/Contents/MacOS/Clipfan"

# Ad-hoc sign so Gatekeeper lets it run locally without quarantine prompts.
codesign --force --deep --sign - "$APP" 2>/dev/null || \
  echo "(codesign skipped — app still runs locally)"

echo "Built $APP"
echo "Run with: open $APP   (or copy to /Applications)"
