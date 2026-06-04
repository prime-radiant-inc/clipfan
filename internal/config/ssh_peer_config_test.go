package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestReadSSHPeerRedactsSecretsAndIncludesRevision(t *testing.T) {
	path := writeConfigForV2Test(t, `{
  "config_version": 2,
  "config_revision": 7,
  "shared_key": "secret",
  "hostname": "m4",
  "transport": "ssh",
  "ssh": {
    "sync_key": "/Users/jesse/.config/clipfan/ssh/sync_ed25519",
    "known_hosts": "/Users/jesse/.config/clipfan/ssh/known_hosts",
    "peers": [{
      "id": "fsck",
      "enabled": true,
      "accept": true,
      "connect": true,
      "persistent": true,
      "on_demand": true,
      "ssh_host": "fsck.com",
      "ssh_user": "jesse",
      "ssh_port": 22,
      "install_path": "/home/jesse/.local/bin/clipfan",
      "gateway_path": "/home/jesse/.local/bin/clipfan",
      "migration_state": "loopback_unprovisioned",
      "shared_key": "peer-secret",
      "private_key": "peer-private",
      "accept_proof": "future-accept-proof-token",
      "connect_proof": {"hmac": "future-connect-proof-token"},
      "proof": {"future_proof": {"keep": true, "shared_key": "proof-secret"}},
      "service_metadata": {
        "keep": true,
        "shared_key": "nested-secret",
        "items": [{"keep": "item", "private_key": "nested-private"}]
      }
    }]
  }
}`)

	status, err := ReadSSHPeer(path, "fsck")
	if err != nil {
		t.Fatal(err)
	}
	if status.RevisionState != RevisionStateVersioned || status.ConfigRevision == nil || *status.ConfigRevision != 7 {
		t.Fatalf("revision = (%s,%v), want versioned 7", status.RevisionState, status.ConfigRevision)
	}
	assertJSONValueEqual(t, "fsck", status.Peer["id"])
	assertJSONValueEqual(t, map[string]any{
		"keep":  true,
		"items": []any{map[string]any{"keep": "item"}},
	}, status.Peer["service_metadata"])
	assertJSONValueEqual(t, map[string]any{
		"future_proof": map[string]any{"keep": true},
	}, status.Peer["proof"])
	if _, ok := status.Peer["shared_key"]; ok {
		t.Fatal("read returned peer shared_key")
	}
	if _, ok := status.Peer["private_key"]; ok {
		t.Fatal("read returned peer private_key")
	}
	if _, ok := status.Peer["accept_proof"]; ok {
		t.Fatal("read returned peer accept_proof")
	}
	if _, ok := status.Peer["connect_proof"]; ok {
		t.Fatal("read returned peer connect_proof")
	}
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("secret")) || bytes.Contains(data, []byte("proof-token")) || bytes.Contains(data, []byte("sync_ed25519")) || bytes.Contains(data, []byte("known_hosts")) {
		t.Fatalf("read response leaked secret/config material: %s", data)
	}
}

func TestRedactRawPeerRedactsScalarProof(t *testing.T) {
	peer := redactRawPeer(map[string]json.RawMessage{
		"id":    json.RawMessage(`"fsck"`),
		"proof": json.RawMessage(`"raw-proof-token"`),
	})

	if _, ok := peer["proof"]; ok {
		t.Fatal("read returned scalar proof")
	}
	data, err := json.Marshal(peer)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("raw-proof-token")) {
		t.Fatalf("read response leaked scalar proof: %s", data)
	}
}

func TestRedactRawPeerRedactsProofArray(t *testing.T) {
	peer := redactRawPeer(map[string]json.RawMessage{
		"id":    json.RawMessage(`"fsck"`),
		"proof": json.RawMessage(`["raw-proof-token",{"keep":true,"shared_key":"secret"},[{"keep":"nested","nonce":"n"}]]`),
	})

	assertJSONValueEqual(t, []any{
		map[string]any{"keep": true},
		[]any{map[string]any{"keep": "nested"}},
	}, peer["proof"])
	data, err := json.Marshal(peer)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("raw-proof-token")) || bytes.Contains(data, []byte("secret")) || bytes.Contains(data, []byte(`"n"`)) {
		t.Fatalf("read response leaked proof array secret: %s", data)
	}
}

func TestRedactRawPeerOmitsUnknownTopLevelScalars(t *testing.T) {
	peer := redactRawPeer(map[string]json.RawMessage{
		"id":            json.RawMessage(`"fsck"`),
		"future_scalar": json.RawMessage(`"future-secret-value"`),
		"auth_token":    json.RawMessage(`"future-auth-token"`),
		"future_peer":   json.RawMessage(`{"keep":true,"auth_token":"nested-token","nested":[{"keep":"item","recovery_seed":"seed"}]}`),
	})

	if _, ok := peer["future_scalar"]; ok {
		t.Fatal("read returned unknown top-level scalar")
	}
	if _, ok := peer["auth_token"]; ok {
		t.Fatal("read returned top-level auth token")
	}
	assertJSONValueEqual(t, map[string]any{
		"keep":   true,
		"nested": []any{map[string]any{"keep": "item"}},
	}, peer["future_peer"])
	data, err := json.Marshal(peer)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("future-secret-value")) || bytes.Contains(data, []byte("future-auth-token")) || bytes.Contains(data, []byte("nested-token")) || bytes.Contains(data, []byte("seed")) {
		t.Fatalf("read response leaked unknown scalar secret: %s", data)
	}
}

func TestReadSSHPeerReturnsNotFoundForAbsentPeer(t *testing.T) {
	path := writeConfigForV2Test(t, `{
  "config_version": 2,
  "config_revision": 7,
  "shared_key": "secret",
  "hostname": "m4",
  "transport": "ssh",
  "ssh": {"peers": [{"id": "fsck", "enabled": true, "accept": true, "migration_state": "loopback_unprovisioned"}]}
}`)

	_, err := ReadSSHPeer(path, "missing")
	if err == nil || !strings.Contains(err.Error(), "ssh_peer_not_found: missing") {
		t.Fatalf("error = %v, want ssh_peer_not_found", err)
	}
}

func TestUpsertSSHPeerMergesScopedFieldsAndPreservesRawSSH(t *testing.T) {
	path := writeConfigForV2Test(t, `{
  "config_version": 2,
  "config_revision": 7,
  "shared_key": "secret",
  "listen": "127.0.0.1:7853",
  "hostname": "m4",
  "transport": "ssh",
  "max_history": 50,
  "future_top": {"keep": true},
  "ssh": {
    "sync_key": "~/.config/clipfan/ssh/sync_ed25519",
    "known_hosts": "~/.config/clipfan/ssh/known_hosts",
    "future_ssh": {"keep": true},
    "peers": [
      {
        "id": "fsck",
        "enabled": true,
        "accept": true,
        "connect": true,
        "persistent": true,
        "on_demand": false,
        "ssh_host": "FSCK.COM.",
        "ssh_user": "jesse",
        "ssh_port": 22,
        "install_path": "/home/jesse/.local/bin/clipfan",
        "gateway_path": "/home/jesse/.local/bin/clipfan",
        "migration_state": "loopback_unprovisioned",
        "shared_key": "peer-secret",
        "private_key": "peer-private",
        "proof": {"future_proof": {"keep": true}},
        "service_metadata": {"keep": true},
        "future_peer": {"keep": true}
      },
      {
        "id": "other",
        "enabled": true,
        "accept": true,
        "migration_state": "loopback_unprovisioned",
        "future_peer": {"keep": "other"}
      }
    ]
  }
}`)
	before := readJSONMap(t, path)

	status, err := upsertSSHPeerWithGate(path, true, "fsck", SSHPeerUpsertRequest{
		ExpectedConfigRevision: uint64Ptr(7),
		Peer: SSHPeerUpsertFields{
			ID:          stringPtr("fsck"),
			Enabled:     boolPtr(false),
			OnDemand:    boolPtr(true),
			SSHHost:     stringPtr("FSCK.COM."),
			SSHUser:     stringPtr("jesse"),
			SSHPort:     intPtr(22),
			InstallPath: stringPtr("/home/jesse/bin/clipfan"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.ConfigRevision == nil || *status.ConfigRevision != 8 {
		t.Fatalf("ConfigRevision = %v, want 8", status.ConfigRevision)
	}
	assertJSONValueEqual(t, "fsck.com", status.Peer["ssh_host"])
	assertJSONValueEqual(t, false, status.Peer["enabled"])
	assertJSONValueEqual(t, true, status.Peer["on_demand"])
	assertJSONValueEqual(t, map[string]any{"keep": true}, status.Peer["future_peer"])
	if _, ok := status.Peer["shared_key"]; ok {
		t.Fatal("upsert response returned peer shared_key")
	}
	if _, ok := status.Peer["private_key"]; ok {
		t.Fatal("upsert response returned peer private_key")
	}

	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 8)
	assertJSONValueEqual(t, before["shared_key"], after["shared_key"])
	assertJSONValueEqual(t, before["future_top"], after["future_top"])

	ssh := after["ssh"].(map[string]any)
	assertJSONValueEqual(t, map[string]any{"keep": true}, ssh["future_ssh"])
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	assertJSONValueEqual(t, ExpandLocalSSHPath("~/.config/clipfan/ssh/sync_ed25519", homeDir), ssh["sync_key"])
	assertJSONValueEqual(t, ExpandLocalSSHPath("~/.config/clipfan/ssh/known_hosts", homeDir), ssh["known_hosts"])
	peers := ssh["peers"].([]any)
	if len(peers) != 2 {
		t.Fatalf("peer count = %d, want 2", len(peers))
	}
	updated := peers[0].(map[string]any)
	other := peers[1].(map[string]any)
	assertJSONValueEqual(t, "fsck.com", updated["ssh_host"])
	assertJSONValueEqual(t, "/home/jesse/bin/clipfan", updated["install_path"])
	assertJSONValueEqual(t, "peer-secret", updated["shared_key"])
	assertJSONValueEqual(t, "peer-private", updated["private_key"])
	assertJSONValueEqual(t, before["ssh"].(map[string]any)["peers"].([]any)[0].(map[string]any)["proof"], updated["proof"])
	assertJSONValueEqual(t, map[string]any{"keep": true}, updated["service_metadata"])
	assertJSONValueEqual(t, map[string]any{"keep": true}, updated["future_peer"])
	assertJSONValueEqual(t, before["ssh"].(map[string]any)["peers"].([]any)[1], other)
}

func TestUpsertSSHPeerCreatesLoopbackPeerAndIncrementsRevision(t *testing.T) {
	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"k","hostname":"m4","transport":"ssh","max_history":50}`)

	status, err := upsertSSHPeerWithGate(path, true, "fsck", SSHPeerUpsertRequest{
		ExpectedConfigRevision: uint64Ptr(7),
		Peer: SSHPeerUpsertFields{
			ID:             stringPtr("fsck"),
			Enabled:        boolPtr(true),
			Accept:         boolPtr(false),
			Connect:        boolPtr(true),
			Persistent:     boolPtr(true),
			OnDemand:       boolPtr(true),
			SSHHost:        stringPtr("FSCK.COM."),
			SSHUser:        stringPtr("jesse"),
			SSHPort:        intPtr(22),
			InstallPath:    stringPtr("/home/jesse/.local/bin/clipfan"),
			GatewayPath:    stringPtr("/home/jesse/.local/bin/clipfan"),
			MigrationState: migrationStatePtr(MigrationStateLoopbackUnprovisioned),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertJSONValueEqual(t, "fsck.com", status.Peer["ssh_host"])
	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 8)
}

func TestUpsertSSHPeerRejectsIncompleteCreatesWithoutWriting(t *testing.T) {
	cases := []struct {
		name string
		req  SSHPeerUpsertRequest
		code string
	}{
		{
			name: "missing migration state",
			req: SSHPeerUpsertRequest{Peer: SSHPeerUpsertFields{
				ID:      stringPtr("fsck"),
				Enabled: boolPtr(true),
				Accept:  boolPtr(true),
				Connect: boolPtr(false),
			}},
			code: "ssh_peer_create_requires_loopback_unprovisioned",
		},
		{
			name: "missing explicit shape",
			req: SSHPeerUpsertRequest{Peer: SSHPeerUpsertFields{
				ID:             stringPtr("fsck"),
				MigrationState: migrationStatePtr(MigrationStateLoopbackUnprovisioned),
			}},
			code: "ssh_peer_create_requires_enabled",
		},
		{
			name: "missing direction",
			req: SSHPeerUpsertRequest{Peer: SSHPeerUpsertFields{
				ID:             stringPtr("fsck"),
				Enabled:        boolPtr(true),
				Accept:         boolPtr(false),
				Connect:        boolPtr(false),
				MigrationState: migrationStatePtr(MigrationStateLoopbackUnprovisioned),
			}},
			code: "ssh_peer_create_requires_direction",
		},
		{
			name: "missing outbound fields",
			req: SSHPeerUpsertRequest{Peer: SSHPeerUpsertFields{
				ID:             stringPtr("fsck"),
				Enabled:        boolPtr(true),
				Accept:         boolPtr(false),
				Connect:        boolPtr(true),
				MigrationState: migrationStatePtr(MigrationStateLoopbackUnprovisioned),
			}},
			code: "ssh_peer_create_requires_outbound_mode",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"k","hostname":"m4","transport":"ssh","max_history":50}`)
			before := readJSONMap(t, path)
			tc.req.ExpectedConfigRevision = uint64Ptr(7)
			_, err := upsertSSHPeerWithGate(path, true, "fsck", tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
			after := readJSONMap(t, path)
			assertJSONValueEqual(t, before, after)
		})
	}
}

