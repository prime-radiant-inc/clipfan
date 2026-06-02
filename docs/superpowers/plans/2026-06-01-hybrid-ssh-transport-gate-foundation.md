# Hybrid SSH Transport Gate Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the release-gate foundation for the hybrid SSH transport so later config, provisioning, and runtime work can fail closed until the correct public milestone gates are enabled.

**Architecture:** Store gate intent in strict JSON manifests under `release/`, validate the manifests in Go, and generate Go/Swift constants from the same source. Swift UI policy consumes the generated constants so Add Peer provisioning is disabled while public sync gates are false, while regular user SSH update/check behavior remains available.

**Tech Stack:** Go 1.26, Swift 5.9, shell scripts, GitHub Actions, XCTest.

---

## Scope

This plan implements Milestones 0a1-0a5 from the hybrid persistent SSH transport spec. It does not implement config v2, host-key pinning, command-locked SSH keys, persistent streams, on-demand sync, safe mode, or peer HTTP removal. Those remain later plans.

## File Structure

- Create `release/ssh-transport-gates.json`: public transport gate manifest, all false in this slice.
- Create `release/ssh-runtime-gates.json`: runtime capability gate manifest, all false in this slice.
- Create `internal/releaseflags/manifest.go`: strict manifest types, parsing, exact-key checks, and bundle validation.
- Create `internal/releaseflags/manifest_test.go`: Go behavior tests for legal and illegal gate bundles.
- Create `internal/releaseflags/ssh_transport_gates.go`: generated Go constants from the transport manifest.
- Create `internal/releaseflags/ssh_runtime_gates.go`: generated Go constants from the runtime manifest.
- Create `cmd/generate-ssh-release-gates/main.go`: generator for Go and Swift outputs.
- Create `scripts/generate-ssh-release-gates.sh`: stable repo-root wrapper used by humans and CI.
- Create `scripts/test-ssh-release-gates.sh`: shell smoke test proving generated artifacts are current.
- Create `apps/mac/Clipfan/Sources/Clipfan/GeneratedSSHTransportGates.swift`: generated Swift transport constants.
- Create `apps/mac/Clipfan/Sources/Clipfan/GeneratedSSHRuntimeGates.swift`: generated Swift runtime constants.
- Create `apps/mac/Clipfan/Sources/Clipfan/SSHTransportGatePolicy.swift`: small Swift policy layer used by UI/runtime call sites.
- Create `apps/mac/Clipfan/Tests/ClipfanTests/SSHTransportGatePolicyTests.swift`: XCTest coverage for generated gates and policy.
- Modify `apps/mac/Clipfan/Sources/Clipfan/AddPeerSheet.swift`: disable provisioning UI while public Add Peer gates are false.
- Modify `apps/mac/Clipfan/Sources/Clipfan/DaemonClient.swift`: skip public peer HTTP version probes when `PeerHTTPRuntimeDisabled` is true.
- Modify `apps/mac/Clipfan/Sources/Clipfan/UpdatePeerSheet.swift`: keep regular SSH update enabled through the policy helper.
- Modify `apps/mac/Clipfan/Sources/Clipfan/FleetRow.swift`: keep version-probe health explicit and avoid treating disabled future SSH public success as healthy.
- Modify `.github/workflows/release.yml`: run the gate generation check before building release artifacts.

## Task 1: Strict Go Manifest Validation

**Files:**
- Create: `release/ssh-transport-gates.json`
- Create: `release/ssh-runtime-gates.json`
- Create: `internal/releaseflags/manifest.go`
- Create: `internal/releaseflags/manifest_test.go`

- [ ] **Step 1: Add failing tests for manifest parsing and bundle validation**

Create `internal/releaseflags/manifest_test.go`:

