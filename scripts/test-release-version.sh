#!/usr/bin/env bash
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT

printf 'v1.0.9\n' > "$fixture/DAEMON_VERSION"

tag_version=$(cd "$fixture" && GITHUB_EVENT_NAME=push GITHUB_REF_NAME=v2.3.4 bash "$here/release-version.sh")
if [[ "$tag_version" != "2.3.4" ]]; then
    echo "FAIL - tag version (want 2.3.4 got $tag_version)" >&2
    exit 1
fi

manual_version=$(cd "$fixture" && GITHUB_EVENT_NAME=workflow_dispatch GITHUB_REF_NAME=main bash "$here/release-version.sh")
if [[ "$manual_version" != "1.0.9" ]]; then
    echo "FAIL - manual version (want 1.0.9 got $manual_version)" >&2
    exit 1
fi

: > "$fixture/DAEMON_VERSION"
if (cd "$fixture" && GITHUB_EVENT_NAME=workflow_dispatch GITHUB_REF_NAME=main bash "$here/release-version.sh") >/dev/null 2>&1; then
    echo "FAIL - empty DAEMON_VERSION was accepted" >&2
    exit 1
fi

echo "ALL PASS"
