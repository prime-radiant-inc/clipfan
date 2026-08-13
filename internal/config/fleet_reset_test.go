//go:build !windows

package config

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestResetLocalFleetWritesLoopbackSingleHostConfigV2(t *testing.T) {
	stateDir := t.TempDir()
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "not-base64",
		"listen": "127.0.0.1:9000",
		"discovery": "tailscale",
		"static_peers": ["old-remote"],
		"hostname": "m4",
		"max_history": 50,
		"future_top": {"keep": true}
	}`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	backupPath := LocalFleetResetBackupPath(path, time.Date(2026, 6, 2, 20, 30, 0, 0, time.UTC))
	newKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))

	result, err := resetLocalFleetWithGate(path, stateDir, true, LocalFleetResetRequest{
		Confirmation:           LocalFleetResetConfirmation,
		ExpectedRevisionState:  RevisionStateVersioned,
		ExpectedConfigRevision: uint64Ptr(7),
	}, backupPath, func() (string, error) { return "unused", nil }, func() string { return newKey })
	if err != nil {
		t.Fatal(err)
	}
	if result.HostID != "m4" || result.ConfigRevision == nil || *result.ConfigRevision != 8 || result.BackupPath != backupPath {
		t.Fatalf("result = %#v, want host m4 revision 8 backup", result)
	}

	backup, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backup, before) {
		t.Fatalf("backup differs from original\nbackup: %s\nbefore: %s", backup, before)
	}
	assertMode(t, backupPath, 0o600)

	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_version"], 2)
	assertJSONNumber(t, after["config_revision"], 8)
	assertJSONValueEqual(t, newKey, after["shared_key"])
	assertJSONValueEqual(t, "127.0.0.1:9000", after["listen"])
	assertJSONNumber(t, after["port"], 9000)
	assertJSONValueEqual(t, "static", after["discovery"])
	assertJSONValueEqual(t, "m4", after["hostname"])
	assertJSONNumber(t, after["max_history"], 50)
	assertJSONValueEqual(t, map[string]any{"keep": true}, after["future_top"])
	for _, field := range []string{"static_peers", "previous_listen"} {
		if _, ok := after[field]; ok {
			t.Fatalf("reset preserved %s in %#v", field, after)
		}
	}
}

func TestResetLocalFleetDerivesHostAndStartsRevisionOneForPreV2(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"shared_key": "",
		"listen": ":7853",
		"static_peers": ["old-remote"],
		"max_history": 50
	}`)
	newKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x24}, 32))

	result, err := resetLocalFleetWithGate(path, t.TempDir(), true, LocalFleetResetRequest{
		Confirmation:          LocalFleetResetConfirmation,
		ExpectedRevisionState: RevisionStatePreV2,
	}, path+".fleet-reset.bak", func() (string, error) { return "derived-host", nil }, func() string { return newKey })
	if err != nil {
		t.Fatal(err)
	}
	if result.HostID != "derived-host" || result.ConfigRevision == nil || *result.ConfigRevision != 1 {
		t.Fatalf("result = %#v, want derived host revision 1", result)
	}

	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_version"], 2)
	assertJSONNumber(t, after["config_revision"], 1)
	assertJSONValueEqual(t, "derived-host", after["hostname"])
	assertJSONValueEqual(t, "127.0.0.1:7853", after["listen"])
	assertJSONValueEqual(t, "static", after["discovery"])
	if _, ok := after["static_peers"]; ok {
		t.Fatalf("reset kept static_peers: %#v", after["static_peers"])
	}
}

func TestResetLocalFleetGeneratedGateFalseFailsClosedWithoutWriting(t *testing.T) {
	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"","listen":"127.0.0.1:7853","max_history":50}`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	_, err = resetLocalFleetWithGate(path, t.TempDir(), false, LocalFleetResetRequest{
		Confirmation:           LocalFleetResetConfirmation,
		ExpectedRevisionState:  RevisionStateVersioned,
		ExpectedConfigRevision: uint64Ptr(7),
	}, path+".bak", func() (string, error) { return "m4", nil }, func() string { return NewSharedKey() })
	if !errors.Is(err, ErrConfigV2WritesDisabled) {
		t.Fatalf("error = %v, want ErrConfigV2WritesDisabled", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("disabled reset changed config\nbefore: %s\nafter: %s", before, after)
	}
	if _, err := os.Lstat(path + ".bak"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup stat err = %v, want not exist", err)
	}
}

func TestResetLocalFleetRequiresConfirmation(t *testing.T) {
	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"","listen":"127.0.0.1:7853","max_history":50}`)

	_, err := resetLocalFleetWithGate(path, t.TempDir(), true, LocalFleetResetRequest{
		Confirmation:           "reset please",
		ExpectedRevisionState:  RevisionStateVersioned,
		ExpectedConfigRevision: uint64Ptr(7),
	}, path+".bak", func() (string, error) { return "m4", nil }, func() string { return NewSharedKey() })
	if !errors.Is(err, ErrFleetResetConfirmationRequired) {
		t.Fatalf("error = %v, want ErrFleetResetConfirmationRequired", err)
	}
}