```go
package releaseflags

import (
	"strings"
	"testing"
)

func TestReadTransportGatesMapsBooleanValues(t *testing.T) {
	gates, err := ReadTransportGates(strings.NewReader(`{
		"PeerHTTPRuntimeDisabled": true,
		"ConfigV2WriteEnabled": true,
		"RemoteSecretWriteReleaseEnabled": false,
		"ssh_public_add_peer_success_enabled": false
	}`))
	if err != nil {
		t.Fatalf("read transport gates: %v", err)
	}
	want := TransportGates{
		PeerHTTPRuntimeDisabled:         true,
		ConfigV2WriteEnabled:            true,
		RemoteSecretWriteReleaseEnabled: false,
		SSHPublicAddPeerSuccessEnabled:  false,
	}
	if gates != want {
		t.Fatalf("transport gates = %#v, want %#v", gates, want)
	}
}

func TestReadRuntimeGatesMapsBooleanValues(t *testing.T) {
	gates, err := ReadRuntimeGates(strings.NewReader(`{
		"ssh_receive_primitive_enabled": true,
		"ssh_sync_stream_enabled": false,
		"ssh_persistent_current_enabled": true,
		"ssh_sync_key_rotation_enabled": false
	}`))
	if err != nil {
		t.Fatalf("read runtime gates: %v", err)
	}
	want := RuntimeGates{
		SSHReceivePrimitiveEnabled:  true,
		SSHSyncStreamEnabled:        false,
		SSHPersistentCurrentEnabled: true,
		SSHSyncKeyRotationEnabled:   false,
	}
	if gates != want {
		t.Fatalf("runtime gates = %#v, want %#v", gates, want)
	}
}

func TestReadTransportGatesRequiresExactKeys(t *testing.T) {
	_, err := ReadTransportGates(strings.NewReader(`{"PeerHTTPRuntimeDisabled":false}`))
	if err == nil || !strings.Contains(err.Error(), "missing_gate: ConfigV2WriteEnabled") {
		t.Fatalf("missing gate error = %v", err)
	}

	_, err = ReadTransportGates(strings.NewReader(`{
		"PeerHTTPRuntimeDisabled": false,
		"ConfigV2WriteEnabled": false,
		"RemoteSecretWriteReleaseEnabled": false,
		"ssh_public_add_peer_success_enabled": false,
		"surprise": false
	}`))
	if err == nil || !strings.Contains(err.Error(), "unexpected_gate: surprise") {
		t.Fatalf("unexpected gate error = %v", err)
	}
}

func TestReadRuntimeGatesRequiresExactKeys(t *testing.T) {
	_, err := ReadRuntimeGates(strings.NewReader(`{
		"ssh_receive_primitive_enabled": false,
		"ssh_sync_stream_enabled": false,
		"ssh_persistent_current_enabled": false
	}`))
	if err == nil || !strings.Contains(err.Error(), "missing_gate: ssh_sync_key_rotation_enabled") {
		t.Fatalf("missing runtime gate error = %v", err)
	}
}

func TestValidateGateBundleAllowsAllFalse(t *testing.T) {
	err := ValidateGateBundle(TransportGates{}, RuntimeGates{})
	if err != nil {
		t.Fatalf("all false gates should be valid: %v", err)
	}
}

func TestValidateGateBundleAllowsPublicLocalCutover(t *testing.T) {
	err := ValidateGateBundle(
		TransportGates{
			PeerHTTPRuntimeDisabled:         true,
			ConfigV2WriteEnabled:            true,
			RemoteSecretWriteReleaseEnabled: false,
			SSHPublicAddPeerSuccessEnabled:  false,
		},
		RuntimeGates{},
	)
	if err != nil {
		t.Fatalf("17d3a local cutover gates should be valid: %v", err)
	}
}

func TestValidateGateBundleAllowsPublicAddPeerWhenRuntimeReady(t *testing.T) {
	err := ValidateGateBundle(
		TransportGates{
			PeerHTTPRuntimeDisabled:         true,
			ConfigV2WriteEnabled:            true,
			RemoteSecretWriteReleaseEnabled: true,
			SSHPublicAddPeerSuccessEnabled:  true,
		},
		RuntimeGates{
			SSHReceivePrimitiveEnabled:  true,
			SSHSyncStreamEnabled:        true,
			SSHPersistentCurrentEnabled: true,
			SSHSyncKeyRotationEnabled:   true,
		},
	)
	if err != nil {
		t.Fatalf("17d3b public add peer gates should be valid: %v", err)
	}
}

func TestValidateGateBundleRejectsInvalidPublicOrdering(t *testing.T) {
	err := ValidateGateBundle(
		TransportGates{PeerHTTPRuntimeDisabled: true},
		RuntimeGates{},
	)
	if err == nil || !strings.Contains(err.Error(), "PeerHTTPRuntimeDisabled must match ConfigV2WriteEnabled") {
		t.Fatalf("ordering error = %v", err)
	}

	err = ValidateGateBundle(
		TransportGates{ConfigV2WriteEnabled: true},
		RuntimeGates{},
	)
	if err == nil || !strings.Contains(err.Error(), "PeerHTTPRuntimeDisabled must match ConfigV2WriteEnabled") {
		t.Fatalf("reverse ordering error = %v", err)
	}
}

func TestValidateGateBundleRejectsMismatchedPublicPeerGates(t *testing.T) {
	err := ValidateGateBundle(
		TransportGates{
			PeerHTTPRuntimeDisabled: true,
			ConfigV2WriteEnabled:    true,
			RemoteSecretWriteReleaseEnabled: true,
		},
		RuntimeGates{},
	)
	if err == nil || !strings.Contains(err.Error(), "RemoteSecretWriteReleaseEnabled must match ssh_public_add_peer_success_enabled") {
		t.Fatalf("mismatch error = %v", err)
	}
}

func TestValidateGateBundleRequiresRuntimeGatesForPublicAddPeer(t *testing.T) {
	err := ValidateGateBundle(
		TransportGates{
			PeerHTTPRuntimeDisabled:          true,
			ConfigV2WriteEnabled:             true,
			RemoteSecretWriteReleaseEnabled:  true,
			SSHPublicAddPeerSuccessEnabled:   true,
		},
		RuntimeGates{
			SSHReceivePrimitiveEnabled:  true,
			SSHSyncStreamEnabled:        true,
			SSHPersistentCurrentEnabled: true,
		},
	)
	if err == nil || !strings.Contains(err.Error(), "ssh_public_add_peer_success_enabled requires ssh_sync_key_rotation_enabled") {
		t.Fatalf("runtime gate error = %v", err)
	}
}

func TestReadGatesRejectNullAndNonBooleanValues(t *testing.T) {
	_, err := ReadTransportGates(strings.NewReader(`{
		"PeerHTTPRuntimeDisabled": null,
		"ConfigV2WriteEnabled": false,
		"RemoteSecretWriteReleaseEnabled": false,
		"ssh_public_add_peer_success_enabled": false
	}`))
	if err == nil || !strings.Contains(err.Error(), "gate_not_boolean: PeerHTTPRuntimeDisabled") {
		t.Fatalf("null gate error = %v", err)
	}
}

func TestReadGatesRejectDuplicateKeys(t *testing.T) {
	_, err := ReadTransportGates(strings.NewReader(`{
		"PeerHTTPRuntimeDisabled": false,
		"ConfigV2WriteEnabled": false,
		"RemoteSecretWriteReleaseEnabled": false,
		"ssh_public_add_peer_success_enabled": false,
		"PeerHTTPRuntimeDisabled": true
	}`))
	if err == nil || !strings.Contains(err.Error(), "duplicate_gate: PeerHTTPRuntimeDisabled") {
		t.Fatalf("duplicate gate error = %v", err)
	}
}
```

- [ ] **Step 2: Run the failing Go tests**

Run:

```bash
go test ./internal/releaseflags -run 'Test(Read|Validate)' -v
```

Expected: FAIL because package `internal/releaseflags` does not exist.

- [ ] **Step 3: Add the all-false manifests**

Create `release/ssh-transport-gates.json`:

```json
{
  "PeerHTTPRuntimeDisabled": false,
  "ConfigV2WriteEnabled": false,
  "RemoteSecretWriteReleaseEnabled": false,
  "ssh_public_add_peer_success_enabled": false
}
```

Create `release/ssh-runtime-gates.json`:

```json
{
  "ssh_receive_primitive_enabled": false,
  "ssh_sync_stream_enabled": false,
  "ssh_persistent_current_enabled": false,
  "ssh_sync_key_rotation_enabled": false
}
```

- [ ] **Step 4: Implement strict manifest parsing**

Create `internal/releaseflags/manifest.go`:

```go
package releaseflags

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

type TransportGates struct {
	PeerHTTPRuntimeDisabled         bool `json:"PeerHTTPRuntimeDisabled"`
	ConfigV2WriteEnabled            bool `json:"ConfigV2WriteEnabled"`
	RemoteSecretWriteReleaseEnabled bool `json:"RemoteSecretWriteReleaseEnabled"`
	SSHPublicAddPeerSuccessEnabled  bool `json:"ssh_public_add_peer_success_enabled"`
}

type RuntimeGates struct {
	SSHReceivePrimitiveEnabled  bool `json:"ssh_receive_primitive_enabled"`
	SSHSyncStreamEnabled        bool `json:"ssh_sync_stream_enabled"`
	SSHPersistentCurrentEnabled bool `json:"ssh_persistent_current_enabled"`
	SSHSyncKeyRotationEnabled   bool `json:"ssh_sync_key_rotation_enabled"`
}

func ReadTransportGates(r io.Reader) (TransportGates, error) {
	var gates TransportGates
	err := readExactJSON(r, map[string]struct{}{
		"PeerHTTPRuntimeDisabled": {},
		"ConfigV2WriteEnabled": {},
		"RemoteSecretWriteReleaseEnabled": {},
		"ssh_public_add_peer_success_enabled": {},
	}, &gates)
	return gates, err
}

func ReadRuntimeGates(r io.Reader) (RuntimeGates, error) {
	var gates RuntimeGates
	err := readExactJSON(r, map[string]struct{}{
		"ssh_receive_primitive_enabled": {},
		"ssh_sync_stream_enabled": {},
		"ssh_persistent_current_enabled": {},
		"ssh_sync_key_rotation_enabled": {},
	}, &gates)
	return gates, err
}

func ValidateGateBundle(transport TransportGates, runtime RuntimeGates) error {
	if transport.PeerHTTPRuntimeDisabled != transport.ConfigV2WriteEnabled {
		return fmt.Errorf("invalid_gate_bundle: PeerHTTPRuntimeDisabled must match ConfigV2WriteEnabled")
	}
	if transport.RemoteSecretWriteReleaseEnabled != transport.SSHPublicAddPeerSuccessEnabled {
		return fmt.Errorf("invalid_gate_bundle: RemoteSecretWriteReleaseEnabled must match ssh_public_add_peer_success_enabled")
	}
	if transport.RemoteSecretWriteReleaseEnabled || transport.SSHPublicAddPeerSuccessEnabled {
		if !transport.PeerHTTPRuntimeDisabled {
			return fmt.Errorf("invalid_gate_bundle: ssh_public_add_peer_success_enabled requires PeerHTTPRuntimeDisabled")
		}
		if !transport.ConfigV2WriteEnabled {
			return fmt.Errorf("invalid_gate_bundle: ssh_public_add_peer_success_enabled requires ConfigV2WriteEnabled")
		}
		required := map[string]bool{
			"ssh_receive_primitive_enabled":    runtime.SSHReceivePrimitiveEnabled,
			"ssh_sync_stream_enabled":          runtime.SSHSyncStreamEnabled,
			"ssh_persistent_current_enabled":   runtime.SSHPersistentCurrentEnabled,
			"ssh_sync_key_rotation_enabled":    runtime.SSHSyncKeyRotationEnabled,
		}
		for name, enabled := range required {
			if !enabled {
				return fmt.Errorf("invalid_gate_bundle: ssh_public_add_peer_success_enabled requires %s", name)
			}
		}
	}
	return nil
}

func readExactJSON(r io.Reader, expected map[string]struct{}, out any) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read_manifest: %w", err)
	}

	seen := make(map[string]struct{})
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("malformed_manifest: %w", err)
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("malformed_manifest: expected object")
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("malformed_manifest: %w", err)
		}
		name, ok := token.(string)
		if !ok {
			return fmt.Errorf("malformed_manifest: expected gate name")
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("duplicate_gate: %s", name)
		}
		seen[name] = struct{}{}
		if _, ok := expected[name]; !ok {
			return fmt.Errorf("unexpected_gate: %s", name)
		}
		var boolValue *bool
		if err := decoder.Decode(&boolValue); err != nil || boolValue == nil {
			return fmt.Errorf("gate_not_boolean: %s", name)
		}
	}
	if token, err := decoder.Token(); err != nil {
		return fmt.Errorf("malformed_manifest: %w", err)
	} else if delim, ok := token.(json.Delim); !ok || delim != '}' {
		return fmt.Errorf("malformed_manifest: expected object end")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("malformed_manifest: trailing data")
	}

	missing := make([]string, 0)
	for name := range expected {
		if _, ok := seen[name]; !ok {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return fmt.Errorf("missing_gate: %s", missing[0])
	}

	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("malformed_manifest: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: Run tests and commit**

Run:

```bash
go test ./internal/releaseflags -run 'Test(Read|Validate)' -v
```

Expected: PASS.

Commit:

```bash
git add release/ssh-transport-gates.json release/ssh-runtime-gates.json internal/releaseflags/manifest.go internal/releaseflags/manifest_test.go
git commit -m "feat: add SSH transport release gate manifests"
```

## Task 2: Generate Go and Swift Gate Constants

**Files:**
- Create: `cmd/generate-ssh-release-gates/main.go`
- Create: `scripts/generate-ssh-release-gates.sh`
- Create: `internal/releaseflags/ssh_transport_gates.go`
- Create: `internal/releaseflags/ssh_runtime_gates.go`
- Create: `apps/mac/Clipfan/Sources/Clipfan/GeneratedSSHTransportGates.swift`
- Create: `apps/mac/Clipfan/Sources/Clipfan/GeneratedSSHRuntimeGates.swift`

- [ ] **Step 1: Add the generator command**

Create `cmd/generate-ssh-release-gates/main.go`:

```go
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	transport, runtime, err := load(root)
	if err != nil {
		return err
	}
	if err := releaseflags.ValidateGateBundle(transport, runtime); err != nil {
		return err
	}
	outputs := map[string][]byte{
		"internal/releaseflags/ssh_transport_gates.go": goTransport(transport),
		"internal/releaseflags/ssh_runtime_gates.go": goRuntime(runtime),
		"apps/mac/Clipfan/Sources/Clipfan/GeneratedSSHTransportGates.swift": swiftTransport(transport),
		"apps/mac/Clipfan/Sources/Clipfan/GeneratedSSHRuntimeGates.swift": swiftRuntime(runtime),
	}
	for rel, body := range outputs {
		path := filepath.Join(root, rel)
		if err := os.WriteFile(path, body, 0644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find repo root")
		}
		dir = parent
	}
}