func TestUpsertSSHPeerRejectsInvalidRequestsWithoutWriting(t *testing.T) {
	cases := []struct {
		name string
		req  SSHPeerUpsertRequest
		code string
	}{
		{
			name: "peer id mismatch",
			req: SSHPeerUpsertRequest{Peer: SSHPeerUpsertFields{
				ID: stringPtr("other"),
			}},
			code: "ssh_peer_id_mismatch",
		},
		{
			name: "secret peer field",
			req: SSHPeerUpsertRequest{Peer: SSHPeerUpsertFields{
				ID:        stringPtr("fsck"),
				SharedKey: stringPtr("secret"),
			}},
			code: "ssh_peer_secret_field_not_allowed",
		},
		{
			name: "migration state change",
			req: SSHPeerUpsertRequest{Peer: SSHPeerUpsertFields{
				ID:             stringPtr("fsck"),
				MigrationState: migrationStatePtr(MigrationStateSSHMaterialStaged),
			}},
			code: "ssh_peer_migration_state_change_not_allowed",
		},
		{
			name: "invalid host id",
			req: SSHPeerUpsertRequest{Peer: SSHPeerUpsertFields{
				ID: stringPtr("-bad"),
			}},
			code: "invalid_ssh_peer_id",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfigForV2Test(t, `{
  "config_version": 2,
  "config_revision": 7,
  "shared_key": "k",
  "hostname": "m4",
  "transport": "ssh",
  "ssh": {"peers": [{"id":"fsck","enabled":true,"accept":true,"migration_state":"loopback_unprovisioned","future_peer":{"keep":true}}]}
}`)
			before := readJSONMap(t, path)
			tc.req.ExpectedConfigRevision = uint64Ptr(7)
			_, err := upsertSSHPeerWithGate(path, true, "fsck", tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
			after := readJSONMap(t, path)
			assertJSONValueEqual(t, before, after)
		})
	}
}

