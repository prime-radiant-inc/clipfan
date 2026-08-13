#!/usr/bin/env bash
# Universal clipfan installer for macOS and Linux.
#
# One-line install (from a published GitHub release):
#
#   curl -fsSL https://github.com/<owner>/clipfan/releases/latest/download/install.sh | bash
#
# Or run from a source clone:
#
#   bash scripts/install.sh [--with-tmux|--no-tmux] [--no-restart]
#
# This script detects the OS and arch, downloads the matching prebuilt tarball
# from GitHub releases, extracts it, and hands off to dist/install.sh (which
# installs the binary, the launchd/systemd user unit, and optional tmux
# integration). Any flags after `--` or given directly are forwarded verbatim.
#
# Environment overrides:
#   CLIPFAN_REPO   GitHub repo to download from (default set below; CI rewrites
#                  it to the releasing repo at upload time).
set -euo pipefail

CLIPFAN_REPO="${CLIPFAN_REPO:-prime-radiant-inc/clipfan}"

usage() {
    cat <<EOF
clipfan installer — macOS + Linux

  curl -fsSL https://github.com/$CLIPFAN_REPO/releases/latest/download/install.sh | bash

Flags (forwarded to the OS installer, dist/install.sh):
  --with-tmux    always install tmux copy integration
  --no-tmux      skip tmux integration
  --no-restart   install files only; do not load/enable/restart the service

Environment:
  CLIPFAN_REPO   GitHub repo to download from (default: $CLIPFAN_REPO)
EOF
}

# Allow --help / -h even when piped to bash (no positional args yet).
for a in "$@"; do
    case "$a" in
        -h|--help) usage; exit 0 ;;
    esac
done

uname_s=$(uname -s)
uname_m=$(uname -m)
case "$uname_s" in
    Darwin) goos=darwin ;;
    Linux)  goos=linux ;;
    *) echo "clipfan: unsupported OS '$uname_s' (clipfan runs on macOS + Linux)" >&2; exit 1 ;;
esac
case "$uname_m" in
    x86_64|amd64)  arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) echo "clipfan: unsupported arch '$uname_m' (want amd64 or arm64)" >&2; exit 1 ;;
esac

have() { command -v "$1" >/dev/null 2>&1; }

download() {
    # $1 = url, $2 = output path
    if have curl; then curl -fsSL "$1" -o "$2"
    elif have wget; then wget -qO "$2" "$1"
    else
        echo "clipfan: need 'curl' or 'wget' to download the tarball" >&2
        exit 1
    fi
}

tarball="clipfan-$goos-$arch.tar.gz"
url="https://github.com/$CLIPFAN_REPO/releases/latest/download/$tarball"

tmpdir=$(mktemp -d 2>/dev/null || mktemp -d -t clipfan)
trap 'rm -rf "$tmpdir"' EXIT

echo ">> Downloading $url"
if ! download "$url" "$tmpdir/$tarball"; then
    echo "clipfan: download failed. If no release is published yet at" >&2
    echo "  $url" >&2
    echo "there is nothing to install. Build from source instead:" >&2
    echo "  bash dist/build-all.sh && cd dist && ./install.sh" >&2
    exit 1
fi

# Guard against GitHub serving an HTML 404 page as a "tarball".
if ! tar -tzf "$tmpdir/$tarball" >/dev/null 2>&1; then
    echo "clipfan: downloaded file is not a tarball (release asset missing at the URL above?)" >&2
    exit 1
fi

tar -xzf "$tmpdir/$tarball" -C "$tmpdir"

installer=$(find "$tmpdir" -type f -name install.sh | head -1)
if [[ -z "$installer" ]]; then
    echo "clipfan: install.sh missing from tarball" >&2
    exit 1
fi
installdir=$(dirname "$installer")

# Forward any caller flags (e.g. --no-tmux) to the OS installer.
echo ">> Running $(basename "$installer") in $installdir"
cd "$installdir"
exec ./install.sh "$@"