func load(root string) (releaseflags.TransportGates, releaseflags.RuntimeGates, error) {
	transportFile, err := os.Open(filepath.Join(root, "release/ssh-transport-gates.json"))
	if err != nil {
		return releaseflags.TransportGates{}, releaseflags.RuntimeGates{}, fmt.Errorf("missing_manifest: ssh-transport-gates.json: %w", err)
	}
	defer transportFile.Close()
	transport, err := releaseflags.ReadTransportGates(transportFile)
	if err != nil {
		return releaseflags.TransportGates{}, releaseflags.RuntimeGates{}, err
	}

	runtimeFile, err := os.Open(filepath.Join(root, "release/ssh-runtime-gates.json"))
	if err != nil {
		return releaseflags.TransportGates{}, releaseflags.RuntimeGates{}, fmt.Errorf("missing_manifest: ssh-runtime-gates.json: %w", err)
	}
	defer runtimeFile.Close()
	runtime, err := releaseflags.ReadRuntimeGates(runtimeFile)
	if err != nil {
		return releaseflags.TransportGates{}, releaseflags.RuntimeGates{}, err
	}
	return transport, runtime, nil
}

func goTransport(g releaseflags.TransportGates) []byte {
	var b bytes.Buffer
	fmt.Fprint(&b, "// Code generated by scripts/generate-ssh-release-gates.sh; DO NOT EDIT.\n\npackage releaseflags\n\nconst (\n")
	fmt.Fprintf(&b, "\tPeerHTTPRuntimeDisabled = %s\n", goBool(g.PeerHTTPRuntimeDisabled))
	fmt.Fprintf(&b, "\tConfigV2WriteEnabled = %s\n", goBool(g.ConfigV2WriteEnabled))
	fmt.Fprintf(&b, "\tRemoteSecretWriteReleaseEnabled = %s\n", goBool(g.RemoteSecretWriteReleaseEnabled))
	fmt.Fprintf(&b, "\tSSHPublicAddPeerSuccessEnabled = %s\n", goBool(g.SSHPublicAddPeerSuccessEnabled))
	fmt.Fprint(&b, ")\n")
	return b.Bytes()
}

func goRuntime(g releaseflags.RuntimeGates) []byte {
	var b bytes.Buffer
	fmt.Fprint(&b, "// Code generated by scripts/generate-ssh-release-gates.sh; DO NOT EDIT.\n\npackage releaseflags\n\nconst (\n")
	fmt.Fprintf(&b, "\tSSHReceivePrimitiveEnabled = %s\n", goBool(g.SSHReceivePrimitiveEnabled))
	fmt.Fprintf(&b, "\tSSHSyncStreamEnabled = %s\n", goBool(g.SSHSyncStreamEnabled))
	fmt.Fprintf(&b, "\tSSHPersistentCurrentEnabled = %s\n", goBool(g.SSHPersistentCurrentEnabled))
	fmt.Fprintf(&b, "\tSSHSyncKeyRotationEnabled = %s\n", goBool(g.SSHSyncKeyRotationEnabled))
	fmt.Fprint(&b, ")\n")
	return b.Bytes()
}

func swiftTransport(g releaseflags.TransportGates) []byte {
	var b bytes.Buffer
	fmt.Fprint(&b, "// Code generated by scripts/generate-ssh-release-gates.sh; DO NOT EDIT.\n\n")
	fmt.Fprint(&b, "enum GeneratedSSHTransportGates {\n")
	fmt.Fprintf(&b, "    static let peerHTTPRuntimeDisabled = %s\n", swiftBool(g.PeerHTTPRuntimeDisabled))
	fmt.Fprintf(&b, "    static let configV2WriteEnabled = %s\n", swiftBool(g.ConfigV2WriteEnabled))
	fmt.Fprintf(&b, "    static let remoteSecretWriteReleaseEnabled = %s\n", swiftBool(g.RemoteSecretWriteReleaseEnabled))
	fmt.Fprintf(&b, "    static let publicAddPeerSuccessEnabled = %s\n", swiftBool(g.SSHPublicAddPeerSuccessEnabled))
	fmt.Fprint(&b, "}\n")
	return b.Bytes()
}

func swiftRuntime(g releaseflags.RuntimeGates) []byte {
	var b bytes.Buffer
	fmt.Fprint(&b, "// Code generated by scripts/generate-ssh-release-gates.sh; DO NOT EDIT.\n\n")
	fmt.Fprint(&b, "enum GeneratedSSHRuntimeGates {\n")
	fmt.Fprintf(&b, "    static let receivePrimitiveEnabled = %s\n", swiftBool(g.SSHReceivePrimitiveEnabled))
	fmt.Fprintf(&b, "    static let syncStreamEnabled = %s\n", swiftBool(g.SSHSyncStreamEnabled))
	fmt.Fprintf(&b, "    static let persistentCurrentEnabled = %s\n", swiftBool(g.SSHPersistentCurrentEnabled))
	fmt.Fprintf(&b, "    static let syncKeyRotationEnabled = %s\n", swiftBool(g.SSHSyncKeyRotationEnabled))
	fmt.Fprint(&b, "}\n")
	return b.Bytes()
}