func TestResetLocalFleetRejectsUnsafeListenerBeforeMutation(t *testing.T) {
	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"","listen":"0.0.0.0:9000","max_history":50}`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	_, err = resetLocalFleetWithGate(path, t.TempDir(), true, LocalFleetResetRequest{
		Confirmation:           LocalFleetResetConfirmation,
		ExpectedRevisionState:  RevisionStateVersioned,
		ExpectedConfigRevision: uint64Ptr(7),
	}, path+".bak", func() (string, error) { return "m4", nil }, func() string { return NewSharedKey() })
	if !errors.Is(err, ErrFleetResetUnsafeListener) {
		t.Fatalf("error = %v, want ErrFleetResetUnsafeListener", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("unsafe-listener reset changed config\nbefore: %s\nafter: %s", before, after)
	}
}

func TestResetLocalFleetRejectsValidExistingSharedKey(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x33}, 32))
	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"`+key+`","listen":"127.0.0.1:7853","max_history":50}`)

	_, err := resetLocalFleetWithGate(path, t.TempDir(), true, LocalFleetResetRequest{
		Confirmation:           LocalFleetResetConfirmation,
		ExpectedRevisionState:  RevisionStateVersioned,
		ExpectedConfigRevision: uint64Ptr(7),
	}, path+".bak", func() (string, error) { return "m4", nil }, func() string { return NewSharedKey() })
	if !errors.Is(err, ErrFleetResetSharedKeyStillValid) {
		t.Fatalf("error = %v, want ErrFleetResetSharedKeyStillValid", err)
	}
}

