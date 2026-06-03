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

func stringPtr(v string) *string { return &v }
func boolPtr(v bool) *bool       { return &v }

func migrationStatePtr(v MigrationState) *MigrationState { return &v }