func TestUpsertSSHPeerRejectsStaleRevisionWithoutWriting(t *testing.T) {
	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"k","hostname":"m4","transport":"ssh","ssh":{"peers":[{"id":"fsck","enabled":true,"accept":true,"migration_state":"loopback_unprovisioned"}]}}`)
	before := readJSONMap(t, path)

	_, err := upsertSSHPeerWithGate(path, true, "fsck", SSHPeerUpsertRequest{
		ExpectedConfigRevision: uint64Ptr(6),
		Peer:                   SSHPeerUpsertFields{ID: stringPtr("fsck"), Enabled: boolPtr(false)},
	})
	if !errors.Is(err, ErrConfigRevisionConflict) {
		t.Fatalf("error = %v, want ErrConfigRevisionConflict", err)
	}
	after := readJSONMap(t, path)
	assertJSONValueEqual(t, before, after)
}

func TestDecodeSSHPeerProofPatchRequestDecodesBothDirections(t *testing.T) {
	body := `{
  "expected_config_revision": 7,
  "accept_proof": {
    "key_id": "accept-key-1",
    "gateway_path": "/home/jesse/.local/bin/clipfan",
    "verified_at": "2026-06-02T12:34:56Z",
    "verified_by": "local_file"
  },
  "connect_proof": {
    "key_id": "connect-key-1",
    "gateway_path": "/home/jesse/bin/clipfan",
    "verified_at": "2026-06-02T12:35:56Z",
    "verified_by": "regular_ssh"
  }
}`

	req, err := DecodeSSHPeerProofPatchRequest(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	requireUint64Ptr(t, "expected_config_revision", req.ExpectedConfigRevision, 7)
	if req.AcceptProof == nil {
		t.Fatal("AcceptProof = nil")
	}
	if req.ConnectProof == nil {
		t.Fatal("ConnectProof = nil")
	}
	assertJSONValueEqual(t, SSHPeerDirectionalProofPatch{
		KeyID:       "accept-key-1",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		VerifiedAt:  "2026-06-02T12:34:56Z",
		VerifiedBy:  ProofVerifiedByLocalFile,
	}, *req.AcceptProof)
	assertJSONValueEqual(t, SSHPeerDirectionalProofPatch{
		KeyID:       "connect-key-1",
		GatewayPath: "/home/jesse/bin/clipfan",
		VerifiedAt:  "2026-06-02T12:35:56Z",
		VerifiedBy:  ProofVerifiedByRegularSSH,
	}, *req.ConnectProof)
}

func TestDecodeSSHPeerProofPatchRequestRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name string
		body string
		code string
	}{
		{
			name: "unknown wrapper field",
			body: `{"expected_config_revision":7,"accept_proof":{"key_id":"accept-key-1","gateway_path":"/bin/clipfan","verified_at":"2026-06-02T12:34:56Z","verified_by":"local_file"},"future":true}`,
			code: "unknown_field: future",
		},
		{
			name: "unknown proof field",
			body: `{"expected_config_revision":7,"accept_proof":{"key_id":"accept-key-1","gateway_path":"/bin/clipfan","verified_at":"2026-06-02T12:34:56Z","verified_by":"local_file","future":true}}`,
			code: "unknown_field: accept_proof.future",
		},
		{
			name: "missing proof field",
			body: `{"expected_config_revision":7,"accept_proof":{"key_id":"accept-key-1","gateway_path":"/bin/clipfan","verified_by":"local_file"}}`,
			code: "missing_ssh_peer_proof_patch_field: accept_proof.verified_at",
		},
		{
			name: "null proof field",
			body: `{"expected_config_revision":7,"accept_proof":{"key_id":"accept-key-1","gateway_path":"/bin/clipfan","verified_at":null,"verified_by":"local_file"}}`,
			code: "missing_ssh_peer_proof_patch_field: accept_proof.verified_at",
		},
		{
			name: "invalid proof scalar type",
			body: `{"expected_config_revision":7,"accept_proof":{"key_id":"accept-key-1","gateway_path":"/bin/clipfan","verified_at":"2026-06-02T12:34:56Z","verified_by":false}}`,
			code: "invalid_ssh_peer_proof_patch_field: accept_proof.verified_by",
		},
		{
			name: "invalid proof value",
			body: `{"expected_config_revision":7,"accept_proof":{"key_id":"bad","gateway_path":"/bin/clipfan","verified_at":"2026-06-02T12:34:56Z","verified_by":"local_file"}}`,
			code: "invalid_ssh_peer_proof_patch_field: accept_proof: invalid_proof_key_id",
		},
		{
			name: "invalid verified_by value",
			body: `{"expected_config_revision":7,"accept_proof":{"key_id":"accept-key-1","gateway_path":"/bin/clipfan","verified_at":"2026-06-02T12:34:56Z","verified_by":"bogus"}}`,
			code: "invalid_ssh_peer_proof_patch_field: accept_proof: invalid_proof_verified_by",
		},
		{
			name: "empty patch",
			body: `{"expected_config_revision":7}`,
			code: "ssh_peer_proof_patch_empty",
		},
		{
			name: "null proof object",
			body: `{"expected_config_revision":7,"accept_proof":null}`,
			code: "invalid_ssh_peer_proof_patch_field: accept_proof",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeSSHPeerProofPatchRequest(strings.NewReader(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
		})
	}
}

func TestDecodeSSHPeerProofPatchRequestRejectsZeroRevisionAndTrailingJSON(t *testing.T) {
	_, err := DecodeSSHPeerProofPatchRequest(strings.NewReader(`{"expected_config_revision":0,"accept_proof":{"key_id":"accept-key-1","gateway_path":"/bin/clipfan","verified_at":"2026-06-02T12:34:56Z","verified_by":"local_file"}}`))
	if !errors.Is(err, ErrConfigRevisionConflict) {
		t.Fatalf("zero revision error = %v, want ErrConfigRevisionConflict", err)
	}

	_, err = DecodeSSHPeerProofPatchRequest(strings.NewReader(`{"expected_config_revision":7,"accept_proof":{"key_id":"accept-key-1","gateway_path":"/bin/clipfan","verified_at":"2026-06-02T12:34:56Z","verified_by":"local_file"}} {}`))
	if err == nil || !strings.Contains(err.Error(), "malformed_ssh_peer_proof_patch_request: trailing data") {
		t.Fatalf("trailing JSON error = %v, want malformed trailing data", err)
	}
}

func TestPatchSSHPeerProofMergesBothProofsAndPreservesRawConfig(t *testing.T) {
	path := writeConfigForV2Test(t, `{
  "config_version": 2,
  "config_revision": 7,
  "shared_key": "secret",
  "listen": "127.0.0.1:7853",
  "hostname": "m4",
  "transport": "ssh",
  "max_history": 50,
  "future_top": {"keep": true},
  "ssh": {
    "sync_key": "/Users/jesse/.config/clipfan/ssh/sync_ed25519",
    "known_hosts": "/Users/jesse/.config/clipfan/ssh/known_hosts",
    "future_ssh": {"keep": true},
    "peers": [
      {
        "id": "fsck",
        "enabled": true,
        "accept": true,
        "connect": true,
        "persistent": true,
        "on_demand": false,
        "ssh_host": "fsck.com",
        "ssh_user": "jesse",
        "ssh_port": 22,
        "install_path": "/home/jesse/.local/bin/clipfan",
        "gateway_path": "/home/jesse/.local/bin/clipfan",
        "migration_state": "loopback_unprovisioned",
        "shared_key": "peer-secret",
        "private_key": "peer-private",
        "proof": {
          "future_proof": {"keep": true},
          "accept_key_id": "oldaccept",
          "accept_gateway_path": "/old/accept",
          "accept_verified_at": "2026-06-02T00:00:00Z",
          "accept_verified_by": "local_file",
          "connect_key_id": "oldconnect",
          "connect_gateway_path": "/old/connect",
          "connect_verified_at": "2026-06-02T00:00:00Z",
          "connect_verified_by": "regular_ssh"
        },
        "service_metadata": {"keep": true}
      },
      {
        "id": "other",
        "enabled": true,
        "accept": true,
        "migration_state": "loopback_unprovisioned",
        "future_peer": {"keep": "other"}
      }
    ]
  }
}`)
	before := readJSONMap(t, path)

	status, err := patchSSHPeerProofWithGate(path, true, "fsck", SSHPeerProofPatchRequest{
		ExpectedConfigRevision: uint64Ptr(7),
		AcceptProof: &SSHPeerDirectionalProofPatch{
			KeyID:       "accept-key-1",
			GatewayPath: "/home/jesse/.local/bin/clipfan",
			VerifiedAt:  "2026-06-02T12:34:56Z",
			VerifiedBy:  ProofVerifiedByLocalFile,
		},
		ConnectProof: &SSHPeerDirectionalProofPatch{
			KeyID:       "connect-key-1",
			GatewayPath: "/home/jesse/bin/clipfan",
			VerifiedAt:  "2026-06-02T12:35:56Z",
			VerifiedBy:  ProofVerifiedByRegularSSH,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.ConfigVersion == nil || *status.ConfigVersion != 2 {
		t.Fatalf("ConfigVersion = %v, want 2", status.ConfigVersion)
	}
	if status.ConfigRevision == nil || *status.ConfigRevision != 8 {
		t.Fatalf("ConfigRevision = %v, want 8", status.ConfigRevision)
	}
	if status.RevisionState != RevisionStateVersioned {
		t.Fatalf("RevisionState = %s, want versioned", status.RevisionState)
	}
	if _, ok := status.Peer["shared_key"]; ok {
		t.Fatal("patch response returned peer shared_key")
	}
	if _, ok := status.Peer["private_key"]; ok {
		t.Fatal("patch response returned peer private_key")
	}
	assertJSONValueEqual(t, map[string]any{
		"future_proof":         map[string]any{"keep": true},
		"accept_key_id":        "accept-key-1",
		"accept_gateway_path":  "/home/jesse/.local/bin/clipfan",
		"accept_verified_at":   "2026-06-02T12:34:56Z",
		"accept_verified_by":   ProofVerifiedByLocalFile,
		"connect_key_id":       "connect-key-1",
		"connect_gateway_path": "/home/jesse/bin/clipfan",
		"connect_verified_at":  "2026-06-02T12:35:56Z",
		"connect_verified_by":  ProofVerifiedByRegularSSH,
	}, status.Peer["proof"])

	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_version"], 2)
	assertJSONNumber(t, after["config_revision"], 8)
	assertJSONValueEqual(t, before["shared_key"], after["shared_key"])
	assertJSONValueEqual(t, before["future_top"], after["future_top"])
	ssh := after["ssh"].(map[string]any)
	assertJSONValueEqual(t, before["ssh"].(map[string]any)["sync_key"], ssh["sync_key"])
	assertJSONValueEqual(t, before["ssh"].(map[string]any)["known_hosts"], ssh["known_hosts"])
	assertJSONValueEqual(t, before["ssh"].(map[string]any)["future_ssh"], ssh["future_ssh"])
	peers := ssh["peers"].([]any)
	updated := peers[0].(map[string]any)
	other := peers[1].(map[string]any)
	assertJSONValueEqual(t, "peer-secret", updated["shared_key"])
	assertJSONValueEqual(t, "peer-private", updated["private_key"])
	assertJSONValueEqual(t, map[string]any{"keep": true}, updated["service_metadata"])
	assertJSONValueEqual(t, status.Peer["proof"], updated["proof"])
	assertJSONValueEqual(t, before["ssh"].(map[string]any)["peers"].([]any)[1], other)
}

func TestPatchSSHPeerProofOneDirectionPreservesOtherDirection(t *testing.T) {
	path := writeConfigForV2Test(t, `{
  "config_version": 2,
  "config_revision": 7,
  "shared_key": "k",
  "hostname": "m4",
  "transport": "ssh",
  "ssh": {"peers": [{
    "id": "fsck",
    "enabled": true,
    "accept": true,
    "connect": true,
    "persistent": true,
    "ssh_host": "fsck.com",
    "ssh_user": "jesse",
    "ssh_port": 22,
    "install_path": "/home/jesse/.local/bin/clipfan",
    "gateway_path": "/home/jesse/.local/bin/clipfan",
    "migration_state": "loopback_unprovisioned",
    "proof": {
      "accept_key_id": "oldaccept",
      "accept_gateway_path": "/old/accept",
      "accept_verified_at": "2026-06-02T00:00:00Z",
      "accept_verified_by": "local_file",
      "connect_key_id": "oldconnect",
      "connect_gateway_path": "/old/connect",
      "connect_verified_at": "2026-06-02T00:00:00Z",
      "connect_verified_by": "regular_ssh",
      "future_proof": {"keep": true}
    }
  }]}
}`)
	before := readJSONMap(t, path)
	beforeProof := before["ssh"].(map[string]any)["peers"].([]any)[0].(map[string]any)["proof"].(map[string]any)

	_, err := patchSSHPeerProofWithGate(path, true, "fsck", SSHPeerProofPatchRequest{
		ExpectedConfigRevision: uint64Ptr(7),
		ConnectProof: &SSHPeerDirectionalProofPatch{
			KeyID:       "connect-key-1",
			GatewayPath: "/home/jesse/bin/clipfan",
			VerifiedAt:  "2026-06-02T12:35:56Z",
			VerifiedBy:  ProofVerifiedByRegularSSH,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	after := readJSONMap(t, path)
	proof := after["ssh"].(map[string]any)["peers"].([]any)[0].(map[string]any)["proof"].(map[string]any)
	assertJSONValueEqual(t, beforeProof["accept_key_id"], proof["accept_key_id"])
	assertJSONValueEqual(t, beforeProof["accept_gateway_path"], proof["accept_gateway_path"])
	assertJSONValueEqual(t, beforeProof["accept_verified_at"], proof["accept_verified_at"])
	assertJSONValueEqual(t, beforeProof["accept_verified_by"], proof["accept_verified_by"])
	assertJSONValueEqual(t, beforeProof["future_proof"], proof["future_proof"])
	assertJSONValueEqual(t, "connect-key-1", proof["connect_key_id"])
	assertJSONValueEqual(t, "/home/jesse/bin/clipfan", proof["connect_gateway_path"])
}

func TestPatchSSHPeerProofAcceptOnlyLeavesConnectProofAbsent(t *testing.T) {
	path := writeConfigForV2Test(t, proofPatchBaseConfig())

	status, err := patchSSHPeerProofWithGate(path, true, "fsck", validAcceptProofPatchRequest(7))
	if err != nil {
		t.Fatal(err)
	}
	if status.ConfigRevision == nil || *status.ConfigRevision != 8 {
		t.Fatalf("ConfigRevision = %v, want 8", status.ConfigRevision)
	}
	assertJSONValueEqual(t, map[string]any{
		"accept_key_id":       "accept-key-1",
		"accept_gateway_path": "/home/jesse/.local/bin/clipfan",
		"accept_verified_at":  "2026-06-02T12:34:56Z",
		"accept_verified_by":  ProofVerifiedByLocalFile,
	}, status.Peer["proof"])

	after := readJSONMap(t, path)
	proof := after["ssh"].(map[string]any)["peers"].([]any)[0].(map[string]any)["proof"].(map[string]any)
	assertJSONValueEqual(t, status.Peer["proof"], proof)
	for _, key := range []string{"connect_key_id", "connect_gateway_path", "connect_verified_at", "connect_verified_by"} {
		if _, ok := proof[key]; ok {
			t.Fatalf("connect proof key %s was written: %#v", key, proof)
		}
	}
}

func TestPatchSSHPeerProofRejectsGateDisabledWithoutWriting(t *testing.T) {
	path := writeConfigForV2Test(t, proofPatchBaseConfig())
	before := readJSONMap(t, path)

	_, err := patchSSHPeerProofWithGate(path, false, "fsck", validAcceptProofPatchRequest(7))
	if !errors.Is(err, ErrConfigV2WritesDisabled) {
		t.Fatalf("error = %v, want ErrConfigV2WritesDisabled", err)
	}
	after := readJSONMap(t, path)
	assertJSONValueEqual(t, before, after)
}

func TestPatchSSHPeerProofRejectsStaleRevisionWithoutWriting(t *testing.T) {
	path := writeConfigForV2Test(t, proofPatchBaseConfig())
	before := readJSONMap(t, path)

	_, err := patchSSHPeerProofWithGate(path, true, "fsck", validAcceptProofPatchRequest(6))
	if !errors.Is(err, ErrConfigRevisionConflict) {
		t.Fatalf("error = %v, want ErrConfigRevisionConflict", err)
	}
	after := readJSONMap(t, path)
	assertJSONValueEqual(t, before, after)
}

func TestPatchSSHPeerProofRejectsInvalidDirectProofWithoutWriting(t *testing.T) {
	path := writeConfigForV2Test(t, proofPatchBaseConfig())
	before := readJSONMap(t, path)

	req := validAcceptProofPatchRequest(7)
	req.AcceptProof.VerifiedBy = "bogus"
	_, err := patchSSHPeerProofWithGate(path, true, "fsck", req)
	if err == nil || !strings.Contains(err.Error(), "invalid_proof_verified_by") {
		t.Fatalf("error = %v, want invalid_proof_verified_by", err)
	}
	after := readJSONMap(t, path)
	assertJSONValueEqual(t, before, after)
}

func TestPatchSSHPeerProofRejectsProofMismatchWithoutWriting(t *testing.T) {
	cases := []struct {
		name string
		body string
		req  SSHPeerProofPatchRequest
	}{
		{
			name: "accept false",
			body: strings.Replace(proofPatchBaseConfig(), `"accept": true`, `"accept": false`, 1),
			req:  validAcceptProofPatchRequest(7),
		},
		{
			name: "accept disabled",
			body: strings.Replace(proofPatchBaseConfig(), `"enabled": true`, `"enabled": false`, 1),
			req:  validAcceptProofPatchRequest(7),
		},
		{
			name: "connect false",
			body: strings.Replace(
				strings.Replace(
					strings.Replace(proofPatchBaseConfig(), `"connect": true`, `"connect": false`, 1),
					`"persistent": true`, `"persistent": false`, 1,
				),
				`"ssh_port": 22`, `"ssh_port": 0`, 1,
			),
			req: SSHPeerProofPatchRequest{
				ExpectedConfigRevision: uint64Ptr(7),
				ConnectProof: &SSHPeerDirectionalProofPatch{
					KeyID:       "connect-key-1",
					GatewayPath: "/home/jesse/bin/clipfan",
					VerifiedAt:  "2026-06-02T12:35:56Z",
					VerifiedBy:  ProofVerifiedByRegularSSH,
				},
			},
		},
		{
			name: "connect disabled",
			body: strings.Replace(proofPatchBaseConfig(), `"enabled": true`, `"enabled": false`, 1),
			req: SSHPeerProofPatchRequest{
				ExpectedConfigRevision: uint64Ptr(7),
				ConnectProof: &SSHPeerDirectionalProofPatch{
					KeyID:       "connect-key-1",
					GatewayPath: "/home/jesse/bin/clipfan",
					VerifiedAt:  "2026-06-02T12:35:56Z",
					VerifiedBy:  ProofVerifiedByRegularSSH,
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfigForV2Test(t, tc.body)
			before := readJSONMap(t, path)
			_, err := patchSSHPeerProofWithGate(path, true, "fsck", tc.req)
			if err == nil || !strings.Contains(err.Error(), "proof_mismatch") {
				t.Fatalf("error = %v, want proof_mismatch", err)
			}
			after := readJSONMap(t, path)
			assertJSONValueEqual(t, before, after)
		})
	}
}

func TestPatchSSHPeerProofRejectsMissingPeerWithoutWriting(t *testing.T) {
	path := writeConfigForV2Test(t, proofPatchBaseConfig())
	before := readJSONMap(t, path)

	_, err := patchSSHPeerProofWithGate(path, true, "missing", validAcceptProofPatchRequest(7))
	if err == nil || !strings.Contains(err.Error(), "ssh_peer_not_found: missing") {
		t.Fatalf("error = %v, want ssh_peer_not_found", err)
	}
	after := readJSONMap(t, path)
	assertJSONValueEqual(t, before, after)
}

func TestDecodeSSHPeerTransitionRequestDecodesProof(t *testing.T) {
	body := `{
  "expected_config_revision": 7,
  "from_state": "loopback_unprovisioned",
  "to_state": "provision_failed",
  "reason": "provision_failed",
  "log_id": "log-1",
  "failed_phase": "host_key_confirmation",
  "remote_secret_absence_proof": {
    "failed_phase": "host_key_confirmation",
    "secret_write_command_spawned": false,
    "absence_verified_by": "local_config_scan",
    "verified_at": "2026-06-02T12:34:56Z",
    "remote_config_revision": 12,
    "log_id": "log-1"
  }
}`

	req, err := DecodeSSHPeerTransitionRequest(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	requireUint64Ptr(t, "expected_config_revision", req.ExpectedConfigRevision, 7)
	if req.FromState != MigrationStateLoopbackUnprovisioned || req.ToState != MigrationStateProvisionFailed {
		t.Fatalf("states = %s -> %s", req.FromState, req.ToState)
	}
	requireStringPtr(t, "failed_phase", req.FailedPhase, "host_key_confirmation")
	if req.RemoteSecretAbsenceProof == nil {
		t.Fatal("RemoteSecretAbsenceProof = nil")
	}
	requireUint64Ptr(t, "remote_config_revision", req.RemoteSecretAbsenceProof.RemoteConfigRevision, 12)
	if req.RemoteSecretAbsenceProof.SecretWriteCommandSpawned {
		t.Fatal("SecretWriteCommandSpawned = true, want false")
	}
}

func TestDecodeSSHPeerTransitionRequestRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name string
		body string
		code string
	}{
		{
			name: "unknown wrapper field",
			body: `{"expected_config_revision":7,"from_state":"loopback_unprovisioned","to_state":"ssh_material_staged","reason":"material_staged","log_id":"log-1","future":true}`,
			code: "unknown_field: future",
		},
		{
			name: "missing revision",
			body: `{"from_state":"loopback_unprovisioned","to_state":"ssh_material_staged","reason":"material_staged","log_id":"log-1"}`,
			code: "missing_ssh_peer_transition_field: expected_config_revision",
		},
		{
			name: "missing from state",
			body: `{"expected_config_revision":7,"to_state":"ssh_material_staged","reason":"material_staged","log_id":"log-1"}`,
			code: "missing_ssh_peer_transition_field: from_state",
		},
		{
			name: "missing to state",
			body: `{"expected_config_revision":7,"from_state":"loopback_unprovisioned","reason":"material_staged","log_id":"log-1"}`,
			code: "missing_ssh_peer_transition_field: to_state",
		},
		{
			name: "missing reason",
			body: `{"expected_config_revision":7,"from_state":"loopback_unprovisioned","to_state":"ssh_material_staged","log_id":"log-1"}`,
			code: "missing_ssh_peer_transition_field: reason",
		},
		{
			name: "missing log id",
			body: `{"expected_config_revision":7,"from_state":"loopback_unprovisioned","to_state":"ssh_material_staged","reason":"material_staged"}`,
			code: "missing_ssh_peer_transition_field: log_id",
		},
		{
			name: "empty reason",
			body: `{"expected_config_revision":7,"from_state":"loopback_unprovisioned","to_state":"ssh_material_staged","reason":" ","log_id":"log-1"}`,
			code: "invalid_ssh_peer_transition_field: reason",
		},
		{
			name: "unknown proof field",
			body: `{"expected_config_revision":7,"from_state":"loopback_unprovisioned","to_state":"provision_failed","reason":"provision_failed","log_id":"log-1","failed_phase":"host_key_confirmation","remote_secret_absence_proof":{"failed_phase":"host_key_confirmation","secret_write_command_spawned":false,"absence_verified_by":"local_config_scan","verified_at":"2026-06-02T12:34:56Z","log_id":"log-1","future":true}}`,
			code: "unknown_field: remote_secret_absence_proof.future",
		},
		{
			name: "missing proof field",
			body: `{"expected_config_revision":7,"from_state":"loopback_unprovisioned","to_state":"provision_failed","reason":"provision_failed","log_id":"log-1","failed_phase":"host_key_confirmation","remote_secret_absence_proof":{"failed_phase":"host_key_confirmation","secret_write_command_spawned":false,"absence_verified_by":"local_config_scan","log_id":"log-1"}}`,
			code: "missing_ssh_peer_transition_field: remote_secret_absence_proof.verified_at",
		},
		{
			name: "invalid remote config revision",
			body: `{"expected_config_revision":7,"from_state":"loopback_unprovisioned","to_state":"provision_failed","reason":"provision_failed","log_id":"log-1","failed_phase":"host_key_confirmation","remote_secret_absence_proof":{"failed_phase":"host_key_confirmation","secret_write_command_spawned":false,"absence_verified_by":"local_config_scan","verified_at":"2026-06-02T12:34:56Z","remote_config_revision":"nope","log_id":"log-1"}}`,
			code: "invalid_ssh_peer_transition_field: remote_secret_absence_proof.remote_config_revision",
		},
		{
			name: "trailing json",
			body: `{"expected_config_revision":7,"from_state":"loopback_unprovisioned","to_state":"ssh_material_staged","reason":"material_staged","log_id":"log-1"} {}`,
			code: "malformed_ssh_peer_transition_request: trailing data",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeSSHPeerTransitionRequest(strings.NewReader(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
		})
	}

	_, err := DecodeSSHPeerTransitionRequest(strings.NewReader(`{"expected_config_revision":0,"from_state":"loopback_unprovisioned","to_state":"ssh_material_staged","reason":"material_staged","log_id":"log-1"}`))
	if !errors.Is(err, ErrConfigRevisionConflict) {
		t.Fatalf("zero revision error = %v, want ErrConfigRevisionConflict", err)
	}
}

func TestTransitionSSHPeerAcceptsLegalTransitionsAndWritesAudit(t *testing.T) {
	cases := []struct {
		name              string
		from              MigrationState
		to                MigrationState
		req               SSHPeerTransitionRequest
		includeProof      bool
		expectProofClear  bool
		expectSecretClear bool
	}{
		{
			name:         "loopback to staged",
			from:         MigrationStateLoopbackUnprovisioned,
			to:           MigrationStateSSHMaterialStaged,
			req:          validTransitionRequest(7, MigrationStateLoopbackUnprovisioned, MigrationStateSSHMaterialStaged, "material_staged"),
			includeProof: true,
		},
		{
			name: "loopback to provision failed",
			from: MigrationStateLoopbackUnprovisioned,
			to:   MigrationStateProvisionFailed,
			req:  validProvisionFailedTransitionRequest(7, MigrationStateLoopbackUnprovisioned, false, "host_key_confirmation"),
		},
		{
			name:              "provision failed to loopback",
			from:              MigrationStateProvisionFailed,
			to:                MigrationStateLoopbackUnprovisioned,
			req:               validTransitionRequest(7, MigrationStateProvisionFailed, MigrationStateLoopbackUnprovisioned, "retry_progress"),
			includeProof:      true,
			expectProofClear:  true,
			expectSecretClear: true,
		},
		{
			name:         "provision failed to staged",
			from:         MigrationStateProvisionFailed,
			to:           MigrationStateSSHMaterialStaged,
			req:          validTransitionRequest(7, MigrationStateProvisionFailed, MigrationStateSSHMaterialStaged, "retry_progress"),
			includeProof: true,
		},
		{
			name:         "staged to shared key written",
			from:         MigrationStateSSHMaterialStaged,
			to:           MigrationStateSharedKeyWrittenUnverified,
			req:          validTransitionRequest(7, MigrationStateSSHMaterialStaged, MigrationStateSharedKeyWrittenUnverified, "remote_shared_key_written"),
			includeProof: true,
		},
		{
			name:         "staged to shared key unknown outcome",
			from:         MigrationStateSSHMaterialStaged,
			to:           MigrationStateSharedKeyWrittenUnverified,
			req:          validTransitionRequest(7, MigrationStateSSHMaterialStaged, MigrationStateSharedKeyWrittenUnverified, "secret_write_outcome_unknown"),
			includeProof: true,
		},
		{
			name:         "shared key written to ready",
			from:         MigrationStateSharedKeyWrittenUnverified,
			to:           MigrationStateSSHKeysReady,
			req:          validTransitionRequest(7, MigrationStateSharedKeyWrittenUnverified, MigrationStateSSHKeysReady, "gateway_version_verified"),
			includeProof: true,
		},
		{
			name:         "staged to ready after verified ssh material",
			from:         MigrationStateSSHMaterialStaged,
			to:           MigrationStateSSHKeysReady,
			req:          validTransitionRequest(7, MigrationStateSSHMaterialStaged, MigrationStateSSHKeysReady, "ssh_material_verified"),
			includeProof: true,
		},
		{
			name:         "shared key written to staged",
			from:         MigrationStateSharedKeyWrittenUnverified,
			to:           MigrationStateSSHMaterialStaged,
			req:          validTransitionRequest(7, MigrationStateSharedKeyWrittenUnverified, MigrationStateSSHMaterialStaged, "remote_shared_key_cleanup_verified"),
			includeProof: true,
		},
		{
			name:         "ready to staged",
			from:         MigrationStateSSHKeysReady,
			to:           MigrationStateSSHMaterialStaged,
			req:          validTransitionRequest(7, MigrationStateSSHKeysReady, MigrationStateSSHMaterialStaged, "remote_shared_key_cleanup_verified"),
			includeProof: true,
		},
		{
			name:              "ready to loopback",
			from:              MigrationStateSSHKeysReady,
			to:                MigrationStateLoopbackUnprovisioned,
			req:               validTransitionRequest(7, MigrationStateSSHKeysReady, MigrationStateLoopbackUnprovisioned, "identity_reset_prepared"),
			includeProof:      true,
			expectProofClear:  true,
			expectSecretClear: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfigForV2Test(t, transitionBaseConfig(tc.from, tc.includeProof))
			before := readJSONMap(t, path)

			status, err := transitionSSHPeerWithGate(path, true, "fsck", tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if status.ConfigRevision == nil || *status.ConfigRevision != 8 {
				t.Fatalf("ConfigRevision = %v, want 8", status.ConfigRevision)
			}
			assertJSONValueEqual(t, string(tc.to), status.Peer["migration_state"])
			statusData, err := json.Marshal(status.Peer)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(statusData, []byte("peer-secret")) || bytes.Contains(statusData, []byte("peer-private")) {
				t.Fatalf("transition response leaked secret material: %s", statusData)
			}
			if _, ok := status.Peer["shared_key"]; ok {
				t.Fatalf("transition response exposed shared_key: %#v", status.Peer)
			}
			if _, ok := status.Peer["private_key"]; ok {
				t.Fatalf("transition response exposed private_key: %#v", status.Peer)
			}

			after := readJSONMap(t, path)
			assertJSONNumber(t, after["config_revision"], 8)
			assertJSONValueEqual(t, before["shared_key"], after["shared_key"])
			assertJSONValueEqual(t, before["future_top"], after["future_top"])
			peer := after["ssh"].(map[string]any)["peers"].([]any)[0].(map[string]any)
			assertJSONValueEqual(t, string(tc.to), peer["migration_state"])
			if tc.expectSecretClear {
				if _, ok := peer["shared_key"]; ok {
					t.Fatalf("shared_key was not cleared: %#v", peer["shared_key"])
				}
				if _, ok := peer["private_key"]; ok {
					t.Fatalf("private_key was not cleared: %#v", peer["private_key"])
				}
			} else {
				assertJSONValueEqual(t, "peer-secret", peer["shared_key"])
				assertJSONValueEqual(t, "peer-private", peer["private_key"])
			}
			assertJSONValueEqual(t, map[string]any{"keep": true}, peer["future_peer"])
			if tc.expectProofClear {
				if _, ok := peer["proof"]; ok {
					t.Fatalf("proof was not cleared: %#v", peer["proof"])
				}
			}
			log := peer["migration_log"].([]any)
			if len(log) != 2 {
				t.Fatalf("migration_log len = %d, want 2", len(log))
			}
			entry := log[1].(map[string]any)
			assertJSONValueEqual(t, string(tc.from), entry["from_state"])
			assertJSONValueEqual(t, string(tc.to), entry["to_state"])
			assertJSONValueEqual(t, tc.req.Reason, entry["reason"])
			assertJSONValueEqual(t, tc.req.LogID, entry["log_id"])
			data, err := json.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte("peer-secret")) || bytes.Contains(data, []byte("peer-private")) {
				t.Fatalf("audit entry leaked secret material: %s", data)
			}
		})
	}
}

func TestTransitionSSHPeerLoopbackRecursivelyScrubsSecretLikeFields(t *testing.T) {
	body := strings.Replace(
		transitionBaseConfig(MigrationStateSSHKeysReady, true),
		`"future_peer": {"keep": true}`,
		`"future_peer": {"keep": true, "shared_key": "nested-secret", "nested": [{"keep": "item", "auth_token": "nested-token"}]}`,
		1,
	)
	path := writeConfigForV2Test(t, body)

	_, err := transitionSSHPeerWithGate(path, true, "fsck", validTransitionRequest(7, MigrationStateSSHKeysReady, MigrationStateLoopbackUnprovisioned, "identity_reset_prepared"))
	if err != nil {
		t.Fatal(err)
	}

	after := readJSONMap(t, path)
	peer := after["ssh"].(map[string]any)["peers"].([]any)[0].(map[string]any)
	future := peer["future_peer"].(map[string]any)
	assertJSONValueEqual(t, true, future["keep"])
	if _, ok := future["shared_key"]; ok {
		t.Fatalf("nested shared_key was not scrubbed: %#v", future)
	}
	nested := future["nested"].([]any)[0].(map[string]any)
	assertJSONValueEqual(t, "item", nested["keep"])
	if _, ok := nested["auth_token"]; ok {
		t.Fatalf("nested auth_token was not scrubbed: %#v", nested)
	}
	data, err := json.Marshal(peer)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("nested-secret")) || bytes.Contains(data, []byte("nested-token")) {
		t.Fatalf("loopback peer retained nested secret material: %s", data)
	}
}

func TestTransitionSSHPeerLoopbackPreservesAbsenceProofAuditHistory(t *testing.T) {
	body := strings.Replace(
		transitionBaseConfig(MigrationStateProvisionFailed, false),
		`"migration_log": [{"from_state":"initial","to_state":"provision_failed","reason":"existing","log_id":"existing-log"}]`,
		`"migration_log": [{"from_state":"loopback_unprovisioned","to_state":"provision_failed","reason":"provision_failed","log_id":"existing-log","remote_secret_absence_proof":{"failed_phase":"host_key_confirmation","secret_write_command_spawned":false,"absence_verified_by":"local_config_scan","verified_at":"2026-06-02T12:34:56Z","log_id":"existing-log"}}]`,
		1,
	)
	path := writeConfigForV2Test(t, body)

	_, err := transitionSSHPeerWithGate(path, true, "fsck", validTransitionRequest(7, MigrationStateProvisionFailed, MigrationStateLoopbackUnprovisioned, "retry_progress"))
	if err != nil {
		t.Fatal(err)
	}

	after := readJSONMap(t, path)
	peer := after["ssh"].(map[string]any)["peers"].([]any)[0].(map[string]any)
	log := peer["migration_log"].([]any)
	if len(log) != 2 {
		t.Fatalf("migration_log len = %d, want 2", len(log))
	}
	proof := log[0].(map[string]any)["remote_secret_absence_proof"].(map[string]any)
	assertJSONValueEqual(t, "host_key_confirmation", proof["failed_phase"])
	assertJSONValueEqual(t, false, proof["secret_write_command_spawned"])
	assertJSONValueEqual(t, "local_config_scan", proof["absence_verified_by"])
}

func TestDecodeSSHPeerDisableRequestRejectsInvalidInput(t *testing.T) {
	req, err := DecodeSSHPeerDisableRequest(strings.NewReader(`{"expected_config_revision":7,"reason":"user_disabled"}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.ExpectedConfigRevision == nil || *req.ExpectedConfigRevision != 7 || req.Reason != "user_disabled" {
		t.Fatalf("decoded disable request = %#v", req)
	}

	cases := []struct {
		name string
		body string
		code string
	}{
		{
			name: "unknown field",
			body: `{"expected_config_revision":7,"reason":"user_disabled","future":true}`,
			code: "unknown_field: future",
		},
		{
			name: "missing revision",
			body: `{"reason":"user_disabled"}`,
			code: "missing_ssh_peer_disable_field: expected_config_revision",
		},
		{
			name: "null revision",
			body: `{"expected_config_revision":null,"reason":"user_disabled"}`,
			code: "missing_ssh_peer_disable_field: expected_config_revision",
		},
		{
			name: "missing reason",
			body: `{"expected_config_revision":7}`,
			code: "missing_ssh_peer_disable_field: reason",
		},
		{
			name: "blank reason",
			body: `{"expected_config_revision":7,"reason":" "}`,
			code: "missing_ssh_peer_disable_field: reason",
		},
		{
			name: "unstable reason",
			body: `{"expected_config_revision":7,"reason":"user disabled"}`,
			code: "invalid_ssh_peer_disable_field: reason",
		},
		{
			name: "trailing data",
			body: `{"expected_config_revision":7,"reason":"user_disabled"} {}`,
			code: "malformed_ssh_peer_disable_request: trailing data",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeSSHPeerDisableRequest(strings.NewReader(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
		})
	}

	_, err = DecodeSSHPeerDisableRequest(strings.NewReader(`{"expected_config_revision":0,"reason":"user_disabled"}`))
	if !errors.Is(err, ErrConfigRevisionConflict) {
		t.Fatalf("zero revision error = %v, want ErrConfigRevisionConflict", err)
	}
}