func goBool(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func swiftBool(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
```

- [ ] **Step 2: Add the shell wrapper**

Create `scripts/generate-ssh-release-gates.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd)
cd "$repo"
go run ./cmd/generate-ssh-release-gates
```

Make it executable:

```bash
chmod 0755 scripts/generate-ssh-release-gates.sh
```

- [ ] **Step 3: Run the generator**

Run:

```bash
bash scripts/generate-ssh-release-gates.sh
```

Expected: generated Go and Swift files are created with all constants set to `false`.

- [ ] **Step 4: Run generated package tests**

Run:

```bash
go test ./internal/releaseflags ./cmd/generate-ssh-release-gates -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/generate-ssh-release-gates/main.go scripts/generate-ssh-release-gates.sh internal/releaseflags/ssh_transport_gates.go internal/releaseflags/ssh_runtime_gates.go apps/mac/Clipfan/Sources/Clipfan/GeneratedSSHTransportGates.swift apps/mac/Clipfan/Sources/Clipfan/GeneratedSSHRuntimeGates.swift
git commit -m "feat: generate SSH release gate constants"
```

## Task 3: Add Regeneration Checks

**Files:**
- Create: `scripts/test-ssh-release-gates.sh`
- Modify: `.github/workflows/release.yml`

- [ ] **Step 1: Add the failing regeneration test**

Create `scripts/test-ssh-release-gates.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd)
cd "$repo"

bash scripts/generate-ssh-release-gates.sh

generated=(
  internal/releaseflags/ssh_transport_gates.go \
  internal/releaseflags/ssh_runtime_gates.go \
  apps/mac/Clipfan/Sources/Clipfan/GeneratedSSHTransportGates.swift \
  apps/mac/Clipfan/Sources/Clipfan/GeneratedSSHRuntimeGates.swift
)

for path in "${generated[@]}"; do
  git ls-files --error-unmatch "$path" >/dev/null || {
    echo "generated SSH release gate file is not tracked: $path" >&2
    exit 1
  }
done

if [[ -n "$(git status --porcelain -- "${generated[@]}")" ]]; then
  git status --short -- "${generated[@]}" >&2
  echo "SSH release gate generated files are stale. Run scripts/generate-ssh-release-gates.sh." >&2
  exit 1
fi

python3 - <<'PY'
import json
import pathlib
import sys

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

for path, wanted in expected.items():
    got = json.loads(pathlib.Path(path).read_text())
    if got != wanted:
        print(f"{path} does not match this slice's all-false release gate matrix", file=sys.stderr)
        print(f"got: {got}", file=sys.stderr)
        sys.exit(1)
PY

go test ./internal/releaseflags ./cmd/generate-ssh-release-gates -v
```

Make it executable:

```bash
chmod 0755 scripts/test-ssh-release-gates.sh
```

- [ ] **Step 2: Run the regeneration test**

Run:

```bash
bash scripts/test-ssh-release-gates.sh
```

Expected: PASS with no git diff.

- [ ] **Step 3: Add CI release check**

In `.github/workflows/release.yml`, add this step after `Setup Go` and before signing:

```yaml
      - name: Verify SSH Release Gates
        run: bash scripts/test-ssh-release-gates.sh
```

- [ ] **Step 4: Commit**

```bash
git add scripts/test-ssh-release-gates.sh .github/workflows/release.yml
git commit -m "ci: verify SSH release gate generation"
```

## Task 4: Add Swift Gate Policy

**Files:**
- Create: `apps/mac/Clipfan/Sources/Clipfan/SSHTransportGatePolicy.swift`
- Create: `apps/mac/Clipfan/Tests/ClipfanTests/SSHTransportGatePolicyTests.swift`

- [ ] **Step 1: Add policy tests**

Create `apps/mac/Clipfan/Tests/ClipfanTests/SSHTransportGatePolicyTests.swift`:

```swift
import XCTest
@testable import Clipfan

final class SSHTransportGatePolicyTests: XCTestCase {
    func testCurrentGeneratedPolicyKeepsPublicProvisioningDisabled() {
        XCTAssertFalse(SSHTransportGatePolicy.current.addPeerProvisioningEnabled)
        XCTAssertTrue(SSHTransportGatePolicy.current.regularSSHUpdateEnabled)
    }

    func testPublicProvisioningRequiresEveryTransportAndRuntimeGate() {
        var policy = SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: true,
            configV2WriteEnabled: true,
            remoteSecretWriteReleaseEnabled: true,
            publicAddPeerSuccessEnabled: true,
            receivePrimitiveEnabled: true,
            syncStreamEnabled: true,
            persistentCurrentEnabled: true,
            syncKeyRotationEnabled: false
        )
        XCTAssertFalse(policy.addPeerProvisioningEnabled)

        policy.syncKeyRotationEnabled = true
        XCTAssertTrue(policy.addPeerProvisioningEnabled)
    }

    func testPeerHTTPVersionProbeStopsWhenRuntimeHTTPIsDisabled() {
        let enabled = SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: false,
            configV2WriteEnabled: false,
            remoteSecretWriteReleaseEnabled: false,
            publicAddPeerSuccessEnabled: false,
            receivePrimitiveEnabled: false,
            syncStreamEnabled: false,
            persistentCurrentEnabled: false,
            syncKeyRotationEnabled: false
        )
        XCTAssertTrue(enabled.peerHTTPVersionProbeEnabled)

        let disabled = SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: true,
            configV2WriteEnabled: true,
            remoteSecretWriteReleaseEnabled: false,
            publicAddPeerSuccessEnabled: false,
            receivePrimitiveEnabled: false,
            syncStreamEnabled: false,
            persistentCurrentEnabled: false,
            syncKeyRotationEnabled: false
        )
        XCTAssertFalse(disabled.peerHTTPVersionProbeEnabled)
    }
}
```

- [ ] **Step 2: Run the failing Swift test**

Run:

```bash
cd apps/mac/Clipfan && swift test --filter SSHTransportGatePolicyTests
```

Expected: FAIL because `SSHTransportGatePolicy` does not exist.

- [ ] **Step 3: Add the policy type**

Create `apps/mac/Clipfan/Sources/Clipfan/SSHTransportGatePolicy.swift`:

```swift
struct SSHTransportGatePolicy {
    var peerHTTPRuntimeDisabled: Bool
    var configV2WriteEnabled: Bool
    var remoteSecretWriteReleaseEnabled: Bool
    var publicAddPeerSuccessEnabled: Bool
    var receivePrimitiveEnabled: Bool
    var syncStreamEnabled: Bool
    var persistentCurrentEnabled: Bool
    var syncKeyRotationEnabled: Bool

