#!/usr/bin/env bash
# Package the SwiftPM Clipfan executable into a proper Clipfan.app bundle,
# embedding the Go daemon + Swift pasteboard helper so the app is fully
# self-contained. Run from a Mac with Xcode CLT + a built Go dist/.
#
#   apps/mac/build-app.sh
#
# Output: apps/mac/build/Clipfan.app
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)
swiftpkg="$here/Clipfan"
out="$here/build"
app="$out/Clipfan.app"

arch=$(uname -m)
[[ "$arch" == "x86_64" ]] && arch=amd64
[[ "$arch" == "arm64" ]] && garch=arm64 || garch=$arch

echo "[1/5] swift build (release)"
( cd "$swiftpkg" && swift build -c release )
swiftbin="$swiftpkg/.build/release/Clipfan"

echo "[2/5] go build daemon + helper"
( cd "$repo" && GOOS=darwin GOARCH=$garch go build -ldflags '-s -w' -o "$out/clipfand" ./cmd/clipfan )
swiftc -O -target "$(uname -m)-apple-macos13.0" \
    -o "$out/clipfan-pasteboard-helper" "$repo/dist/clipfan-pasteboard-helper.swift"

echo "[3/5] assemble bundle"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp "$swiftpkg/Info.plist" "$app/Contents/Info.plist"
cp "$swiftbin" "$app/Contents/MacOS/Clipfan"
cp "$out/clipfand" "$app/Contents/MacOS/clipfand"
cp "$out/clipfan-pasteboard-helper" "$app/Contents/MacOS/clipfan-pasteboard-helper"
chmod +x "$app/Contents/MacOS/"*

echo "[4/5] ad-hoc codesign (replace with Developer ID for distribution)"
codesign --force --deep --sign - "$app" 2>/dev/null || \
    echo "  (codesign failed — app will still run locally after a Gatekeeper prompt)"

echo "[5/5] done"
echo "Built: $app"
echo "Install: cp -R '$app' /Applications/ && open '$app'"
