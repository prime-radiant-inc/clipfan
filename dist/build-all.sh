#!/usr/bin/env bash
# Cross-compile clipfan + clipfan-shim for every supported target and stage
# everything (with install.sh + unit files) in dist/.
set -euo pipefail

cd "$(dirname "$0")/.."
mkdir -p dist
ldflags="-s -w"

for goos in darwin linux; do
    for goarch in amd64 arm64; do
        echo "[build] clipfan $goos/$goarch"
        GOOS=$goos GOARCH=$goarch go build -ldflags "$ldflags" \
            -o "dist/clipfan-$goos-$goarch" ./cmd/clipfan
        if [[ "$goos" == "linux" ]]; then
            echo "[build] clipfan-shim $goos/$goarch"
            GOOS=$goos GOARCH=$goarch go build -ldflags "$ldflags" \
                -o "dist/clipfan-shim-$goos-$goarch" ./cmd/clipfan-shim
        fi
    done
done

# Menubar app is macOS-only and uses cgo (NSStatusItem). Build only on a Mac,
# only for the current arch — cross-cgo is more trouble than it's worth here.
if [[ "$(uname -s)" == "Darwin" ]]; then
    goarch=$(uname -m)
    [[ $goarch == "x86_64" ]] && goarch=amd64
    [[ $goarch == "aarch64" ]] && goarch=arm64
    echo "[build] clipfan-menu darwin/$goarch (cgo)"
    # Avoid the homebrew ccache shim that breaks if libfmt is mid-upgrade.
    PATH=/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin \
        CC=/usr/bin/cc \
        GOOS=darwin GOARCH=$goarch \
        go build -ldflags "$ldflags" \
            -o "dist/clipfan-menu-darwin-$goarch" ./cmd/clipfan-menu
fi

echo
echo "Staged in dist/:"
ls -la dist/
