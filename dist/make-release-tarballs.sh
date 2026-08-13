#!/usr/bin/env bash
# Build per-(os,arch) release tarballs from the staged dist/ payloads produced
# by dist/build-all.sh. Each tarball expands to a single clipfan/ directory
# containing everything dist/install.sh needs for that target, so a Linux user
# on an empty box can:
#
#   curl -fsSL .../install.sh | bash
#
# and get a working daemon. Run after dist/build-all.sh has populated dist/.
#
#   dist/make-release-tarballs.sh [output-dir]
#
# Default output dir is dist/release. Writes one tarball per (os,arch):
#   clipfan-darwin-amd64.tar.gz   clipfan-darwin-arm64.tar.gz
#   clipfan-linux-amd64.tar.gz    clipfan-linux-arm64.tar.gz
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/.." && pwd)
dist="$repo/dist"
out="${1:-$dist/release}"
mkdir -p "$out"

# Verify build-all.sh output exists before we start slicing it up.
required=(
    install.sh bootstrap-self-ssh.sh tmux.conf.snippet
    clipfan-darwin-amd64 clipfan-darwin-arm64
    clipfan-linux-amd64  clipfan-linux-arm64
    clipfan-pasteboard-helper-darwin-amd64 clipfan-pasteboard-helper-darwin-arm64
    clipfan-shim-linux-amd64 clipfan-shim-linux-arm64
    com.primeradiant.clipfan.plist clipfan.service
)
for f in "${required[@]}"; do
    [[ -r "$dist/$f" ]] || { echo "missing dist/$f (run dist/build-all.sh first)" >&2; exit 1; }
done

# Pack a single (os,arch) tarball. The tarball root is clipfan/ so it extracts
# to a clean directory no matter where the user runs tar.
make_tarball() {
    local goos="$1" arch="$2"
    local stage
    stage=$(mktemp -d)
    trap 'rm -rf "$stage"' RETURN
    mkdir -p "$stage/clipfan"

    # Shared payload: the installer, the tmux snippet, and this target's daemon.
    cp "$dist/install.sh"            "$stage/clipfan/"
    cp "$dist/tmux.conf.snippet"     "$stage/clipfan/"
    cp "$dist/clipfan-$goos-$arch"   "$stage/clipfan/"
    chmod +x "$stage/clipfan/install.sh" "$stage/clipfan/clipfan-$goos-$arch"

    case "$goos" in
        darwin)
            cp "$dist/clipfan-pasteboard-helper-$goos-$arch" "$stage/clipfan/"
            chmod +x "$stage/clipfan/clipfan-pasteboard-helper-$goos-$arch"
            cp "$dist/com.primeradiant.clipfan.plist" "$stage/clipfan/"
            cp "$dist/bootstrap-self-ssh.sh" "$stage/clipfan/"
            chmod +x "$stage/clipfan/bootstrap-self-ssh.sh"
            ;;
        linux)
            cp "$dist/clipfan-shim-$goos-$arch" "$stage/clipfan/"
            chmod +x "$stage/clipfan/clipfan-shim-$goos-$arch"
            cp "$dist/clipfan.service" "$stage/clipfan/"
            ;;
    esac

    tar -C "$stage" -czf "$out/clipfan-$goos-$arch.tar.gz" clipfan
    echo "built $out/clipfan-$goos-$arch.tar.gz"
}

for goos in darwin linux; do
    for arch in amd64 arm64; do
        make_tarball "$goos" "$arch"
    done
done

echo
echo "Tarballs in $out:"
ls -la "$out"
