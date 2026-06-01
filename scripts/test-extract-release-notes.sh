#!/usr/bin/env bash
set -euo pipefail

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

changelog="$tmpdir/CHANGELOG.md"
out="$tmpdir/release-notes.md"
cat >"$changelog" <<'CHANGELOG'
# Changelog

## [0.3.5] - 2026-06-01

### Added

- Remote peer update prompts.
- Sparkle release notes.

## [0.3.4] - 2026-05-31

### Fixed

- Older entry.
CHANGELOG

bash scripts/extract-release-notes.sh 0.3.5 "$changelog" "$out"

expected="$tmpdir/expected.md"
cat >"$expected" <<'EXPECTED'
# Clipfan 0.3.5

### Added

- Remote peer update prompts.
- Sparkle release notes.
EXPECTED

diff -u "$expected" "$out"

if bash scripts/extract-release-notes.sh 9.9.9 "$changelog" "$tmpdir/missing.md" 2>/dev/null; then
  echo "expected missing changelog section to fail" >&2
  exit 1
fi

