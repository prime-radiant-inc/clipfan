#!/usr/bin/env bash
# Cross-compile clipfan, clipfan-shim, and macOS pasteboard helpers for every
# supported target and stage everything (with install.sh + unit files) in dist/.
set -euo pipefail

cd "$(dirname "$0")/.."
mkdir -p dist
version=${CLIPFAN_DAEMON_VERSION:-}
if [[ -z "$version" ]]; then
    if [[ ! -f DAEMON_VERSION ]]; then
        echo "missing DAEMON_VERSION" >&2
        exit 1
    fi
    version=$(<DAEMON_VERSION)
fi
version=${version//$'\n'/}
version=${version//$'\r'/}
ldflags="-s -w -X github.com/prime-radiant-inc/clipfan/internal/version.Version=$version"

go_payloads=(
    dist/clipfan-darwin-amd64
    dist/clipfan-darwin-arm64
    dist/clipfan-linux-amd64
    dist/clipfan-linux-arm64
    dist/clipfan-windows-amd64.exe
    dist/clipfan-windows-arm64.exe
    dist/clipfan-shim-linux-amd64
    dist/clipfan-shim-linux-arm64
)

helper_payloads=(
    dist/clipfan-pasteboard-helper-darwin-amd64
    dist/clipfan-pasteboard-helper-darwin-arm64
)

remove_release_payloads() {
    rm -f "${go_payloads[@]}" "${helper_payloads[@]}"
}

build_go_payloads() {
    for goos in darwin linux windows; do
        for goarch in amd64 arm64; do
            out="dist/clipfan-$goos-$goarch"
            if [[ "$goos" == "windows" ]]; then out="$out.exe"; fi
            echo "[build] clipfan $goos/$goarch"
            GOOS=$goos GOARCH=$goarch go build -ldflags "$ldflags" \
                -o "$out" ./cmd/clipfan
            if [[ "$goos" == "linux" ]]; then
                echo "[build] clipfan-shim $goos/$goarch"
                GOOS=$goos GOARCH=$goarch go build -ldflags "$ldflags" \
                    -o "dist/clipfan-shim-$goos-$goarch" ./cmd/clipfan-shim
            fi
        done
    done
}

build_pasteboard_helpers() {
    command -v swiftc >/dev/null 2>&1 || {
        echo "swiftc is required to build darwin pasteboard helpers" >&2
        exit 1
    }

    echo "[build] clipfan-pasteboard-helper darwin/amd64"
    swiftc -O -target x86_64-apple-macos13.0 \
        -o dist/clipfan-pasteboard-helper-darwin-amd64 dist/clipfan-pasteboard-helper.swift
    echo "[build] clipfan-pasteboard-helper darwin/arm64"
    swiftc -O -target arm64-apple-macos13.0 \
        -o dist/clipfan-pasteboard-helper-darwin-arm64 dist/clipfan-pasteboard-helper.swift
}

verify_payload() {
    local f="$1"
    [[ -x "$f" ]] || {
        echo "missing executable payload: $f" >&2
        exit 1
    }
}

verify_payloads() {
    for f in "${go_payloads[@]}" "${helper_payloads[@]}"; do
        verify_payload "$f"
    done
}

if [[ "${CLIPFAN_VERIFY_ONLY:-0}" == "1" ]]; then
    verify_payloads
    exit 0
fi

remove_release_payloads
build_go_payloads
build_pasteboard_helpers
verify_payloads

echo
echo "Staged in dist/:"
ls -la dist/
