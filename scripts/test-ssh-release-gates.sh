#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

bash scripts/generate-ssh-release-gates.sh

generated=(
  internal/releaseflags/ssh_transport_gates.go
  internal/releaseflags/ssh_runtime_gates.go
  apps/mac/Clipfan/Sources/Clipfan/GeneratedSSHTransportGates.swift
  apps/mac/Clipfan/Sources/Clipfan/GeneratedSSHRuntimeGates.swift
)

for path in "${generated[@]}"; do
  if ! git ls-files --error-unmatch "$path" >/dev/null 2>&1; then
    echo "Generated file is not tracked: $path" >&2
    exit 1
  fi
done

dirty="$(git status --porcelain -- "${generated[@]}")"
if [ -n "$dirty" ]; then
  echo "Generated SSH release gate files are stale or dirty:" >&2
  echo "$dirty" >&2
  exit 1
fi

python3 - <<'PY'
import json
from pathlib import Path

expected = {
    "release/ssh-transport-gates.json": {
        "PeerHTTPRuntimeDisabled": False,
        "ConfigV2WriteEnabled": False,
        "RemoteSecretWriteReleaseEnabled": False,
        "ssh_public_add_peer_success_enabled": False,
    },
    "release/ssh-runtime-gates.json": {
        "ssh_receive_primitive_enabled": False,
        "ssh_sync_stream_enabled": False,
        "ssh_persistent_current_enabled": False,
        "ssh_sync_key_rotation_enabled": False,
    },
}

for path, expected_matrix in expected.items():
    actual = json.loads(Path(path).read_text())
    if actual != expected_matrix:
        raise SystemExit(
            f"{path} does not match the public release gate matrix.\n"
            f"Expected: {expected_matrix}\n"
            f"Actual:   {actual}"
        )
PY

go test ./internal/releaseflags ./cmd/generate-ssh-release-gates -v
