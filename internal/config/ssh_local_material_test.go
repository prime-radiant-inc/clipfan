package config

import (
	"errors"
	"testing"
)

const testStandardSharedKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

func TestReadConfigRevisionReturnsVersionedStatus(t *testing.T) {
	t.Parallel()

	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"k","hostname":"m4","max_history":50}`)

	status, err := ReadConfigRevision(path)
	if err != nil {
		t.Fatalf("ReadConfigRevision error = %v", err)
	}
	if status.RevisionState != RevisionStateVersioned {
		t.Fatalf("RevisionState = %q, want versioned", status.RevisionState)
	}
	if status.ConfigVersion == nil || *status.ConfigVersion != 2 {
		t.Fatalf("ConfigVersion = %v, want 2", status.ConfigVersion)
	}
	if status.ConfigRevision == nil || *status.ConfigRevision != 7 {
		t.Fatalf("ConfigRevision = %v, want 7", revisionString(status.ConfigRevision))
	}
}

func TestUpdateSSHLocalMaterialPreservesRawSSHAndIncrementsRevision(t *testing.T) {
	t.Parallel()

	path := writeConfigForV2Test(t, `{
  "config_version": 2,
  "config_revision": 7,
  "shared_key": "k",
  "hostname": "m4",
  "transport": "ssh",
  "max_history": 50,
  "ssh": {
    "sync_key": "/Users/jesse/.config/clipfan/ssh/old_sync_ed25519",
    "known_hosts": "/Users/jesse/.config/clipfan/ssh/old_known_hosts",
    "future_ssh": {"keep": true},
    "peers": [
      {
        "id": "fsck",
        "enabled": true,
        "accept": true,
        "migration_state": "loopback_unprovisioned",
        "future_peer": {"keep": true}
      }
    ]
  }
}`)

	transport := TransportSSH
	status, err := updateSSHLocalMaterialWithGate(path, true, SSHLocalMaterialUpdateRequest{
		ExpectedConfigRevision: uint64Ptr(7),
		Transport:              &transport,
		SyncKey:                stringPtr("/Users/jesse/.config/clipfan/ssh/sync_ed25519"),
		KnownHosts:             stringPtr("/Users/jesse/.config/clipfan/ssh/known_hosts"),
	})
	if err != nil {
		t.Fatalf("UpdateSSHLocalMaterial error = %v", err)
	}
	if status.ConfigRevision == nil || *status.ConfigRevision != 8 {
		t.Fatalf("ConfigRevision = %v, want 8", revisionString(status.ConfigRevision))
	}

	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 8)
	assertJSONValueEqual(t, TransportSSH, after["transport"])
	ssh := after["ssh"].(map[string]any)
	assertJSONValueEqual(t, "/Users/jesse/.config/clipfan/ssh/sync_ed25519", ssh["sync_key"])
	assertJSONValueEqual(t, "/Users/jesse/.config/clipfan/ssh/known_hosts", ssh["known_hosts"])
	assertJSONValueEqual(t, map[string]any{"keep": true}, ssh["future_ssh"])
	peer := ssh["peers"].([]any)[0].(map[string]any)
	assertJSONValueEqual(t, map[string]any{"keep": true}, peer["future_peer"])
}

func TestUpdateSSHLocalMaterialTransportOnlyEnablesSSHMode(t *testing.T) {
	t.Parallel()

	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"k","hostname":"m4","max_history":50,"future_top":{"keep":true}}`)

	transport := TransportSSH
	status, err := updateSSHLocalMaterialWithGate(path, true, SSHLocalMaterialUpdateRequest{
		ExpectedConfigRevision: uint64Ptr(7),
		Transport:              &transport,
	})
	if err != nil {
		t.Fatalf("UpdateSSHLocalMaterial error = %v", err)
	}
	if status.ConfigRevision == nil || *status.ConfigRevision != 8 {
		t.Fatalf("ConfigRevision = %v, want 8", revisionString(status.ConfigRevision))
	}
	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 8)
	assertJSONValueEqual(t, TransportSSH, after["transport"])
	assertJSONValueEqual(t, map[string]any{"keep": true}, after["future_top"])
}

