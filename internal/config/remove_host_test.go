package config

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestRemoveHostRemovesStaticAndSSHPeerWithAudit(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "k",
		"hostname": "m4",
		"transport": "ssh",
		"discovery": "static",
		"static_peers": ["magic-kingdom.local", "flower-garden"],
		"ssh": {
			"sync_key": "/Users/jesse/.config/clipfan/ssh/sync_ed25519",
			"known_hosts": "/Users/jesse/.config/clipfan/ssh/known_hosts",
			"peers": [
				{"id":"magic-kingdom","enabled":true,"accept":true,"connect":false,"migration_state":"loopback_unprovisioned","future_peer":{"keep":true}},
				{"id":"flower-garden","enabled":true,"accept":true,"connect":false,"migration_state":"loopback_unprovisioned"}
			],
			"future_ssh": {"keep": true}
		},
		"future_top": {"keep": true}
	}`)

	result, err := removeHostWithGate(path, true, "magic-kingdom", HostRemoveRequest{
		ExpectedRevisionState:  RevisionStateVersioned,
		ExpectedConfigRevision: uint64Ptr(7),
		Reason:                 "user_deleted",
		LogID:                  "peer-log-1780257600",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.RemovedStaticPeer || !result.RemovedSSHPeer {
		t.Fatalf("result removed static/ssh = %v/%v, want true/true", result.RemovedStaticPeer, result.RemovedSSHPeer)
	}
	if result.ConfigRevision == nil || *result.ConfigRevision != 8 {
		t.Fatalf("result revision = %#v, want 8", result.ConfigRevision)
	}
	if result.SSHCleanupStatus == nil || result.SSHCleanupStatus["status"] == "" {
		t.Fatalf("cleanup status = %#v, want populated", result.SSHCleanupStatus)
	}

	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_version"], 2)
	assertJSONNumber(t, after["config_revision"], 8)
	assertJSONValueEqual(t, []any{"flower-garden"}, after["static_peers"])
	assertJSONValueEqual(t, map[string]any{"keep": true}, after["future_top"])

	ssh := after["ssh"].(map[string]any)
	assertJSONValueEqual(t, map[string]any{"keep": true}, ssh["future_ssh"])
	peers := ssh["peers"].([]any)
	if len(peers) != 1 || peers[0].(map[string]any)["id"] != "flower-garden" {
		t.Fatalf("ssh peers = %#v, want only flower-garden", peers)
	}
	audit := ssh["audit_log"].([]any)
	if len(audit) != 1 {
		t.Fatalf("audit_log = %#v, want one entry", audit)
	}
	entry := audit[0].(map[string]any)
	assertJSONValueEqual(t, "magic-kingdom", entry["peer_id"])
	assertJSONValueEqual(t, "user_deleted", entry["reason"])
	assertJSONValueEqual(t, "peer-log-1780257600", entry["log_id"])
}

func TestRemoveHostRemovesLegacyStaticPeerFromPreV2Config(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"shared_key": "k",
		"hostname": "m4",
		"discovery": "static",
		"static_peers": ["jesse-magic-kingdom", "flower-garden"],
		"future_top": {"keep": true}
	}`)

	result, err := removeHostWithGate(path, true, "jesse-magic-kingdom", HostRemoveRequest{
		ExpectedRevisionState: RevisionStatePreV2,
		Reason:                "user_deleted",
		LogID:                 "peer-log-1780257600",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.RemovedStaticPeer || result.RemovedSSHPeer {
		t.Fatalf("result removed static/ssh = %v/%v, want true/false", result.RemovedStaticPeer, result.RemovedSSHPeer)
	}
	if result.ConfigRevision == nil || *result.ConfigRevision != 1 {
		t.Fatalf("result revision = %#v, want 1", result.ConfigRevision)
	}

	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_version"], 2)
	assertJSONNumber(t, after["config_revision"], 1)
	assertJSONValueEqual(t, []any{"flower-garden"}, after["static_peers"])
	assertJSONValueEqual(t, map[string]any{"keep": true}, after["future_top"])
}

func TestRemoveHostAllowsLongStaticPeerFQDN(t *testing.T) {
	longFQDN := "very-long-static-peer-name-that-exceeds-host-id-limit.example.com"
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "k",
		"hostname": "m4",
		"static_peers": ["`+longFQDN+`", "flower-garden"]
	}`)

	result, err := removeHostWithGate(path, true, longFQDN, HostRemoveRequest{
		ExpectedRevisionState:  RevisionStateVersioned,
		ExpectedConfigRevision: uint64Ptr(7),
		Reason:                 "user_deleted",
		LogID:                  "peer-log-1780257600",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.RemovedStaticPeer || result.RemovedSSHPeer {
		t.Fatalf("result removed static/ssh = %v/%v, want true/false", result.RemovedStaticPeer, result.RemovedSSHPeer)
	}

	after := readJSONMap(t, path)
	assertJSONValueEqual(t, []any{"flower-garden"}, after["static_peers"])
}

func TestRemoveHostDoesNotRemoveSuffixAliasCollisions(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "k",
		"hostname": "m4",
		"transport": "ssh",
		"static_peers": ["prod01", "db-prod01", "flower-garden"],
		"ssh": {
			"peers": [
				{"id":"prod01","enabled":true,"accept":true,"migration_state":"loopback_unprovisioned"},
				{"id":"db-prod01","enabled":true,"accept":true,"migration_state":"loopback_unprovisioned"},
				{"id":"flower-garden","enabled":true,"accept":true,"migration_state":"loopback_unprovisioned"}
			]
		}
	}`)

	result, err := removeHostWithGate(path, true, "prod01", HostRemoveRequest{
		ExpectedRevisionState:  RevisionStateVersioned,
		ExpectedConfigRevision: uint64Ptr(7),
		Reason:                 "user_deleted",
		LogID:                  "peer-log-1780257600",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.RemovedStaticPeer || !result.RemovedSSHPeer {
		t.Fatalf("result removed static/ssh = %v/%v, want true/true", result.RemovedStaticPeer, result.RemovedSSHPeer)
	}

	after := readJSONMap(t, path)
	assertJSONValueEqual(t, []any{"db-prod01", "flower-garden"}, after["static_peers"])
	ssh := after["ssh"].(map[string]any)
	peers := ssh["peers"].([]any)
	gotPeerIDs := make([]any, 0, len(peers))
	for _, rawPeer := range peers {
		gotPeerIDs = append(gotPeerIDs, rawPeer.(map[string]any)["id"])
	}
	assertJSONValueEqual(t, []any{"db-prod01", "flower-garden"}, gotPeerIDs)
	audit := ssh["audit_log"].([]any)
	if len(audit) != 1 {
		t.Fatalf("audit_log = %#v, want one entry", audit)
	}
	entry := audit[0].(map[string]any)
	assertJSONValueEqual(t, "prod01", entry["peer_id"])
	assertJSONValueEqual(t, "user_deleted", entry["reason"])
	assertJSONValueEqual(t, "peer-log-1780257600", entry["log_id"])
}

func TestRemoveHostRejectsNotFoundStaleRevisionAndDisabledGateWithoutWriting(t *testing.T) {
	cases := []struct {
		name       string
		gate       bool
		host       string
		revision   uint64
		wantError  string
		isConflict bool
		isDisabled bool
	}{
		{name: "not found", gate: true, host: "missing", revision: 7, wantError: "host_not_found"},
		{name: "stale revision", gate: true, host: "magic-kingdom", revision: 6, isConflict: true},
		{name: "disabled gate", gate: false, host: "magic-kingdom", revision: 7, isDisabled: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfigForV2Test(t, `{
				"config_version": 2,
				"config_revision": 7,
				"shared_key": "k",
				"hostname": "m4",
				"discovery": "static",
				"static_peers": ["magic-kingdom"]
			}`)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			_, err = removeHostWithGate(path, tc.gate, tc.host, HostRemoveRequest{
				ExpectedRevisionState:  RevisionStateVersioned,
				ExpectedConfigRevision: uint64Ptr(tc.revision),
				Reason:                 "user_deleted",
				LogID:                  "peer-log-1780257600",
			})
			switch {
			case tc.isConflict:
				if !errors.Is(err, ErrConfigRevisionConflict) {
					t.Fatalf("error = %v, want ErrConfigRevisionConflict", err)
				}
			case tc.isDisabled:
				if !errors.Is(err, ErrConfigV2WritesDisabled) {
					t.Fatalf("error = %v, want ErrConfigV2WritesDisabled", err)
				}
			case tc.wantError != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error = %v, want %s", err, tc.wantError)
				}
			default:
				t.Fatalf("unexpected test case")
			}

			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Fatalf("failed remove changed config\nbefore: %s\nafter: %s", before, after)
			}
		})
	}
}

