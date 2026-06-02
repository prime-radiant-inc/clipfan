#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

work="$tmp/clipfan"
mkdir -p "$work"
rsync -a \
  --exclude '.git' \
  --exclude '.build' \
  --exclude 'apps/mac/Clipfan/.build' \
  "$repo_root/" "$work/"

cd "$work"
cp release/internal-test/ssh-transport-gates.json release/ssh-transport-gates.json
cp release/internal-test/ssh-runtime-gates.json release/ssh-runtime-gates.json
bash scripts/generate-ssh-release-gates.sh

go test ./internal/releaseflags -run 'TestInternalTestLocalCutoverManifestEnablesOnlyConfigV2AndPeerHTTPGates|TestValidateGateBundleAccepts17d3aLocalCutoverBundle'
go test ./internal/cli -run TestGeneratedLocalFleetResetSucceedsWithInternalConfigV2Gate

(cd apps/mac/Clipfan && swift test --filter GeneratedConfigV2WriteGateEnablesConfirmedLocalFleetResetPlan)