func TestUpdateSSHLocalMaterialSeedsSharedKeyAndPreservesUnknownFields(t *testing.T) {
	t.Parallel()

	path := writeConfigForV2Test(t, `{
  "config_version": 2,
  "config_revision": 7,
  "shared_key": "",
  "hostname": "m4",
  "transport": "ssh",
  "max_history": 50,
  "future_top": {"keep": true},
  "ssh": {
    "future_ssh": {"keep": true}
  }
}`)

	status, err := updateSSHLocalMaterialWithGate(path, true, SSHLocalMaterialUpdateRequest{
		ExpectedConfigRevision: uint64Ptr(7),
		SharedKey:              stringPtr(testStandardSharedKey),
	})
	if err != nil {
		t.Fatalf("UpdateSSHLocalMaterial error = %v", err)
	}
	if status.ConfigRevision == nil || *status.ConfigRevision != 8 {
		t.Fatalf("ConfigRevision = %v, want 8", revisionString(status.ConfigRevision))
	}

	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 8)
	assertJSONValueEqual(t, testStandardSharedKey, after["shared_key"])
	assertJSONValueEqual(t, map[string]any{"keep": true}, after["future_top"])
	ssh := after["ssh"].(map[string]any)
	assertJSONValueEqual(t, map[string]any{"keep": true}, ssh["future_ssh"])
}

func TestUpdateSSHLocalMaterialFailsClosedWhenGateDisabled(t *testing.T) {
	t.Parallel()

	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"k","hostname":"m4","transport":"ssh","max_history":50}`)

	_, err := updateSSHLocalMaterialWithGate(path, false, SSHLocalMaterialUpdateRequest{
		ExpectedConfigRevision: uint64Ptr(7),
		SyncKey:                stringPtr("/Users/jesse/.config/clipfan/ssh/sync_ed25519"),
	})
	if !errors.Is(err, ErrConfigV2WritesDisabled) {
		t.Fatalf("error = %v, want ErrConfigV2WritesDisabled", err)
	}
	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 7)
}

func TestUpdateSSHLocalMaterialRejectsStaleRevisionWithoutWriting(t *testing.T) {
	t.Parallel()

	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"k","hostname":"m4","transport":"ssh","max_history":50}`)

	_, err := updateSSHLocalMaterialWithGate(path, true, SSHLocalMaterialUpdateRequest{
		ExpectedConfigRevision: uint64Ptr(6),
		KnownHosts:             stringPtr("/Users/jesse/.config/clipfan/ssh/known_hosts"),
	})
	if !errors.Is(err, ErrConfigRevisionConflict) {
		t.Fatalf("error = %v, want ErrConfigRevisionConflict", err)
	}
	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 7)
}

func TestUpdateSSHLocalMaterialRejectsInvalidPathWithoutWriting(t *testing.T) {
	t.Parallel()

	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"k","hostname":"m4","transport":"ssh","max_history":50}`)

	_, err := updateSSHLocalMaterialWithGate(path, true, SSHLocalMaterialUpdateRequest{
		ExpectedConfigRevision: uint64Ptr(7),
		KnownHosts:             stringPtr("/Users/jesse/.config/clipfan/ssh/known hosts"),
	})
	if err == nil {
		t.Fatal("error = nil, want invalid path error")
	}
	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 7)
}

func TestUpdateSSHLocalMaterialRejectsInvalidSharedKeyWithoutWriting(t *testing.T) {
	t.Parallel()

	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"k","hostname":"m4","transport":"ssh","max_history":50}`)

	_, err := updateSSHLocalMaterialWithGate(path, true, SSHLocalMaterialUpdateRequest{
		ExpectedConfigRevision: uint64Ptr(7),
		SharedKey:              stringPtr("not-base64"),
	})
	if err == nil || err.Error() != "invalid_shared_key" {
		t.Fatalf("error = %v, want invalid_shared_key", err)
	}
	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 7)
	assertJSONValueEqual(t, "k", after["shared_key"])
}
