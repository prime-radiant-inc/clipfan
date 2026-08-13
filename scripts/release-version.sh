#!/usr/bin/env bash
set -euo pipefail

event=${GITHUB_EVENT_NAME:-}
ref=${GITHUB_REF_NAME:-}
if [[ "$event" == "push" && "$ref" == v* ]]; then
    version=${ref#v}
else
    if [[ ! -f DAEMON_VERSION ]]; then
        echo "release-version: missing DAEMON_VERSION" >&2
        exit 1
    fi
    version=$(tr -d '\r\n' < DAEMON_VERSION)
    version=${version#v}
fi

if [[ -z "$version" ]]; then
    echo "release-version: version is empty" >&2
    exit 1
fi

printf '%s\n' "$version"