func TestDecodeSSHPeerDeleteRequestRejectsInvalidInput(t *testing.T) {
	req, err := DecodeSSHPeerDeleteRequest(strings.NewReader(`{"expected_config_revision":7,"reason":"user_deleted","log_id":"peer-log-1780257600"}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.ExpectedConfigRevision == nil || *req.ExpectedConfigRevision != 7 || req.Reason != "user_deleted" || req.LogID != "peer-log-1780257600" {
		t.Fatalf("decoded delete request = %#v", req)
	}

	cases := []struct {
		name string
		body string
		code string
	}{
		{
			name: "unknown field",
			body: `{"expected_config_revision":7,"reason":"user_deleted","log_id":"peer-log-1780257600","future":true}`,
			code: "unknown_field: future",
		},
		{
			name: "missing revision",
			body: `{"reason":"user_deleted","log_id":"peer-log-1780257600"}`,
			code: "missing_ssh_peer_delete_field: expected_config_revision",
		},
		{
			name: "null revision",
			body: `{"expected_config_revision":null,"reason":"user_deleted","log_id":"peer-log-1780257600"}`,
			code: "missing_ssh_peer_delete_field: expected_config_revision",
		},
		{
			name: "missing reason",
			body: `{"expected_config_revision":7,"log_id":"peer-log-1780257600"}`,
			code: "missing_ssh_peer_delete_field: reason",
		},
		{
			name: "missing log id",
			body: `{"expected_config_revision":7,"reason":"user_deleted"}`,
			code: "missing_ssh_peer_delete_field: log_id",
		},
		{
			name: "blank log id",
			body: `{"expected_config_revision":7,"reason":"user_deleted","log_id":" "}`,
			code: "missing_ssh_peer_delete_field: log_id",
		},
		{
			name: "unstable reason",
			body: `{"expected_config_revision":7,"reason":"user deleted","log_id":"peer-log-1780257600"}`,
			code: "invalid_ssh_peer_delete_field: reason",
		},
		{
			name: "trailing data",
			body: `{"expected_config_revision":7,"reason":"user_deleted","log_id":"peer-log-1780257600"} {}`,
			code: "malformed_ssh_peer_delete_request: trailing data",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeSSHPeerDeleteRequest(strings.NewReader(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
		})
	}

	_, err = DecodeSSHPeerDeleteRequest(strings.NewReader(`{"expected_config_revision":0,"reason":"user_deleted","log_id":"peer-log-1780257600"}`))
	if !errors.Is(err, ErrConfigRevisionConflict) {
		t.Fatalf("zero revision error = %v, want ErrConfigRevisionConflict", err)
	}
}

func TestDisableSSHPeerSetsEnabledFalseRetainsMaterialAndWritesAudit(t *testing.T) {
	path := writeConfigForV2Test(t, disableDeleteBaseConfig(MigrationStateSSHKeysReady, true))

	status, err := disableSSHPeerWithGate(path, true, "fsck", SSHPeerDisableRequest{
		ExpectedConfigRevision: uint64Ptr(7),
		Reason:                 "user_disabled",
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.ConfigRevision == nil || *status.ConfigRevision != 8 {
		t.Fatalf("ConfigRevision = %v, want 8", status.ConfigRevision)
	}
	assertJSONValueEqual(t, false, status.Peer["enabled"])
	if _, ok := status.Peer["shared_key"]; ok {
		t.Fatalf("disable response exposed shared_key: %#v", status.Peer)
	}
	if _, ok := status.Peer["private_key"]; ok {
		t.Fatalf("disable response exposed private_key: %#v", status.Peer)
	}

	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 8)
	assertJSONValueEqual(t, map[string]any{"keep": true}, after["future_top"])
	ssh := after["ssh"].(map[string]any)
	assertJSONValueEqual(t, map[string]any{"keep": true}, ssh["future_ssh"])
	peer := peerByIDFromJSON(t, ssh["peers"], "fsck")
	assertJSONValueEqual(t, false, peer["enabled"])
	assertJSONValueEqual(t, "peer-secret", peer["shared_key"])
	assertJSONValueEqual(t, "peer-private", peer["private_key"])
	assertJSONValueEqual(t, map[string]any{"keep": true}, peer["future_peer"])
	if _, ok := peer["proof"]; !ok {
		t.Fatal("disable removed proof needed for repair")
	}
	other := peerByIDFromJSON(t, ssh["peers"], "other")
	assertJSONValueEqual(t, "other", other["id"])

	audit := ssh["audit_log"].([]any)
	if len(audit) != 1 {
		t.Fatalf("audit_log len = %d, want 1", len(audit))
	}
	entry := audit[0].(map[string]any)
	assertJSONValueEqual(t, "ssh_peer_disable", entry["source"])
	assertJSONValueEqual(t, true, entry["durable"])
	assertJSONValueEqual(t, "fsck", entry["peer_id"])
	assertJSONValueEqual(t, "user_disabled", entry["reason"])
	assertJSONValueEqual(t, string(MigrationStateSSHKeysReady), entry["previous_migration_state"])
}

func TestDisableSSHPeerAlreadyDisabledRecordsAuditRequest(t *testing.T) {
	body := strings.Replace(disableDeleteBaseConfig(MigrationStateSSHKeysReady, true), `"enabled": true,`, `"enabled": false,`, 1)
	path := writeConfigForV2Test(t, body)

	status, err := disableSSHPeerWithGate(path, true, "fsck", SSHPeerDisableRequest{
		ExpectedConfigRevision: uint64Ptr(7),
		Reason:                 "user_disabled",
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.ConfigRevision == nil || *status.ConfigRevision != 8 {
		t.Fatalf("ConfigRevision = %v, want 8", status.ConfigRevision)
	}

	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 8)
	ssh := after["ssh"].(map[string]any)
	audit := ssh["audit_log"].([]any)
	if len(audit) != 1 {
		t.Fatalf("audit_log len = %d, want 1", len(audit))
	}
	assertJSONValueEqual(t, "ssh_peer_disable", audit[0].(map[string]any)["source"])
}

func TestDisableSSHPeerProoflessStagedPeerWritesAudit(t *testing.T) {
	path := writeConfigForV2Test(t, disableDeleteBaseConfig(MigrationStateSSHMaterialStaged, false))

	status, err := disableSSHPeerWithGate(path, true, "fsck", SSHPeerDisableRequest{
		ExpectedConfigRevision: uint64Ptr(7),
		Reason:                 "user_disabled",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertJSONValueEqual(t, false, status.Peer["enabled"])

	after := readJSONMap(t, path)
	ssh := after["ssh"].(map[string]any)
	peer := peerByIDFromJSON(t, ssh["peers"], "fsck")
	assertJSONValueEqual(t, false, peer["enabled"])
	audit := ssh["audit_log"].([]any)
	assertJSONValueEqual(t, "ssh_peer_disable", audit[0].(map[string]any)["source"])
	assertJSONValueEqual(t, string(MigrationStateSSHMaterialStaged), audit[0].(map[string]any)["previous_migration_state"])
}

func TestDeleteSSHPeerLoopbackRemovesPeerAndWritesAudit(t *testing.T) {
	path := writeConfigForV2Test(t, disableDeleteBaseConfig(MigrationStateLoopbackUnprovisioned, false))

	status, err := deleteSSHPeerWithGate(path, true, "fsck", validDeleteRequest(7))
	if err != nil {
		t.Fatal(err)
	}
	if status.ConfigRevision == nil || *status.ConfigRevision != 8 {
		t.Fatalf("ConfigRevision = %v, want 8", status.ConfigRevision)
	}
	cleanup := status.Peer["cleanup_status"].(map[string]any)
	assertJSONValueEqual(t, false, cleanup["cleanup_required"])
	assertJSONValueEqual(t, false, cleanup["pending"])

	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 8)
	ssh := after["ssh"].(map[string]any)
	peers := ssh["peers"].([]any)
	if len(peers) != 1 {
		t.Fatalf("peers len = %d, want 1", len(peers))
	}
	assertJSONValueEqual(t, "other", peers[0].(map[string]any)["id"])
	if _, ok := ssh["remediation"]; ok {
		t.Fatalf("loopback delete wrote remediation: %#v", ssh["remediation"])
	}
	audit := ssh["audit_log"].([]any)
	entry := audit[0].(map[string]any)
	assertJSONValueEqual(t, "ssh_peer_delete", entry["source"])
	assertJSONValueEqual(t, "fsck", entry["peer_id"])
	assertJSONValueEqual(t, "user_deleted", entry["reason"])
	assertJSONValueEqual(t, "peer-log-1780257600", entry["log_id"])
}

func TestDeleteSSHPeerPreSecretWritesCleanupRecordBeforeRemovingPeer(t *testing.T) {
	path := writeConfigForV2Test(t, disableDeleteBaseConfig(MigrationStateSSHMaterialStaged, true))

	status, err := deleteSSHPeerWithGate(path, true, "fsck", validDeleteRequest(7))
	if err != nil {
		t.Fatal(err)
	}
	cleanupStatus := status.Peer["cleanup_status"].(map[string]any)
	assertJSONValueEqual(t, "ssh_material_cleanup", cleanupStatus["source"])
	assertJSONValueEqual(t, true, cleanupStatus["cleanup_required"])
	assertJSONValueEqual(t, true, cleanupStatus["pending"])
	if _, ok := status.Peer["shared_key"]; ok {
		t.Fatalf("delete response exposed shared_key: %#v", status.Peer)
	}
	if _, ok := status.Peer["private_key"]; ok {
		t.Fatalf("delete response exposed private_key: %#v", status.Peer)
	}

	after := readJSONMap(t, path)
	ssh := after["ssh"].(map[string]any)
	if _, found := findPeerByIDFromJSON(ssh["peers"], "fsck"); found {
		t.Fatal("deleted peer row still exists")
	}
	remediation := ssh["remediation"].([]any)
	if len(remediation) != 1 {
		t.Fatalf("remediation len = %d, want 1", len(remediation))
	}
	record := remediation[0].(map[string]any)
	assertJSONValueEqual(t, "ssh_material_cleanup", record["source"])
	assertJSONValueEqual(t, true, record["durable"])
	assertJSONValueEqual(t, true, record["cleanup_required"])
	assertJSONValueEqual(t, true, record["pending"])
	assertJSONValueEqual(t, "fsck", record["peer_id"])
	assertJSONValueEqual(t, string(MigrationStateSSHMaterialStaged), record["previous_migration_state"])
	assertJSONValueEqual(t, "peer-log-1780257600", record["log_id"])
	assertJSONValueEqual(t, "fsck.com", record["ssh_host"])
	assertJSONValueEqual(t, "accept-key-1", record["accept_key_id"])
	assertJSONValueEqual(t, "connect-key-1", record["connect_key_id"])
	actions := record["remaining_user_actions"].([]any)
	assertJSONValueEqual(t, "retry_regular_ssh_cleanup", actions[0])

	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("peer-secret")) || bytes.Contains(data, []byte("peer-private")) {
		t.Fatalf("pre-secret cleanup record leaked secret material: %s", data)
	}
}

func TestDeleteSSHPeerProvisionFailedCapturesLastProvisioningPhase(t *testing.T) {
	cases := []struct {
		name string
		log  string
		want string
	}{
		{
			name: "top level failed phase",
			log:  `[{"from_state":"ssh_material_staged","to_state":"provision_failed","reason":"provision_failed","log_id":"existing-log","failed_phase":"managed_authorized_keys_write","remote_secret_absence_proof":{"failed_phase":"host_key_confirmation","secret_write_command_spawned":false,"absence_verified_by":"local_config_scan","verified_at":"2026-06-02T12:34:56Z","log_id":"existing-log"}}]`,
			want: "managed_authorized_keys_write",
		},
		{
			name: "absence proof failed phase",
			log:  `[{"from_state":"loopback_unprovisioned","to_state":"provision_failed","reason":"provision_failed","log_id":"existing-log","remote_secret_absence_proof":{"failed_phase":"host_key_confirmation","secret_write_command_spawned":false,"absence_verified_by":"local_config_scan","verified_at":"2026-06-02T12:34:56Z","log_id":"existing-log"}}]`,
			want: "host_key_confirmation",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(
				disableDeleteBaseConfig(MigrationStateProvisionFailed, true),
				`"future_peer": {"keep": true}`,
				`"future_peer": {"keep": true}, "migration_log": `+tc.log,
				1,
			)
			path := writeConfigForV2Test(t, body)

			_, err := deleteSSHPeerWithGate(path, true, "fsck", validDeleteRequest(7))
			if err != nil {
				t.Fatal(err)
			}

			after := readJSONMap(t, path)
			record := after["ssh"].(map[string]any)["remediation"].([]any)[0].(map[string]any)
			assertJSONValueEqual(t, "ssh_material_cleanup", record["source"])
			assertJSONValueEqual(t, string(MigrationStateProvisionFailed), record["previous_migration_state"])
			assertJSONValueEqual(t, tc.want, record["last_provisioning_phase"])
		})
	}
}

func TestDeleteSSHPeerProvisionFailedWithoutAbsenceProofFallsBackToTombstone(t *testing.T) {
	body := strings.Replace(
		disableDeleteBaseConfig(MigrationStateProvisionFailed, true),
		`"future_peer": {"keep": true}`,
		`"future_peer": {"keep": true}, "migration_log": [{"from_state":"ssh_material_staged","to_state":"provision_failed","reason":"provision_failed","log_id":"existing-log","failed_phase":"managed_authorized_keys_write"}]`,
		1,
	)
	path := writeConfigForV2Test(t, body)

	_, err := deleteSSHPeerWithGate(path, true, "fsck", validDeleteRequest(7))
	if err != nil {
		t.Fatal(err)
	}

	after := readJSONMap(t, path)
	record := after["ssh"].(map[string]any)["remediation"].([]any)[0].(map[string]any)
	assertJSONValueEqual(t, "post_secret_tombstone", record["source"])
	assertJSONValueEqual(t, string(MigrationStateProvisionFailed), record["previous_migration_state"])
	assertJSONValueEqual(t, false, record["remote_fleet_secret_cleanup_verified"])
	actions := record["remaining_user_actions"].([]any)
	assertJSONValueEqual(t, "rotate_fleet_shared_key", actions[1])
}

func TestDeleteSSHPeerProvisionFailedAfterSecretWriteSpawnedWritesTombstone(t *testing.T) {
	body := strings.Replace(
		disableDeleteBaseConfig(MigrationStateProvisionFailed, true),
		`"future_peer": {"keep": true}`,
		`"future_peer": {"keep": true}, "migration_log": [{"from_state":"ssh_material_staged","to_state":"provision_failed","reason":"provision_failed","log_id":"existing-log","failed_phase":"remote_shared_key_write","remote_secret_absence_proof":{"failed_phase":"remote_shared_key_write","secret_write_command_spawned":true,"absence_verified_by":"regular_ssh_locked_read","verified_at":"2026-06-02T12:34:56Z","log_id":"existing-log"}}]`,
		1,
	)
	path := writeConfigForV2Test(t, body)

	_, err := deleteSSHPeerWithGate(path, true, "fsck", validDeleteRequest(7))
	if err != nil {
		t.Fatal(err)
	}

	after := readJSONMap(t, path)
	record := after["ssh"].(map[string]any)["remediation"].([]any)[0].(map[string]any)
	assertJSONValueEqual(t, "post_secret_tombstone", record["source"])
	assertJSONValueEqual(t, string(MigrationStateProvisionFailed), record["previous_migration_state"])
	assertJSONValueEqual(t, false, record["remote_fleet_secret_cleanup_verified"])
	actions := record["remaining_user_actions"].([]any)
	assertJSONValueEqual(t, "rotate_fleet_shared_key", actions[1])
}

func TestDeleteSSHPeerPostSecretWritesTombstoneBeforeRemovingPeer(t *testing.T) {
	path := writeConfigForV2Test(t, disableDeleteBaseConfig(MigrationStateSSHKeysReady, true))

	status, err := deleteSSHPeerWithGate(path, true, "fsck", validDeleteRequest(7))
	if err != nil {
		t.Fatal(err)
	}
	cleanupStatus := status.Peer["cleanup_status"].(map[string]any)
	assertJSONValueEqual(t, "post_secret_tombstone", cleanupStatus["source"])
	assertJSONValueEqual(t, true, cleanupStatus["pending"])
	if _, ok := status.Peer["shared_key"]; ok {
		t.Fatalf("delete response exposed shared_key: %#v", status.Peer)
	}
	if _, ok := status.Peer["private_key"]; ok {
		t.Fatalf("delete response exposed private_key: %#v", status.Peer)
	}

	after := readJSONMap(t, path)
	ssh := after["ssh"].(map[string]any)
	if _, found := findPeerByIDFromJSON(ssh["peers"], "fsck"); found {
		t.Fatal("deleted peer row still exists")
	}
	record := ssh["remediation"].([]any)[0].(map[string]any)
	assertJSONValueEqual(t, "post_secret_tombstone", record["source"])
	assertJSONValueEqual(t, false, record["remote_fleet_secret_cleanup_verified"])
	assertJSONValueEqual(t, false, record["remote_managed_key_cleanup_verified"])
	actions := record["remaining_user_actions"].([]any)
	assertJSONValueEqual(t, "retry_regular_ssh_cleanup", actions[0])
	assertJSONValueEqual(t, "rotate_fleet_shared_key", actions[1])

	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("peer-secret")) || bytes.Contains(data, []byte("peer-private")) {
		t.Fatalf("post-secret tombstone leaked secret material: %s", data)
	}
}

func TestDeleteSSHPeerSharedKeyWrittenUnverifiedWritesTombstone(t *testing.T) {
	path := writeConfigForV2Test(t, disableDeleteBaseConfig(MigrationStateSharedKeyWrittenUnverified, false))

	status, err := deleteSSHPeerWithGate(path, true, "fsck", validDeleteRequest(7))
	if err != nil {
		t.Fatal(err)
	}
	cleanupStatus := status.Peer["cleanup_status"].(map[string]any)
	assertJSONValueEqual(t, "post_secret_tombstone", cleanupStatus["source"])

	after := readJSONMap(t, path)
	ssh := after["ssh"].(map[string]any)
	if _, found := findPeerByIDFromJSON(ssh["peers"], "fsck"); found {
		t.Fatal("deleted peer row still exists")
	}
	record := ssh["remediation"].([]any)[0].(map[string]any)
	assertJSONValueEqual(t, "post_secret_tombstone", record["source"])
	assertJSONValueEqual(t, string(MigrationStateSharedKeyWrittenUnverified), record["previous_migration_state"])
	if _, ok := record["accept_key_id"]; ok {
		t.Fatalf("tombstone unexpectedly copied absent accept proof: %#v", record)
	}
	if _, ok := record["connect_key_id"]; ok {
		t.Fatalf("tombstone unexpectedly copied absent connect proof: %#v", record)
	}
}

func TestDeleteSSHPeerAppendsAuditAndRemediationHistory(t *testing.T) {
	body := strings.Replace(
		disableDeleteBaseConfig(MigrationStateSSHMaterialStaged, true),
		`"future_ssh": {"keep": true},`,
		`"future_ssh": {"keep": true},
    "audit_log": [{"source":"existing_audit","peer_id":"old"}],
    "remediation": [{"source":"existing_remediation","peer_id":"old"}],`,
		1,
	)
	path := writeConfigForV2Test(t, body)

	_, err := deleteSSHPeerWithGate(path, true, "fsck", validDeleteRequest(7))
	if err != nil {
		t.Fatal(err)
	}

	after := readJSONMap(t, path)
	ssh := after["ssh"].(map[string]any)
	audit := ssh["audit_log"].([]any)
	if len(audit) != 2 {
		t.Fatalf("audit_log len = %d, want 2", len(audit))
	}
	assertJSONValueEqual(t, "existing_audit", audit[0].(map[string]any)["source"])
	assertJSONValueEqual(t, "ssh_peer_delete", audit[1].(map[string]any)["source"])
	remediation := ssh["remediation"].([]any)
	if len(remediation) != 2 {
		t.Fatalf("remediation len = %d, want 2", len(remediation))
	}
	assertJSONValueEqual(t, "existing_remediation", remediation[0].(map[string]any)["source"])
	assertJSONValueEqual(t, "ssh_material_cleanup", remediation[1].(map[string]any)["source"])
}

func TestDisableDeleteSSHPeerRejectMalformedHistoryWithoutWriting(t *testing.T) {
	cases := []struct {
		name string
		body string
		run  func(string) error
		code string
	}{
		{
			name: "disable malformed audit log",
			body: strings.Replace(disableDeleteBaseConfig(MigrationStateSSHKeysReady, true), `"future_ssh": {"keep": true},`, `"future_ssh": {"keep": true}, "audit_log": "bad",`, 1),
			run: func(path string) error {
				_, err := disableSSHPeerWithGate(path, true, "fsck", SSHPeerDisableRequest{ExpectedConfigRevision: uint64Ptr(7), Reason: "user_disabled"})
				return err
			},
			code: "invalid_ssh_peer_audit_log",
		},
		{
			name: "delete malformed remediation",
			body: strings.Replace(disableDeleteBaseConfig(MigrationStateSSHMaterialStaged, true), `"future_ssh": {"keep": true},`, `"future_ssh": {"keep": true}, "remediation": "bad",`, 1),
			run: func(path string) error {
				_, err := deleteSSHPeerWithGate(path, true, "fsck", validDeleteRequest(7))
				return err
			},
			code: "invalid_ssh_peer_remediation",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfigForV2Test(t, tc.body)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			err = tc.run(path)
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("%s changed config\nbefore=%s\nafter=%s", tc.name, before, after)
			}
		})
	}
}

func TestDisableDeleteSSHPeerMissingPeerDoesNotWrite(t *testing.T) {
	cases := []struct {
		name string
		run  func(string) error
	}{
		{
			name: "disable missing peer",
			run: func(path string) error {
				_, err := disableSSHPeerWithGate(path, true, "missing", SSHPeerDisableRequest{ExpectedConfigRevision: uint64Ptr(7), Reason: "user_disabled"})
				return err
			},
		},
		{
			name: "delete missing peer",
			run: func(path string) error {
				_, err := deleteSSHPeerWithGate(path, true, "missing", validDeleteRequest(7))
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfigForV2Test(t, disableDeleteBaseConfig(MigrationStateSSHKeysReady, true))
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			err = tc.run(path)
			if err == nil || !strings.Contains(err.Error(), "ssh_peer_not_found: missing") {
				t.Fatalf("error = %v, want ssh_peer_not_found", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("%s changed config\nbefore=%s\nafter=%s", tc.name, before, after)
			}
		})
	}
}

func TestDisableDeleteSSHPeerRejectStaleRevisionAndGateDisabledWithoutWriting(t *testing.T) {
	cases := []struct {
		name string
		run  func(string) error
	}{
		{
			name: "disable stale",
			run: func(path string) error {
				_, err := disableSSHPeerWithGate(path, true, "fsck", SSHPeerDisableRequest{ExpectedConfigRevision: uint64Ptr(6), Reason: "user_disabled"})
				return err
			},
		},
		{
			name: "delete stale",
			run: func(path string) error {
				_, err := deleteSSHPeerWithGate(path, true, "fsck", SSHPeerDeleteRequest{ExpectedConfigRevision: uint64Ptr(6), Reason: "user_deleted", LogID: "peer-log-1780257600"})
				return err
			},
		},
		{
			name: "disable gate disabled",
			run: func(path string) error {
				_, err := disableSSHPeerWithGate(path, false, "fsck", SSHPeerDisableRequest{ExpectedConfigRevision: uint64Ptr(7), Reason: "user_disabled"})
				return err
			},
		},
		{
			name: "delete gate disabled",
			run: func(path string) error {
				_, err := deleteSSHPeerWithGate(path, false, "fsck", validDeleteRequest(7))
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfigForV2Test(t, disableDeleteBaseConfig(MigrationStateSSHKeysReady, true))
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			err = tc.run(path)
			if tc.name == "disable gate disabled" || tc.name == "delete gate disabled" {
				if !errors.Is(err, ErrConfigV2WritesDisabled) {
					t.Fatalf("error = %v, want ErrConfigV2WritesDisabled", err)
				}
			} else if !errors.Is(err, ErrConfigRevisionConflict) {
				t.Fatalf("error = %v, want ErrConfigRevisionConflict", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("%s changed config\nbefore=%s\nafter=%s", tc.name, before, after)
			}
		})
	}
}

func TestDisableDeleteSSHPeerRejectMissingMigrationStateWithoutWriting(t *testing.T) {
	cases := []struct {
		name string
		run  func(string) error
		code string
	}{
		{
			name: "disable missing state",
			run: func(path string) error {
				_, err := disableSSHPeerWithGate(path, true, "fsck", SSHPeerDisableRequest{ExpectedConfigRevision: uint64Ptr(7), Reason: "user_disabled"})
				return err
			},
			code: "invalid_ssh_peer_disable_state",
		},
		{
			name: "delete missing state",
			run: func(path string) error {
				_, err := deleteSSHPeerWithGate(path, true, "fsck", validDeleteRequest(7))
				return err
			},
			code: "invalid_ssh_peer_delete_state",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(disableDeleteBaseConfig(MigrationStateSSHKeysReady, true), `        "migration_state": "ssh_keys_ready",`+"\n", "", 1)
			path := writeConfigForV2Test(t, body)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			err = tc.run(path)
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("%s changed config\nbefore=%s\nafter=%s", tc.name, before, after)
			}
		})
	}
}

func TestDisableDeleteSSHPeerDirectValidationRejectsInvalidStructsWithoutWriting(t *testing.T) {
	cases := []struct {
		name string
		run  func(string) error
		code string
	}{
		{
			name: "disable invalid reason",
			run: func(path string) error {
				_, err := disableSSHPeerWithGate(path, true, "fsck", SSHPeerDisableRequest{ExpectedConfigRevision: uint64Ptr(7), Reason: "user disabled"})
				return err
			},
			code: "invalid_ssh_peer_disable_field: reason",
		},
		{
			name: "delete invalid reason",
			run: func(path string) error {
				_, err := deleteSSHPeerWithGate(path, true, "fsck", SSHPeerDeleteRequest{ExpectedConfigRevision: uint64Ptr(7), Reason: "user deleted", LogID: "peer-log-1780257600"})
				return err
			},
			code: "invalid_ssh_peer_delete_field: reason",
		},
		{
			name: "delete blank log id",
			run: func(path string) error {
				_, err := deleteSSHPeerWithGate(path, true, "fsck", SSHPeerDeleteRequest{ExpectedConfigRevision: uint64Ptr(7), Reason: "user_deleted", LogID: " "})
				return err
			},
			code: "missing_ssh_peer_delete_field: log_id",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfigForV2Test(t, disableDeleteBaseConfig(MigrationStateSSHKeysReady, true))
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			err = tc.run(path)
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("%s changed config\nbefore=%s\nafter=%s", tc.name, before, after)
			}
		})
	}
}

func TestTransitionSSHPeerRejectsInvalidRequestsWithoutWriting(t *testing.T) {
	cases := []struct {
		name       string
		configBody string
		req        SSHPeerTransitionRequest
		code       string
	}{
		{
			name: "runtime ui state",
			req:  validTransitionRequest(7, MigrationStateLoopbackUnprovisioned, MigrationState("never_connected"), "material_staged"),
			code: "invalid_ssh_peer_transition_state: to_state",
		},
		{
			name: "missing log id",
			req: SSHPeerTransitionRequest{
				ExpectedConfigRevision: uint64Ptr(7),
				FromState:              MigrationStateLoopbackUnprovisioned,
				ToState:                MigrationStateSSHMaterialStaged,
				Reason:                 "material_staged",
			},
			code: "missing_ssh_peer_transition_field: log_id",
		},
		{
			name: "missing absence proof",
			req:  SSHPeerTransitionRequest{ExpectedConfigRevision: uint64Ptr(7), FromState: MigrationStateLoopbackUnprovisioned, ToState: MigrationStateProvisionFailed, Reason: "provision_failed", LogID: "log-1", FailedPhase: stringPtr("host_key_confirmation")},
			code: "missing_ssh_peer_transition_field: remote_secret_absence_proof",
		},
		{
			name: "pre secret phase strict when command not spawned",
			req:  validProvisionFailedTransitionRequest(7, MigrationStateLoopbackUnprovisioned, false, "remote_shared_key_write"),
			code: "invalid_ssh_peer_transition_failed_phase",
		},
		{
			name: "provision failed proof timestamp must be valid",
			req: func() SSHPeerTransitionRequest {
				req := validProvisionFailedTransitionRequest(7, MigrationStateLoopbackUnprovisioned, false, "host_key_confirmation")
				req.RemoteSecretAbsenceProof.VerifiedAt = "not-a-timestamp"
				return req
			}(),
			code: "invalid_ssh_peer_transition_field: remote_secret_absence_proof.verified_at",
		},
		{
			name: "provision failed phase must match absence proof",
			req: func() SSHPeerTransitionRequest {
				req := validProvisionFailedTransitionRequest(7, MigrationStateLoopbackUnprovisioned, false, "host_key_confirmation")
				req.RemoteSecretAbsenceProof.FailedPhase = "local_peer_create"
				return req
			}(),
			code: "ssh_peer_transition_absence_proof_failed_phase_mismatch",
		},
		{
			name: "current state mismatch",
			req:  validTransitionRequest(7, MigrationStateSSHMaterialStaged, MigrationStateSharedKeyWrittenUnverified, "remote_shared_key_written"),
			code: "ssh_peer_transition_state_mismatch",
		},
		{
			name: "valid states but transition not allowed",
			req:  validTransitionRequest(7, MigrationStateLoopbackUnprovisioned, MigrationStateSSHKeysReady, "gateway_version_verified"),
			code: "ssh_peer_transition_not_allowed",
		},
		{
			name:       "missing accept material",
			configBody: strings.Replace(transitionBaseConfig(MigrationStateLoopbackUnprovisioned, true), `      "gateway_path": "/home/jesse/.local/bin/clipfan",`+"\n", "", 1),
			req:        validTransitionRequest(7, MigrationStateLoopbackUnprovisioned, MigrationStateSSHMaterialStaged, "material_staged"),
			code:       "ssh_peer_transition_requires_accept_material",
		},
		{
			name:       "missing connect material",
			configBody: strings.Replace(transitionBaseConfig(MigrationStateLoopbackUnprovisioned, true), `      "install_path": "/home/jesse/.local/bin/clipfan",`+"\n", "", 1),
			req:        validTransitionRequest(7, MigrationStateLoopbackUnprovisioned, MigrationStateSSHMaterialStaged, "material_staged"),
			code:       "ssh_peer_transition_requires_connect_material",
		},
		{
			name:       "missing shared-key promotion proof",
			configBody: transitionBaseConfig(MigrationStateSharedKeyWrittenUnverified, false),
			req:        validTransitionRequest(7, MigrationStateSharedKeyWrittenUnverified, MigrationStateSSHKeysReady, "gateway_version_verified"),
			code:       "ssh_peer_transition_requires_current_proof",
		},
		{
			name:       "missing verified-material promotion proof",
			configBody: transitionBaseConfig(MigrationStateSSHMaterialStaged, false),
			req:        validTransitionRequest(7, MigrationStateSSHMaterialStaged, MigrationStateSSHKeysReady, "ssh_material_verified"),
			code:       "ssh_peer_transition_requires_current_proof",
		},
		{
			name: "failed phase rejected outside provision failed",
			req: SSHPeerTransitionRequest{
				ExpectedConfigRevision: uint64Ptr(7),
				FromState:              MigrationStateLoopbackUnprovisioned,
				ToState:                MigrationStateSSHMaterialStaged,
				Reason:                 "material_staged",
				LogID:                  "log-1",
				FailedPhase:            stringPtr("host_key_confirmation"),
			},
			code: "invalid_ssh_peer_transition_field: failed_phase",
		},
		{
			name: "absence proof rejected outside provision failed",
			req: SSHPeerTransitionRequest{
				ExpectedConfigRevision: uint64Ptr(7),
				FromState:              MigrationStateLoopbackUnprovisioned,
				ToState:                MigrationStateSSHMaterialStaged,
				Reason:                 "material_staged",
				LogID:                  "log-1",
				RemoteSecretAbsenceProof: &SSHPeerRemoteSecretAbsenceProof{
					FailedPhase:               "host_key_confirmation",
					SecretWriteCommandSpawned: false,
					AbsenceVerifiedBy:         "local_config_scan",
					VerifiedAt:                "not-a-timestamp",
					LogID:                     "log-1",
				},
			},
			code: "invalid_ssh_peer_transition_field: remote_secret_absence_proof",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configBody := tc.configBody
			if configBody == "" {
				configBody = transitionBaseConfig(MigrationStateLoopbackUnprovisioned, true)
			}
			path := writeConfigForV2Test(t, configBody)
			before := readJSONMap(t, path)
			_, err := transitionSSHPeerWithGate(path, true, "fsck", tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
			after := readJSONMap(t, path)
			assertJSONValueEqual(t, before, after)
		})
	}
}

func TestTransitionSSHPeerRejectsStaleRevisionAndGateDisabledWithoutWriting(t *testing.T) {
	path := writeConfigForV2Test(t, transitionBaseConfig(MigrationStateLoopbackUnprovisioned, true))
	before := readJSONMap(t, path)

	_, err := transitionSSHPeerWithGate(path, true, "fsck", validTransitionRequest(6, MigrationStateLoopbackUnprovisioned, MigrationStateSSHMaterialStaged, "material_staged"))
	if !errors.Is(err, ErrConfigRevisionConflict) {
		t.Fatalf("stale revision error = %v, want ErrConfigRevisionConflict", err)
	}
	after := readJSONMap(t, path)
	assertJSONValueEqual(t, before, after)

	_, err = transitionSSHPeerWithGate(path, false, "fsck", validTransitionRequest(7, MigrationStateLoopbackUnprovisioned, MigrationStateSSHMaterialStaged, "material_staged"))
	if !errors.Is(err, ErrConfigV2WritesDisabled) {
		t.Fatalf("gate disabled error = %v, want ErrConfigV2WritesDisabled", err)
	}
	after = readJSONMap(t, path)
	assertJSONValueEqual(t, before, after)
}

func TestTransitionSSHPeerRejectsMissingPeerWithoutWriting(t *testing.T) {
	path := writeConfigForV2Test(t, transitionBaseConfig(MigrationStateLoopbackUnprovisioned, true))
	before := readJSONMap(t, path)

	_, err := transitionSSHPeerWithGate(path, true, "missing", validTransitionRequest(7, MigrationStateLoopbackUnprovisioned, MigrationStateSSHMaterialStaged, "material_staged"))
	if err == nil || !strings.Contains(err.Error(), "ssh_peer_not_found: missing") {
		t.Fatalf("missing peer error = %v, want ssh_peer_not_found", err)
	}
	after := readJSONMap(t, path)
	assertJSONValueEqual(t, before, after)
}

func TestDecodeSSHPeerUpsertRequestDecodesAllFields(t *testing.T) {
	body := `{
  "expected_config_revision": 7,
  "peer": {
    "id": "fsck",
    "enabled": true,
    "accept": false,
    "connect": true,
    "persistent": true,
    "on_demand": false,
    "ssh_host": "FSCK.COM.",
    "ssh_user": "jesse",
    "ssh_port": 2222,
    "install_path": "/home/jesse/.local/bin/clipfan",
    "gateway_path": "/home/jesse/bin/clipfan",
    "migration_state": "loopback_unprovisioned"
  }
}`

	req, err := DecodeSSHPeerUpsertRequest(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	requireUint64Ptr(t, "expected_config_revision", req.ExpectedConfigRevision, 7)
	requireStringPtr(t, "id", req.Peer.ID, "fsck")
	requireBoolPtr(t, "enabled", req.Peer.Enabled, true)
	requireBoolPtr(t, "accept", req.Peer.Accept, false)
	requireBoolPtr(t, "connect", req.Peer.Connect, true)
	requireBoolPtr(t, "persistent", req.Peer.Persistent, true)
	requireBoolPtr(t, "on_demand", req.Peer.OnDemand, false)
	requireStringPtr(t, "ssh_host", req.Peer.SSHHost, "FSCK.COM.")
	requireStringPtr(t, "ssh_user", req.Peer.SSHUser, "jesse")
	requireIntPtr(t, "ssh_port", req.Peer.SSHPort, 2222)
	requireStringPtr(t, "install_path", req.Peer.InstallPath, "/home/jesse/.local/bin/clipfan")
	requireStringPtr(t, "gateway_path", req.Peer.GatewayPath, "/home/jesse/bin/clipfan")
	requireMigrationStatePtr(t, "migration_state", req.Peer.MigrationState, MigrationStateLoopbackUnprovisioned)
}

func TestDecodeSSHPeerUpsertRequestRejectsUnknownFields(t *testing.T) {
	cases := []struct {
		name string
		body string
		code string
	}{
		{
			name: "unknown wrapper field",
			body: `{"peer":{"id":"fsck"},"unexpected":true}`,
			code: "unknown_field: unexpected",
		},
		{
			name: "unknown peer field",
			body: `{"expected_config_revision":7,"peer":{"id":"fsck","future":true}}`,
			code: "unknown_field: peer.future",
		},
		{
			name: "secret peer field",
			body: `{"expected_config_revision":7,"peer":{"id":"fsck","shared_key":"secret"}}`,
			code: "ssh_peer_secret_field_not_allowed: peer.shared_key",
		},
		{
			name: "null scalar field",
			body: `{"expected_config_revision":7,"peer":{"enabled":null}}`,
			code: "invalid_ssh_peer_upsert_field: peer.enabled",
		},
		{
			name: "null id field",
			body: `{"expected_config_revision":7,"peer":{"id":null}}`,
			code: "invalid_ssh_peer_upsert_field: peer.id",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeSSHPeerUpsertRequest(strings.NewReader(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
		})
	}
}

func TestDecodeSSHPeerUpsertRequestRejectsZeroRevisionAndTrailingJSON(t *testing.T) {
	_, err := DecodeSSHPeerUpsertRequest(strings.NewReader(`{"expected_config_revision":0,"peer":{"id":"fsck"}}`))
	if !errors.Is(err, ErrConfigRevisionConflict) {
		t.Fatalf("zero revision error = %v, want ErrConfigRevisionConflict", err)
	}

	_, err = DecodeSSHPeerUpsertRequest(strings.NewReader(`{"expected_config_revision":7,"peer":{"id":"fsck"}} {}`))
	if err == nil || !strings.Contains(err.Error(), "malformed_ssh_peer_upsert_request: trailing data") {
		t.Fatalf("trailing JSON error = %v, want malformed trailing data", err)
	}
}

func requireUint64Ptr(t *testing.T, name string, got *uint64, want uint64) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("%s = %v, want %d", name, got, want)
	}
}

func requireStringPtr(t *testing.T, name string, got *string, want string) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("%s = %v, want %q", name, got, want)
	}
}

func requireBoolPtr(t *testing.T, name string, got *bool, want bool) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("%s = %v, want %t", name, got, want)
	}
}

func requireIntPtr(t *testing.T, name string, got *int, want int) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("%s = %v, want %d", name, got, want)
	}
}

func requireMigrationStatePtr(t *testing.T, name string, got *MigrationState, want MigrationState) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("%s = %v, want %s", name, got, want)
	}
}

func proofPatchBaseConfig() string {
	return `{
  "config_version": 2,
  "config_revision": 7,
  "shared_key": "k",
  "hostname": "m4",
  "transport": "ssh",
  "ssh": {"peers": [{
    "id": "fsck",
    "enabled": true,
    "accept": true,
    "connect": true,
    "persistent": true,
    "ssh_host": "fsck.com",
    "ssh_user": "jesse",
    "ssh_port": 22,
    "install_path": "/home/jesse/.local/bin/clipfan",
    "gateway_path": "/home/jesse/.local/bin/clipfan",
    "migration_state": "loopback_unprovisioned"
  }]}
}`
}

func validAcceptProofPatchRequest(revision uint64) SSHPeerProofPatchRequest {
	return SSHPeerProofPatchRequest{
		ExpectedConfigRevision: uint64Ptr(revision),
		AcceptProof: &SSHPeerDirectionalProofPatch{
			KeyID:       "accept-key-1",
			GatewayPath: "/home/jesse/.local/bin/clipfan",
			VerifiedAt:  "2026-06-02T12:34:56Z",
			VerifiedBy:  ProofVerifiedByLocalFile,
		},
	}
}

func transitionBaseConfig(state MigrationState, includeProof bool) string {
	proof := ""
	if includeProof {
		proof = `,
    "proof": {
      "accept_key_id": "accept-key-1",
      "accept_gateway_path": "/home/jesse/.local/bin/clipfan",
      "accept_verified_at": "2026-06-02T12:34:56Z",
      "accept_verified_by": "local_file",
      "connect_key_id": "connect-key-1",
      "connect_gateway_path": "/home/jesse/.local/bin/clipfan",
      "connect_verified_at": "2026-06-02T12:35:56Z",
      "connect_verified_by": "regular_ssh"
    }`
	}
	return `{
  "config_version": 2,
  "config_revision": 7,
  "shared_key": "k",
  "hostname": "m4",
  "transport": "ssh",
  "future_top": {"keep": true},
  "ssh": {
    "future_ssh": {"keep": true},
    "peers": [{
      "id": "fsck",
      "enabled": true,
      "accept": true,
      "connect": true,
      "persistent": true,
      "ssh_host": "fsck.com",
      "ssh_user": "jesse",
      "ssh_port": 22,
      "install_path": "/home/jesse/.local/bin/clipfan",
      "gateway_path": "/home/jesse/.local/bin/clipfan",
      "migration_state": "` + string(state) + `",
      "shared_key": "peer-secret",
      "private_key": "peer-private",
      "future_peer": {"keep": true},
      "migration_log": [{"from_state":"initial","to_state":"` + string(state) + `","reason":"existing","log_id":"existing-log"}]` + proof + `
    }]
  }
}`
}

func validTransitionRequest(revision uint64, from MigrationState, to MigrationState, reason string) SSHPeerTransitionRequest {
	return SSHPeerTransitionRequest{
		ExpectedConfigRevision: uint64Ptr(revision),
		FromState:              from,
		ToState:                to,
		Reason:                 reason,
		LogID:                  "log-1",
	}
}

func validProvisionFailedTransitionRequest(revision uint64, from MigrationState, secretWriteCommandSpawned bool, failedPhase string) SSHPeerTransitionRequest {
	return SSHPeerTransitionRequest{
		ExpectedConfigRevision: uint64Ptr(revision),
		FromState:              from,
		ToState:                MigrationStateProvisionFailed,
		Reason:                 "provision_failed",
		LogID:                  "log-1",
		FailedPhase:            stringPtr(failedPhase),
		RemoteSecretAbsenceProof: &SSHPeerRemoteSecretAbsenceProof{
			FailedPhase:               failedPhase,
			SecretWriteCommandSpawned: secretWriteCommandSpawned,
			AbsenceVerifiedBy:         "local_config_scan",
			VerifiedAt:                "2026-06-02T12:34:56Z",
			RemoteConfigRevision:      uint64Ptr(12),
			LogID:                     "log-1",
		},
	}
}

func validDeleteRequest(revision uint64) SSHPeerDeleteRequest {
	return SSHPeerDeleteRequest{
		ExpectedConfigRevision: uint64Ptr(revision),
		Reason:                 "user_deleted",
		LogID:                  "peer-log-1780257600",
	}
}

func disableDeleteBaseConfig(state MigrationState, includeProof bool) string {
	proof := ""
	if includeProof {
		proof = `,
        "proof": {
          "accept_key_id": "accept-key-1",
          "accept_gateway_path": "/home/jesse/.local/bin/clipfan",
          "accept_verified_at": "2026-06-02T12:34:56Z",
          "accept_verified_by": "local_file",
          "connect_key_id": "connect-key-1",
          "connect_gateway_path": "/home/jesse/.local/bin/clipfan",
          "connect_verified_at": "2026-06-02T12:35:56Z",
          "connect_verified_by": "regular_ssh"
        }`
	}
	return `{
  "config_version": 2,
  "config_revision": 7,
  "shared_key": "k",
  "hostname": "m4",
  "transport": "ssh",
  "future_top": {"keep": true},
  "ssh": {
    "future_ssh": {"keep": true},
    "peers": [
      {
        "id": "fsck",
        "enabled": true,
        "accept": true,
        "connect": true,
        "persistent": true,
        "ssh_host": "fsck.com",
        "ssh_user": "jesse",
        "ssh_port": 22,
        "install_path": "/home/jesse/.local/bin/clipfan",
        "gateway_path": "/home/jesse/.local/bin/clipfan",
        "migration_state": "` + string(state) + `",
        "shared_key": "peer-secret",
        "private_key": "peer-private",
        "future_peer": {"keep": true}` + proof + `
      },
      {
        "id": "other",
        "enabled": true,
        "accept": true,
        "migration_state": "loopback_unprovisioned",
        "future_peer": {"keep": "other"}
      }
    ]
  }
}`
}

func peerByIDFromJSON(t *testing.T, value any, peerID string) map[string]any {
	t.Helper()
	peer, found := findPeerByIDFromJSON(value, peerID)
	if !found {
		t.Fatalf("peer %q not found in %#v", peerID, value)
	}
	return peer
}

func findPeerByIDFromJSON(value any, peerID string) (map[string]any, bool) {
	peers, ok := value.([]any)
	if !ok {
		return nil, false
	}
	for _, value := range peers {
		peer, ok := value.(map[string]any)
		if ok && peer["id"] == peerID {
			return peer, true
		}
	}
	return nil, false
}

func stringPtr(v string) *string { return &v }
func boolPtr(v bool) *bool       { return &v }

func migrationStatePtr(v MigrationState) *MigrationState { return &v }