    static var current: SSHTransportGatePolicy {
        SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: GeneratedSSHTransportGates.peerHTTPRuntimeDisabled,
            configV2WriteEnabled: GeneratedSSHTransportGates.configV2WriteEnabled,
            remoteSecretWriteReleaseEnabled: GeneratedSSHTransportGates.remoteSecretWriteReleaseEnabled,
            publicAddPeerSuccessEnabled: GeneratedSSHTransportGates.publicAddPeerSuccessEnabled,
            receivePrimitiveEnabled: GeneratedSSHRuntimeGates.receivePrimitiveEnabled,
            syncStreamEnabled: GeneratedSSHRuntimeGates.syncStreamEnabled,
            persistentCurrentEnabled: GeneratedSSHRuntimeGates.persistentCurrentEnabled,
            syncKeyRotationEnabled: GeneratedSSHRuntimeGates.syncKeyRotationEnabled
        )
    }

    var addPeerProvisioningEnabled: Bool {
        peerHTTPRuntimeDisabled
            && configV2WriteEnabled
            && remoteSecretWriteReleaseEnabled
            && publicAddPeerSuccessEnabled
            && receivePrimitiveEnabled
            && syncStreamEnabled
            && persistentCurrentEnabled
            && syncKeyRotationEnabled
    }

    var regularSSHUpdateEnabled: Bool {
        true
    }

    var peerHTTPVersionProbeEnabled: Bool {
        !peerHTTPRuntimeDisabled
    }
}
```

- [ ] **Step 4: Run Swift policy tests and commit**

Run:

```bash
cd apps/mac/Clipfan && swift test --filter SSHTransportGatePolicyTests
```

Expected: PASS.

Commit:

```bash
git add apps/mac/Clipfan/Sources/Clipfan/SSHTransportGatePolicy.swift apps/mac/Clipfan/Tests/ClipfanTests/SSHTransportGatePolicyTests.swift
git commit -m "feat: add Swift SSH transport gate policy"
```

## Task 5: Wire Swift Consumers Without Enabling SSH Sync

**Files:**
- Modify: `apps/mac/Clipfan/Sources/Clipfan/AddPeerSheet.swift`
- Modify: `apps/mac/Clipfan/Sources/Clipfan/DaemonClient.swift`
- Modify: `apps/mac/Clipfan/Sources/Clipfan/UpdatePeerSheet.swift`
- Modify: `apps/mac/Clipfan/Sources/Clipfan/FleetRow.swift`
- Modify: `apps/mac/Clipfan/Tests/ClipfanTests/SSHTransportGatePolicyTests.swift`

- [ ] **Step 1: Add consumer tests**

Insert these tests into `SSHTransportGatePolicyTests.swift` before the final closing brace of `final class SSHTransportGatePolicyTests`:

```swift
    func testAddPeerInstallButtonDisabledUntilPublicProvisioningGateIsReady() {
        let disabled = SSHTransportGatePolicy.current
        XCTAssertTrue(isAddPeerInstallDisabled(installCount: 1, installing: false, policy: disabled))

        let enabled = SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: true,
            configV2WriteEnabled: true,
            remoteSecretWriteReleaseEnabled: true,
            publicAddPeerSuccessEnabled: true,
            receivePrimitiveEnabled: true,
            syncStreamEnabled: true,
            persistentCurrentEnabled: true,
            syncKeyRotationEnabled: true
        )
        XCTAssertFalse(isAddPeerInstallDisabled(installCount: 1, installing: false, policy: enabled))
    }

    func testRegularSSHUpdateButtonRemainsEnabledWhenPublicAddPeerIsDisabled() {
        XCTAssertFalse(isPeerUpdateButtonDisabled(host: "fsck.com", updating: false, policy: .current))
        XCTAssertTrue(isPeerUpdateButtonDisabled(host: "", updating: false, policy: .current))
        XCTAssertTrue(isPeerUpdateButtonDisabled(host: "fsck.com", updating: true, policy: .current))
    }

    func testPeerHTTPVersionProbeGateControlsDaemonClientPolicy() {
        XCTAssertTrue(shouldProbePeerHTTPVersions(policy: .current, localVersion: "v0.3.8", sharedKeyLoaded: true))
        let disabled = SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: true,
            configV2WriteEnabled: true,
            remoteSecretWriteReleaseEnabled: false,
            publicAddPeerSuccessEnabled: false,
            receivePrimitiveEnabled: false,
            syncStreamEnabled: false,
            persistentCurrentEnabled: false,
            syncKeyRotationEnabled: false
        )
        XCTAssertFalse(shouldProbePeerHTTPVersions(policy: disabled, localVersion: "v0.3.8", sharedKeyLoaded: true))
        XCTAssertFalse(shouldVerifyPeerHTTPVersionAfterUpdate(policy: disabled, expectedVersion: "v0.3.8"))
    }

    func testVerifyPeerVersionResultIsSkippedWhenPeerHTTPRuntimeIsDisabled() {
        let result = skippedPeerHTTPVersionVerification(host: "fsck.com")
        XCTAssertNil(result.status)
        XCTAssertEqual(result.detail, "fsck.com peer HTTP version verification is disabled by SSH transport gates")
    }

    @MainActor
    func testDaemonClientVerifyPeerVersionSkipsHTTPFetchWhenDisabled() async {
        let daemon = DaemonClient.shared
        let oldPolicy = daemon.transportGatePolicy
        let oldFetch = daemon.peerVersionFetch
        let oldPeers = daemon.peers
        let oldVersions = daemon.peerVersions
        defer {
            daemon.transportGatePolicy = oldPolicy
            daemon.peerVersionFetch = oldFetch
            daemon.peers = oldPeers
            daemon.peerVersions = oldVersions
        }

        daemon.transportGatePolicy = SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: true,
            configV2WriteEnabled: true,
            remoteSecretWriteReleaseEnabled: false,
            publicAddPeerSuccessEnabled: false,
            receivePrimitiveEnabled: false,
            syncStreamEnabled: false,
            persistentCurrentEnabled: false,
            syncKeyRotationEnabled: false
        )
        daemon.peers = [Peer(hostname: "fsck.com",
                             port: 7853,
                             last_push_ts: nil,
                             last_push_ok: false,
                             last_push_err: nil,
                             last_recv_ts: nil)]
        daemon.peerVersions = ["fsck.com": .current("v0.3.8")]
        var fetchCalled = false
        daemon.peerVersionFetch = { _, _, _ in
            fetchCalled = true
            return "v0.3.8"
        }

        let result = await daemon.verifyPeerVersion(hostname: "fsck.com",
                                                    expectedVersion: "v0.3.8")

        XCTAssertFalse(fetchCalled)
        XCTAssertNil(result?.status)
        XCTAssertNil(daemon.peerVersions["fsck.com"])
    }

    @MainActor
    func testDaemonClientRefreshPeerVersionsClearsWithoutHTTPFetchWhenDisabled() async {
        let daemon = DaemonClient.shared
        let oldPolicy = daemon.transportGatePolicy
        let oldFetch = daemon.peerVersionFetch
        let oldVersion = daemon.version
        let oldPeers = daemon.peers
        let oldVersions = daemon.peerVersions
        defer {
            daemon.transportGatePolicy = oldPolicy
            daemon.peerVersionFetch = oldFetch
            daemon.version = oldVersion
            daemon.peers = oldPeers
            daemon.peerVersions = oldVersions
        }

        daemon.transportGatePolicy = SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: true,
            configV2WriteEnabled: true,
            remoteSecretWriteReleaseEnabled: false,
            publicAddPeerSuccessEnabled: false,
            receivePrimitiveEnabled: false,
            syncStreamEnabled: false,
            persistentCurrentEnabled: false,
            syncKeyRotationEnabled: false
        )
        daemon.version = "v0.3.8"
        daemon.peers = [Peer(hostname: "fsck.com",
                             port: 7853,
                             last_push_ts: nil,
                             last_push_ok: false,
                             last_push_err: nil,
                             last_recv_ts: nil)]
        daemon.peerVersions = ["fsck.com": .needsUpdate("v0.3.7")]
        var fetchCalled = false
        daemon.peerVersionFetch = { _, _, _ in
            fetchCalled = true
            return "v0.3.8"
        }

        await daemon.refreshPeerVersions()

        XCTAssertFalse(fetchCalled)
        XCTAssertTrue(daemon.peerVersions.isEmpty)
    }

    func testFleetRowsIgnoreHTTPVersionHealthWhenPeerHTTPRuntimeIsDisabled() {
        let disabled = SSHTransportGatePolicy(
            peerHTTPRuntimeDisabled: true,
            configV2WriteEnabled: true,
            remoteSecretWriteReleaseEnabled: false,
            publicAddPeerSuccessEnabled: false,
            receivePrimitiveEnabled: false,
            syncStreamEnabled: false,
            persistentCurrentEnabled: false,
            syncKeyRotationEnabled: false
        )
        let peer = Peer(hostname: "fsck.com",
                        port: 7853,
                        last_push_ts: nil,
                        last_push_ok: false,
                        last_push_err: nil,
                        last_recv_ts: nil)

        let rows = fleetRows(origin: "m4",
                             connected: true,
                             peers: [peer],
                             peerVersions: ["fsck.com": .current("v0.3.8")],
                             policy: disabled)

        XCTAssertEqual(rows[1].health, .down)
        XCTAssertEqual(rows[1].subtitle, "port 7853")
    }
