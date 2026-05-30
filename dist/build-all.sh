#!/usr/bin/env bash
# Cross-compile clipfan + clipfan-shim for every supported target and stage
# everything (with install.sh + unit files) in dist/.
set -euo pipefail

cd "$(dirname "$0")/.."
mkdir -p dist
version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
ldflags="-s -w -X github.com/prime-radiant-inc/clipfan/internal/version.Version=$version"

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

echo
echo "Staged in dist/:"
ls -la dist/
