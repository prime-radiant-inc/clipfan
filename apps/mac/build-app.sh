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

echo "[1/6] swift build (release)"
( cd "$swiftpkg" && swift build -c release )
swiftbin="$swiftpkg/.build/release/Clipfan"

# Stamp the daemon version from DAEMON_VERSION (same scheme as
# dist/build-all.sh) so the bundled binary reports a real version, not "dev".
version=""
if [[ -f "$repo/DAEMON_VERSION" ]]; then
    version=$(<"$repo/DAEMON_VERSION"); version=${version//$'\n'/}; version=${version//$'\r'/}
fi
daemon_ldflags="-s -w"
[[ -n "$version" ]] && daemon_ldflags="$daemon_ldflags -X github.com/prime-radiant-inc/clipfan/internal/version.Version=$version"

echo "[2/6] go build daemon + helper"
( cd "$repo" && GOOS=darwin GOARCH=$garch go build -ldflags "$daemon_ldflags" -o "$out/clipfand" ./cmd/clipfan )
swiftc -O -target "$(uname -m)-apple-macos13.0" \
    -o "$out/clipfan-pasteboard-helper" "$repo/dist/clipfan-pasteboard-helper.swift"

echo "[3/6] assemble bundle"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources" "$app/Contents/Frameworks"
cp "$swiftpkg/Info.plist" "$app/Contents/Info.plist"
cp "$swiftbin" "$app/Contents/MacOS/Clipfan"
cp "$out/clipfand" "$app/Contents/MacOS/clipfand"
cp "$out/clipfan-pasteboard-helper" "$app/Contents/MacOS/clipfan-pasteboard-helper"
chmod +x "$app/Contents/MacOS/"*

# Embed Sparkle.framework (binary dependency) and KeyboardShortcuts resource bundle.
# SwiftPM uses .build/release for the convenience path and may also emit an
# architecture-qualified .build/<triple>/release path.
find_sparkle_framework() {
    local candidate
    for candidate in \
        "$swiftpkg/.build/release/Sparkle.framework" \
        "$swiftpkg"/.build/*/release/Sparkle.framework; do
        if [[ -d "$candidate" ]]; then
            printf '%s\n' "$candidate"
            return 0
        fi
    done

    echo "error: Sparkle.framework not found in either SwiftPM release layout:" >&2
    echo "       $swiftpkg/.build/release/Sparkle.framework" >&2
    echo "       $swiftpkg/.build/<triple>/release/Sparkle.framework" >&2
    return 1
}

sparkle_src=$(find_sparkle_framework) || exit 1
kb_bundle_src="$swiftpkg/.build/release/KeyboardShortcuts_KeyboardShortcuts.bundle"
cp -R "$sparkle_src" "$app/Contents/Frameworks/Sparkle.framework"
rm -rf "$app/Contents/Frameworks/Sparkle.framework/Versions/B/_CodeSignature" \
       "$app/Contents/Frameworks/Sparkle.framework/Versions/B/Headers" \
       "$app/Contents/Frameworks/Sparkle.framework/Versions/B/PrivateHeaders" \
       "$app/Contents/Frameworks/Sparkle.framework/Versions/B/Modules"
if [[ -d "$kb_bundle_src" ]]; then
    cp -R "$kb_bundle_src" "$app/Contents/Resources/KeyboardShortcuts_KeyboardShortcuts.bundle"
fi

# Main binary rpath covers @loader_path (Contents/MacOS); add ../Frameworks so
# dyld finds Sparkle.framework in the conventional macOS app layout.
install_name_tool -add_rpath '@loader_path/../Frameworks' \
    "$app/Contents/MacOS/Clipfan" 2>/dev/null || true

# Bundle the cross-arch dist/ payload (every daemon binary + install.sh + the
# launchd/systemd units + tmux snippet) as Resources/dist. The app's "Add Peer"
# flow uploads the target-arch binary from here, and resolves its provisioning
# binary (Resources/dist/clipfan-darwin-<arch>) from here too — without this
# payload, Add Peer fails with current_local_provisioning_binary_required.
# dist/build-all.sh must have staged dist/; verify the key payloads exist.
echo "[4/6] bundle dist/ payload (required for Add Peer)"
payload_src="$repo/dist"
required_payloads=(
    "$payload_src/clipfan-darwin-amd64" "$payload_src/clipfan-darwin-arm64"
    "$payload_src/clipfan-linux-amd64" "$payload_src/clipfan-linux-arm64"
    "$payload_src/install.sh" "$payload_src/com.primeradiant.clipfan.plist"
    "$payload_src/clipfan.service" "$payload_src/tmux.conf.snippet"
)
for f in "${required_payloads[@]}"; do
    if [[ ! -r "$f" ]]; then
        echo "error: missing payload $f" >&2
        echo "       run 'bash dist/build-all.sh' first — the app embeds dist/" >&2
        echo "       as Resources/dist so it can install peers of every arch." >&2
        exit 1
    fi
done
mkdir -p "$app/Contents/Resources/dist"
cp -R "$payload_src/." "$app/Contents/Resources/dist/"

echo "[5/6] ad-hoc codesign (replace with Developer ID for distribution)"
codesign --force --deep --sign - "$app" 2>/dev/null || \
    echo "  (codesign failed — app will still run locally after a Gatekeeper prompt)"

echo "[6/6] done"
echo "Built: $app"
echo "Install: cp -R '$app' /Applications/ && open '$app'"