func TestResetLocalFleetRejectsSSHMaterialAndState(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		configPath string
		statePath  string
	}{
		{
			name: "ssh peers",
			body: `{"config_version":2,"config_revision":7,"shared_key":"","listen":"127.0.0.1:7853","ssh":{"peers":[{"id":"p1"}]}}`,
		},
		{
			name: "ssh known hosts",
			body: `{"config_version":2,"config_revision":7,"shared_key":"","listen":"127.0.0.1:7853","ssh":{"known_hosts":"/tmp/known_hosts"}}`,
		},
		{
			name: "sync key path",
			body: `{"config_version":2,"config_revision":7,"shared_key":"","listen":"127.0.0.1:7853","sync_key_path":"/tmp/key"}`,
		},
		{
			name: "dedicated known hosts",
			body: `{"config_version":2,"config_revision":7,"shared_key":"","listen":"127.0.0.1:7853","known_hosts_path":"/tmp/known_hosts"}`,
		},
		{
			name: "managed authorized keys metadata",
			body: `{"config_version":2,"config_revision":7,"shared_key":"","listen":"127.0.0.1:7853","managed_authorized_keys":{"path":"/tmp/authorized_keys"}}`,
		},
		{
			name: "ssh transport marker",
			body: `{"config_version":2,"config_revision":7,"shared_key":"","listen":"127.0.0.1:7853","transport":"ssh"}`,
		},
		{
			name:      "transport current file",
			body:      `{"config_version":2,"config_revision":7,"shared_key":"","listen":"127.0.0.1:7853"}`,
			statePath: filepath.Join("ssh", "transport-current"),
		},
		{
			name:       "default config known hosts",
			body:       `{"config_version":2,"config_revision":7,"shared_key":"","listen":"127.0.0.1:7853"}`,
			configPath: filepath.Join("ssh", "known_hosts"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stateDir := t.TempDir()
			if tc.statePath != "" {
				full := filepath.Join(stateDir, tc.statePath)
				if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte("state"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			path := writeConfigForV2Test(t, tc.body)
			if tc.configPath != "" {
				full := filepath.Join(filepath.Dir(path), tc.configPath)
				if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte("material"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			status, err := ReadLocalFleetResetStatus(path, stateDir)
			if err != nil {
				t.Fatal(err)
			}
			if status.SSHMaterialAbsent {
				t.Fatalf("status missed SSH material for %s", tc.name)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			_, err = resetLocalFleetWithGate(path, stateDir, true, LocalFleetResetRequest{
				Confirmation:           LocalFleetResetConfirmation,
				ExpectedRevisionState:  RevisionStateVersioned,
				ExpectedConfigRevision: uint64Ptr(7),
			}, path+".bak", func() (string, error) { return "m4", nil }, func() string { return NewSharedKey() })
			if !errors.Is(err, ErrFleetResetSSHMaterialPresent) {
				t.Fatalf("error = %v, want ErrFleetResetSSHMaterialPresent", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Fatalf("SSH-material reset changed config\nbefore: %s\nafter: %s", before, after)
			}
		})
	}
}

func TestResetLocalFleetRegeneratesConfiguredSyncKey(t *testing.T) {
	keyPath, _ := createSyncKeyForHost(t, "old-host", "OLD-PRIVATE-MATERIAL", "old public")
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "",
		"listen": "127.0.0.1:7853",
		"hostname": "m4",
		"ssh": {"sync_key": "`+jsonStringForTest(keyPath)+`"},
		"future_top": {"keep": true}
	}`)
	status, err := ReadLocalFleetResetStatus(path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !status.SSHMaterialAbsent {
		t.Fatalf("status treated configured sync key as unsupported SSH material: %#v", status)
	}
	newSharedKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x77}, 32))
	newPublicKey := "ssh-ed25519 " + base64.StdEncoding.EncodeToString([]byte("new public")) + " clipfan:m4"

	result, err := resetLocalFleetWithGateAndDeps(path, t.TempDir(), true, LocalFleetResetRequest{
		Confirmation:           LocalFleetResetConfirmation,
		ExpectedRevisionState:  RevisionStateVersioned,
		ExpectedConfigRevision: uint64Ptr(7),
	}, path+".bak", localFleetResetDeps{
		deriveHostID:     func() (string, error) { return "unused", nil },
		newSharedKey:     func() string { return newSharedKey },
		syncKeyChecker:   localSyncKeyChecker(),
		syncKeyGenerator: fakeSyncKeyGenerator("NEW-PRIVATE-MATERIAL", newPublicKey),
		now:              func() time.Time { return time.Date(2026, 6, 3, 9, 10, 11, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprintf("%+v", result), "OLD-PRIVATE-MATERIAL") || strings.Contains(fmt.Sprintf("%+v", result), "NEW-PRIVATE-MATERIAL") {
		t.Fatalf("reset result disclosed private key material: %+v", result)
	}

	loaded, err := LoadLocalSyncKey(SyncKeyLoadOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PublicKey != newPublicKey || loaded.Metadata.HostID != "m4" || loaded.Metadata.CreatedAt != "2026-06-03T09:10:11Z" {
		t.Fatalf("loaded regenerated key = %#v", loaded)
	}
	if got := readFile(t, keyPath); got != "NEW-PRIVATE-MATERIAL" {
		t.Fatalf("private key = %q, want regenerated material", got)
	}

	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 8)
	assertJSONValueEqual(t, map[string]any{"keep": true}, after["future_top"])
	ssh, ok := after["ssh"].(map[string]any)
	if !ok {
		t.Fatalf("ssh = %#v, want object", after["ssh"])
	}
	assertJSONValueEqual(t, keyPath, ssh["sync_key"])
}

func TestResetLocalFleetRestoresSyncKeyWhenBackupWriteFails(t *testing.T) {
	keyPath, _ := createSyncKeyForHost(t, "m4", "OLD-PRIVATE-MATERIAL", "old public")
	oldPrivate := readBytes(t, keyPath)
	oldPublic := readBytes(t, keyPath+".pub")
	oldSidecar := readBytes(t, keyPath+".clipfan.json")
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "",
		"listen": "127.0.0.1:7853",
		"hostname": "m4",
		"ssh": {"sync_key": "`+jsonStringForTest(keyPath)+`"}
	}`)
	beforeConfig := readBytes(t, path)
	backupPath := path + ".bak"
	if err := os.WriteFile(backupPath, []byte("existing backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	newPublicKey := "ssh-ed25519 " + base64.StdEncoding.EncodeToString([]byte("new public")) + " clipfan:m4"

	_, err := resetLocalFleetWithGateAndDeps(path, t.TempDir(), true, LocalFleetResetRequest{
		Confirmation:           LocalFleetResetConfirmation,
		ExpectedRevisionState:  RevisionStateVersioned,
		ExpectedConfigRevision: uint64Ptr(7),
	}, backupPath, localFleetResetDeps{
		deriveHostID:     func() (string, error) { return "unused", nil },
		newSharedKey:     func() string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x7c}, 32)) },
		syncKeyChecker:   localSyncKeyChecker(),
		syncKeyGenerator: fakeSyncKeyGenerator("NEW-PRIVATE-MATERIAL", newPublicKey),
	})
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("error = %v, want backup exists error", err)
	}
	if !bytes.Equal(readBytes(t, path), beforeConfig) {
		t.Fatalf("failed reset changed config\nbefore: %s\nafter: %s", beforeConfig, readBytes(t, path))
	}
	if got := readBytes(t, keyPath); !bytes.Equal(got, oldPrivate) {
		t.Fatalf("private key changed after rollback\nbefore: %q\nafter: %q", oldPrivate, got)
	}
	if got := readBytes(t, keyPath+".pub"); !bytes.Equal(got, oldPublic) {
		t.Fatalf("public key changed after rollback\nbefore: %q\nafter: %q", oldPublic, got)
	}
	if got := readBytes(t, keyPath+".clipfan.json"); !bytes.Equal(got, oldSidecar) {
		t.Fatalf("sidecar changed after rollback\nbefore: %s\nafter: %s", oldSidecar, got)
	}
	assertNoSyncKeyResetScratch(t, keyPath)
	if strings.Contains(fmt.Sprint(err), "NEW-PRIVATE-MATERIAL") || strings.Contains(fmt.Sprint(err), "OLD-PRIVATE-MATERIAL") {
		t.Fatalf("error disclosed private material: %v", err)
	}
}