```

- [ ] **Step 2: Run failing consumer tests**

Run:

```bash
cd apps/mac/Clipfan && swift test --filter SSHTransportGatePolicyTests
```

Expected: FAIL because the helper functions do not exist.

- [ ] **Step 3: Add helpers and wire call sites**

Implement these helpers in the existing Swift files near the behavior they control:

```swift
func isAddPeerInstallDisabled(installCount: Int, installing: Bool, policy: SSHTransportGatePolicy = .current) -> Bool {
    installCount == 0 || installing || !policy.addPeerProvisioningEnabled
}
```

Use it in `AddPeerSheet`:

```swift
.disabled(isAddPeerInstallDisabled(installCount: installCount, installing: installing))
```

Add this helper in `UpdatePeerSheet.swift`:

```swift
func isPeerUpdateButtonDisabled(host: String, updating: Bool, policy: SSHTransportGatePolicy = .current) -> Bool {
    host.isEmpty || updating || !policy.regularSSHUpdateEnabled
}
```

Use it in `UpdatePeerSheet`:

```swift
.disabled(isPeerUpdateButtonDisabled(host: host, updating: updating))
```

Add this helper in `DaemonClient.swift`:

Add these test injection properties to `DaemonClient`:

```swift
var transportGatePolicy: SSHTransportGatePolicy = .current
var peerVersionFetch: PeerUpdateVerifier.Fetch = PeerVersionProbe.fetch
```

```swift
func shouldProbePeerHTTPVersions(policy: SSHTransportGatePolicy = .current,
                                 localVersion: String?,
                                 sharedKeyLoaded: Bool) -> Bool {
    policy.peerHTTPVersionProbeEnabled && localVersion != nil && sharedKeyLoaded
}

func shouldVerifyPeerHTTPVersionAfterUpdate(policy: SSHTransportGatePolicy = .current,
                                            expectedVersion: String?) -> Bool {
    policy.peerHTTPVersionProbeEnabled && expectedVersion != nil
}