func TestDecodeHostRemoveRequestRejectsInvalidInput(t *testing.T) {
	req, err := DecodeHostRemoveRequest(strings.NewReader(`{"expected_revision_state":"versioned","expected_config_revision":7,"reason":"user_deleted","log_id":"peer-log-1780257600"}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.ExpectedRevisionState != RevisionStateVersioned || req.ExpectedConfigRevision == nil || *req.ExpectedConfigRevision != 7 || req.Reason != "user_deleted" || req.LogID != "peer-log-1780257600" {
		t.Fatalf("request = %#v, want decoded", req)
	}

	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "unknown field", body: `{"expected_revision_state":"versioned","expected_config_revision":7,"reason":"user_deleted","log_id":"peer-log-1780257600","future":true}`, want: "unknown_field"},
		{name: "missing state", body: `{"expected_config_revision":7,"reason":"user_deleted","log_id":"peer-log-1780257600"}`, want: "missing_host_remove_field: expected_revision_state"},
		{name: "versioned missing revision", body: `{"expected_revision_state":"versioned","reason":"user_deleted","log_id":"peer-log-1780257600"}`, want: "config_revision_conflict"},
		{name: "pre v2 with revision", body: `{"expected_revision_state":"pre_v2","expected_config_revision":7,"reason":"user_deleted","log_id":"peer-log-1780257600"}`, want: "config_revision_conflict"},
		{name: "bad reason", body: `{"expected_revision_state":"versioned","expected_config_revision":7,"reason":"user deleted","log_id":"peer-log-1780257600"}`, want: "invalid_host_remove_field: reason"},
		{name: "missing log id", body: `{"expected_revision_state":"versioned","expected_config_revision":7,"reason":"user_deleted"}`, want: "missing_host_remove_field: log_id"},
		{name: "trailing json", body: `{"expected_revision_state":"versioned","expected_config_revision":7,"reason":"user_deleted","log_id":"peer-log-1780257600"} {}`, want: "malformed_host_remove_request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeHostRemoveRequest(strings.NewReader(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %s", err, tc.want)
			}
		})
	}
}