func TestResetLocalFleetRegeneratesDefaultSyncKeyAndPersistsPath(t *testing.T) {
	path := writeFleetResetConfigForSyncKeyTest(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "",
		"listen": "127.0.0.1:7853",
		"hostname": "m4"
	}`)
	keyPath := filepath.Join(filepath.Dir(path), "ssh", "sync_ed25519")
	createSyncKeyAtPathForHost(t, keyPath, "old-host", "OLD-PRIVATE-MATERIAL", "old public")
	status, err := ReadLocalFleetResetStatus(path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !status.SSHMaterialAbsent {
		t.Fatalf("status treated default sync key as unsupported SSH material: %#v", status)
	}
	newPublicKey := "ssh-ed25519 " + base64.StdEncoding.EncodeToString([]byte("default new public")) + " clipfan:m4"

	_, err = resetLocalFleetWithGateAndDeps(path, t.TempDir(), true, LocalFleetResetRequest{
		Confirmation:           LocalFleetResetConfirmation,
		ExpectedRevisionState:  RevisionStateVersioned,
		ExpectedConfigRevision: uint64Ptr(7),
	}, path+".bak", localFleetResetDeps{
		deriveHostID:     func() (string, error) { return "unused", nil },
		newSharedKey:     func() string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x78}, 32)) },
		syncKeyChecker:   localSyncKeyChecker(),
		syncKeyGenerator: fakeSyncKeyGenerator("NEW-PRIVATE-MATERIAL", newPublicKey),
	})
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadLocalSyncKey(SyncKeyLoadOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PublicKey != newPublicKey || strings.Contains(fmt.Sprintf("%+v", loaded), "NEW-PRIVATE-MATERIAL") {
		t.Fatalf("loaded regenerated default key = %#v", loaded)
	}
	after := readJSONMap(t, path)
	ssh, ok := after["ssh"].(map[string]any)
	if !ok {
		t.Fatalf("ssh = %#v, want object with sync_key", after["ssh"])
	}
	assertJSONValueEqual(t, keyPath, ssh["sync_key"])
}

func TestResetLocalFleetRejectsDefaultSyncKeyWhenCustomSyncKeyConfigured(t *testing.T) {
	customKeyPath := filepath.Join(syncKeyTempDir(t), "custom", "sync_ed25519")
	path := writeFleetResetConfigForSyncKeyTest(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "",
		"listen": "127.0.0.1:7853",
		"hostname": "m4",
		"ssh": {"sync_key": "`+jsonStringForTest(customKeyPath)+`"}
	}`)
	defaultKeyPath := filepath.Join(filepath.Dir(path), "ssh", "sync_ed25519")
	createSyncKeyAtPathForHost(t, defaultKeyPath, "old-host", "OLD-PRIVATE-MATERIAL", "old public")
	beforeDefaultPrivate := readFile(t, defaultKeyPath)
	status, err := ReadLocalFleetResetStatus(path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if status.SSHMaterialAbsent {
		t.Fatalf("status ignored default sync key material with custom configured path: %#v", status)
	}

	_, err = resetLocalFleetWithGateAndDeps(path, t.TempDir(), true, LocalFleetResetRequest{
		Confirmation:           LocalFleetResetConfirmation,
		ExpectedRevisionState:  RevisionStateVersioned,
		ExpectedConfigRevision: uint64Ptr(7),
	}, path+".bak", localFleetResetDeps{
		deriveHostID:     func() (string, error) { return "unused", nil },
		newSharedKey:     func() string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x79}, 32)) },
		syncKeyChecker:   localSyncKeyChecker(),
		syncKeyGenerator: fakeSyncKeyGenerator("NEW-PRIVATE-MATERIAL", "ssh-ed25519 "+base64.StdEncoding.EncodeToString([]byte("new"))+" clipfan:m4"),
	})
	if !errors.Is(err, ErrFleetResetSSHMaterialPresent) {
		t.Fatalf("error = %v, want ErrFleetResetSSHMaterialPresent", err)
	}
	if got := readFile(t, defaultKeyPath); got != beforeDefaultPrivate {
		t.Fatalf("default private key changed from %q to %q", beforeDefaultPrivate, got)
	}
	if _, err := os.Lstat(customKeyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("custom key stat err = %v, want not exist", err)
	}
}