func skippedPeerHTTPVersionVerification(host: String) -> PeerUpdateVerificationResult {
    PeerUpdateVerificationResult(
        status: nil,
        detail: "\(host) peer HTTP version verification is disabled by SSH transport gates"
    )
}
```

Use `transportGatePolicy` at the start of `refreshPeerVersions()` before `loadSharedKey()` or any network setup:

```swift
guard transportGatePolicy.peerHTTPVersionProbeEnabled else {
    peerVersions = [:]
    return
}
guard let key = loadSharedKey(), let localVersion = version else {
    peerVersions = [:]
    return
}
```

Before entering the task group, capture the injected fetch closure:

```swift
let fetch = peerVersionFetch
```

Inside the existing task group, call `PeerVersionProbe.fetch` only through that captured closure:

```swift
let remoteVersion = try await fetch(peer.hostname, peer.port, key)
```

Use the same policy in `refreshPeerVersion()` and `verifyPeerVersion()`. When peer HTTP runtime is disabled, neither method may call `PeerVersionProbe.fetch`, `PeerUpdateVerifier.verify`, or construct an off-host `http://<peer>:7853/v1/version` URL. `refreshPeerVersion()` should return `nil`. `verifyPeerVersion()` should remove that host from `peerVersions` and return `skippedPeerHTTPVersionVerification(host:)`.

In `verifyPeerVersion()`, pass `peerVersionFetch` to `PeerUpdateVerifier.verify` so the tests can prove the disabled path performs no network work:

```swift
let result = await PeerUpdateVerifier.verify(host: peer.hostname,
                                             port: peer.port,
                                             key: key,
                                             expectedVersion: expectedVersion,
                                             attempts: attempts,
                                             delayNanoseconds: delayNanoseconds,
                                             fetch: peerVersionFetch)
```

In `UpdatePeerSheet.update()`, guard the post-update verification block:

```swift
if shouldVerifyPeerHTTPVersionAfterUpdate(expectedVersion: version) {
    await MainActor.run {
        progress = "\(targetHost): Updated to \(version). Verifying daemon..."
        log.record(.init(step: "Verify", detail: "probing signed /v1/version on \(peer.hostname)"))
    }
    let result = await DaemonClient.shared.verifyPeerVersion(hostname: peer.hostname,
                                                             expectedVersion: version,
                                                             attempts: 6,
                                                             delayNanoseconds: 1_000_000_000)
    let shouldDismiss = shouldDismissPeerUpdateSheet(result)
    await MainActor.run {
        if case .current = result?.status {
            log.record(.init(step: "Verify", detail: result?.detail ?? "\(peer.hostname) is running \(version)"))
            progress = "\(targetHost): Updated to \(version)."
        } else {
            log.record(.init(step: "Verify",
                             detail: result?.detail ?? "\(peer.hostname) did not answer with the current daemon version yet"))
            progress = "\(targetHost): Updated to \(version); daemon verification is still pending."
        }
    }
    if shouldDismiss {
        try? await Task.sleep(nanoseconds: 1_000_000_000)
        await MainActor.run {
            updating = false
            dismiss()
        }
    } else {
        await MainActor.run {
            updating = false
        }
    }
} else {
    await MainActor.run {
        log.record(.init(step: "Verify", detail: skippedPeerHTTPVersionVerification(host: peer.hostname).detail))
        progress = "\(targetHost): Updated to \(version)."
        updating = false
    }
}
```

Do not add a new SSH probe in this task; when peer HTTP runtime is disabled, all peer HTTP version methods should clear or skip version state and return without network I/O.

Change `fleetRows` in `FleetRow.swift` to accept an optional policy and ignore peer HTTP version-derived health when the gate has disabled peer HTTP runtime:

```swift
func fleetRows(origin: String,
               connected: Bool,
               peers: [Peer],
               peerVersions: [String: PeerVersionStatus] = [:],
               policy: SSHTransportGatePolicy = .current) -> [FleetRowModel]
```

Inside the existing `peers.map` body, replace `let versionStatus = peerVersions[p.hostname]` with:

```swift
let versionStatus = policy.peerHTTPVersionProbeEnabled ? peerVersions[p.hostname] : nil
```

The existing `StatusMenuView` and `SettingsView` call sites should not need explicit arguments because the default policy is `.current`.

- [ ] **Step 4: Run Swift consumer tests and commit**

Run:

```bash
cd apps/mac/Clipfan && swift test --filter SSHTransportGatePolicyTests
```

Expected: PASS.

Commit:

```bash
git add apps/mac/Clipfan/Sources/Clipfan/AddPeerSheet.swift apps/mac/Clipfan/Sources/Clipfan/DaemonClient.swift apps/mac/Clipfan/Sources/Clipfan/UpdatePeerSheet.swift apps/mac/Clipfan/Sources/Clipfan/FleetRow.swift apps/mac/Clipfan/Tests/ClipfanTests/SSHTransportGatePolicyTests.swift
git commit -m "feat: wire Swift SSH transport gates"
```

## Task 6: Full Verification

**Files:**
- No new files.

- [ ] **Step 1: Run Go tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Run Swift tests**

Run:

```bash
cd apps/mac/Clipfan && swift test
```

Expected: PASS.

- [ ] **Step 3: Run release-gate regeneration and release payload helper tests**

Run:

```bash
bash scripts/test-ssh-release-gates.sh
bash dist/test-build-all-helper.sh
```

Expected: both PASS.

- [ ] **Step 4: Inspect worktree and commit any missed verification-only fix**

Run:

```bash
git status --short
```

Expected: only unrelated pre-existing files remain untracked/modified. Do not stage unrelated `.gitignore` changes or roborev artifacts.

## Self-Review

- Spec coverage: This plan covers Milestones 0a1 through 0a5 only. It intentionally leaves Milestones 0b and later for follow-on plans.
- Placeholder scan: There are no `TBD`, `TODO`, or "implement later" placeholders in the planned implementation steps.
- Type consistency: Go uses `TransportGates`, `RuntimeGates`, and `ValidateGateBundle`. Swift uses `SSHTransportGatePolicy`, `GeneratedSSHTransportGates`, and `GeneratedSSHRuntimeGates`.
