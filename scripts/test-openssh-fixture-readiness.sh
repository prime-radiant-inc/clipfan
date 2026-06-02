#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture="$repo_root/scripts/openssh-fixture-readiness.sh"
tmp="${TMPDIR:-/tmp}/clipfan-openssh-fixture-test.$$"
mkdir -p "$tmp"
trap 'rm -rf "$tmp"' EXIT

bash -n "$fixture"

case "$(uname -s)" in
  Darwin)
    unavailable_target="ubuntu"
    sentinel="ssh_fixture_unavailable"
    ;;
  Linux)
    unavailable_target="macos"
    sentinel="macos_ssh_fixture_unavailable"
    ;;
  *)
    echo "skipping OpenSSH fixture sentinel test on unsupported host OS"
    exit 0
    ;;
esac

artifact="$tmp/readiness.json"
CLIPFAN_OPENSSH_FIXTURE_ARTIFACT="$artifact" \
  "$fixture" "$unavailable_target" >/dev/null 2>"$tmp/nonblocking.err"

if ! grep -q "\"status\": \"$sentinel\"" "$artifact"; then
  echo "expected nonblocking artifact status $sentinel" >&2
  cat "$artifact" >&2 || true
  exit 1
fi
if command -v python3 >/dev/null 2>&1; then
  python3 -m json.tool "$artifact" >/dev/null
fi

if CLIPFAN_OPENSSH_FIXTURE_REQUIRED=1 \
  CLIPFAN_OPENSSH_FIXTURE_ARTIFACT="$tmp/required.json" \
  "$fixture" "$unavailable_target" >/dev/null 2>"$tmp/required.err"; then
  echo "required unavailable fixture unexpectedly succeeded" >&2
  exit 1
fi

if ! grep -q "\"status\": \"$sentinel\"" "$tmp/required.json"; then
  echo "expected required artifact status $sentinel" >&2
  cat "$tmp/required.json" >&2 || true
  exit 1
fi
if command -v python3 >/dev/null 2>&1; then
  python3 -m json.tool "$tmp/required.json" >/dev/null
fi