func TestResetLocalFleetCreatesMissingConfiguredSyncKey(t *testing.T) {
	keyPath := filepath.Join(syncKeyTempDir(t), "missing", "sync_ed25519")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "",
		"listen": "127.0.0.1:7853",
		"hostname": "m4",
		"ssh": {"sync_key": "`+jsonStringForTest(keyPath)+`"}
	}`)
	newPublicKey := "ssh-ed25519 " + base64.StdEncoding.EncodeToString([]byte("created public")) + " clipfan:m4"

	_, err := resetLocalFleetWithGateAndDeps(path, t.TempDir(), true, LocalFleetResetRequest{
		Confirmation:           LocalFleetResetConfirmation,
		ExpectedRevisionState:  RevisionStateVersioned,
		ExpectedConfigRevision: uint64Ptr(7),
	}, path+".bak", localFleetResetDeps{
		deriveHostID:     func() (string, error) { return "unused", nil },
		newSharedKey:     func() string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x7a}, 32)) },
		syncKeyChecker:   localSyncKeyChecker(),
		syncKeyGenerator: fakeSyncKeyGenerator("NEW-PRIVATE-MATERIAL", newPublicKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadLocalSyncKey(SyncKeyLoadOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PublicKey != newPublicKey || strings.Contains(fmt.Sprintf("%+v", loaded), "NEW-PRIVATE-MATERIAL") {
		t.Fatalf("loaded created key = %#v", loaded)
	}
}

func TestResetLocalFleetRejectsPersistedInvalidSyncKeyPathBeforeRotation(t *testing.T) {
	keyPath := filepath.Join(syncKeyTempDir(t), "Application Support", "clipfan", "ssh", "sync_ed25519")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "",
		"listen": "127.0.0.1:7853",
		"hostname": "m4",
		"ssh": {"sync_key": "`+jsonStringForTest(keyPath)+`"}
	}`)

	_, err := resetLocalFleetWithGateAndDeps(path, t.TempDir(), true, LocalFleetResetRequest{
		Confirmation:           LocalFleetResetConfirmation,
		ExpectedRevisionState:  RevisionStateVersioned,
		ExpectedConfigRevision: uint64Ptr(7),
	}, path+".bak", localFleetResetDeps{
		deriveHostID:     func() (string, error) { return "unused", nil },
		newSharedKey:     func() string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x7b}, 32)) },
		syncKeyChecker:   localSyncKeyChecker(),
		syncKeyGenerator: fakeSyncKeyGenerator("NEW-PRIVATE-MATERIAL", "ssh-ed25519 "+base64.StdEncoding.EncodeToString([]byte("new"))+" clipfan:m4"),
	})
	if err == nil || !strings.Contains(err.Error(), "invalid_sync_key") {
		t.Fatalf("error = %v, want invalid_sync_key", err)
	}
	if strings.Contains(fmt.Sprint(err), "NEW-PRIVATE-MATERIAL") {
		t.Fatalf("error disclosed private material: %v", err)
	}
	for _, path := range syncKeyMaterialPaths(keyPath) {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Lstat(%s) err = %v, want not exist", path, err)
		}
	}
}

func TestResetLocalFleetRejectsUnparseableUnsafeModeAndHeldLock(t *testing.T) {
	t.Run("unparseable", func(t *testing.T) {
		path := writeConfigForV2Test(t, `{`)
		_, err := resetLocalFleetWithGate(path, t.TempDir(), true, LocalFleetResetRequest{
			Confirmation:          LocalFleetResetConfirmation,
			ExpectedRevisionState: RevisionStatePreV2,
		}, path+".bak", func() (string, error) { return "m4", nil }, func() string { return NewSharedKey() })
		if err == nil {
			t.Fatal("reset succeeded for unparseable config")
		}
	})

	t.Run("unsafe mode", func(t *testing.T) {
		path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"","listen":"127.0.0.1:7853"}`)
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := resetLocalFleetWithGate(path, t.TempDir(), true, LocalFleetResetRequest{
			Confirmation:           LocalFleetResetConfirmation,
			ExpectedRevisionState:  RevisionStateVersioned,
			ExpectedConfigRevision: uint64Ptr(7),
		}, path+".bak", func() (string, error) { return "m4", nil }, func() string { return NewSharedKey() })
		if !errors.Is(err, ErrConfigFileUnsafe) {
			t.Fatalf("error = %v, want ErrConfigFileUnsafe", err)
		}
	})

	t.Run("held lock", func(t *testing.T) {
		path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"","listen":"127.0.0.1:7853"}`)
		lockFile, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer lockFile.Close()
		if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			t.Fatal(err)
		}
		defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

		_, err = resetLocalFleetWithGate(path, t.TempDir(), true, LocalFleetResetRequest{
			Confirmation:           LocalFleetResetConfirmation,
			ExpectedRevisionState:  RevisionStateVersioned,
			ExpectedConfigRevision: uint64Ptr(7),
		}, path+".bak", func() (string, error) { return "m4", nil }, func() string { return NewSharedKey() })
		if !errors.Is(err, ErrConfigLockHeld) {
			t.Fatalf("error = %v, want ErrConfigLockHeld", err)
		}
	})
}

func TestResetLocalFleetLeavesAuthorizedKeysUntouched(t *testing.T) {
	root := t.TempDir()
	authorizedKeys := filepath.Join(root, "authorized_keys")
	body := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA old\n"
	if err := os.WriteFile(authorizedKeys, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "",
		"listen": "127.0.0.1:7853",
		"future_authorized_keys_note": "`+strings.ReplaceAll(authorizedKeys, `\`, `\\`)+`"
	}`)

	_, err := resetLocalFleetWithGate(path, t.TempDir(), true, LocalFleetResetRequest{
		Confirmation:           LocalFleetResetConfirmation,
		ExpectedRevisionState:  RevisionStateVersioned,
		ExpectedConfigRevision: uint64Ptr(7),
	}, path+".bak", func() (string, error) { return "m4", nil }, func() string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x55}, 32)) })
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(authorizedKeys)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Fatalf("authorized_keys changed to %q, want %q", after, body)
	}
}

func createSyncKeyForHost(t *testing.T, hostID, privateKey, publicBlob string) (string, SyncKeyCreateResult) {
	t.Helper()
	keyPath := filepath.Join(syncKeyTempDir(t), "sync_ed25519")
	return keyPath, createSyncKeyAtPathForHost(t, keyPath, hostID, privateKey, publicBlob)
}

func createSyncKeyAtPathForHost(t *testing.T, keyPath, hostID, privateKey, publicBlob string) SyncKeyCreateResult {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	publicKey := "ssh-ed25519 " + base64.StdEncoding.EncodeToString([]byte(publicBlob)) + " clipfan:" + hostID
	result, err := CreateLocalSyncKey(SyncKeyCreateOptions{
		KeyPath:   keyPath,
		HostID:    hostID,
		Checker:   localSyncKeyChecker(),
		Now:       func() time.Time { return time.Date(2026, 6, 1, 12, 34, 56, 0, time.UTC) },
		Generator: fakeSyncKeyGenerator(privateKey, publicKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func jsonStringForTest(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

func writeFleetResetConfigForSyncKeyTest(t *testing.T, body string) string {
	t.Helper()
	dir := filepath.Join(syncKeyTempDir(t), "clipfan")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
