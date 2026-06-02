package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestReadListenerRepairStatusReportsSafeModeFields(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 4,
		"listen": "bad-listen",
		"port": 70000,
		"previous_listen": "0.0.0.0:9000",
		"shared_key": "k"
	}`)

	status, err := ReadListenerRepairStatus(path)
	if err != nil {
		t.Fatal(err)
	}
	if status.Listen != "bad-listen" {
		t.Fatalf("Listen = %q, want bad-listen", status.Listen)
	}
	if status.Port != 70000 {
		t.Fatalf("Port = %d, want 70000", status.Port)
	}
	if status.PreviousListen == nil || *status.PreviousListen != "0.0.0.0:9000" {
		t.Fatalf("PreviousListen = %v, want 0.0.0.0:9000", status.PreviousListen)
	}
	if status.ConfiguredListen != "bad-listen" {
		t.Fatalf("ConfiguredListen = %q, want bad-listen", status.ConfiguredListen)
	}
	if status.EffectiveRepairListen != "127.0.0.1:7853" {
		t.Fatalf("EffectiveRepairListen = %q, want 127.0.0.1:7853", status.EffectiveRepairListen)
	}
	if status.ParseError != "invalid_listen_port" {
		t.Fatalf("ParseError = %q, want invalid_listen_port", status.ParseError)
	}
	if !status.SafeMode {
		t.Fatal("SafeMode = false, want true")
	}
	if status.ConfigVersion == nil || *status.ConfigVersion != 2 {
		t.Fatalf("ConfigVersion = %v, want 2", status.ConfigVersion)
	}
	if status.ConfigRevision == nil || *status.ConfigRevision != 4 {
		t.Fatalf("ConfigRevision = %v, want 4", revisionString(status.ConfigRevision))
	}
	if status.RevisionState != RevisionStateVersioned {
		t.Fatalf("RevisionState = %q, want versioned", status.RevisionState)
	}
}

func TestReadListenerRepairStatusRejectsUnsafeConfigMode(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 4,
		"listen": "0.0.0.0:9000",
		"port": 9000,
		"shared_key": "k"
	}`)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadListenerRepairStatus(path)
	if !errors.Is(err, ErrConfigFileUnsafe) {
		t.Fatalf("ReadListenerRepairStatus error = %v, want ErrConfigFileUnsafe", err)
	}
}

func TestRepairListenerWritesRevisionOneForPreV2AndMissingRevision(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		expected RevisionState
		listen   string
		port     int
		previous string
	}{
		{
			name:     "pre v2",
			body:     `{"shared_key":"k","listen":"0.0.0.0:9000","max_history":50,"transport":"ssh","ssh":{"peers":[{"id":"p1"}]},"private_key_path":"/secret/key","known_hosts_path":"/secret/known_hosts","authorized_keys_path":"/secret/authorized_keys","future_top":{"drop":true}}`,
			expected: RevisionStatePreV2,
			listen:   "127.0.0.1:9000",
			port:     9000,
			previous: "0.0.0.0:9000",
		},
		{
			name:     "missing revision",
			body:     `{"config_version":2,"shared_key":"k","listen":":9001","max_history":50,"transport":"ssh","ssh":{"peers":[{"id":"p1"}]},"private_key_path":"/secret/key","known_hosts_path":"/secret/known_hosts","authorized_keys_path":"/secret/authorized_keys","future_top":{"drop":true}}`,
			expected: RevisionStateMissingRevision,
			listen:   "127.0.0.1:9001",
			port:     9001,
			previous: ":9001",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfigForV2Test(t, tc.body)

			_, err := repairListenerWithGate(path, true, ListenerRepairRequest{
				ExpectedRevisionState: tc.expected,
				Listen:                tc.listen,
				Port:                  tc.port,
				PreviousListen:        &tc.previous,
			})
			if err != nil {
				t.Fatal(err)
			}

			after := readJSONMap(t, path)
			assertJSONNumber(t, after["config_version"], 2)
			assertJSONNumber(t, after["config_revision"], 1)
			assertJSONValueEqual(t, tc.listen, after["listen"])
			assertJSONNumber(t, after["port"], int64(tc.port))
			assertJSONValueEqual(t, tc.previous, after["previous_listen"])
			assertJSONValueEqual(t, "k", after["shared_key"])
			assertJSONNumber(t, after["max_history"], 50)
			for _, field := range []string{"transport", "ssh", "private_key_path", "known_hosts_path", "authorized_keys_path", "future_top"} {
				if _, ok := after[field]; ok {
					t.Fatalf("repair preserved unrevisioned field %q in %#v", field, after)
				}
			}
		})
	}
}

func TestRepairListenerIncrementsVersionedRevision(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "k",
		"listen": "203.0.113.10:49152",
		"max_history": 50
	}`)
	previous := "203.0.113.10:49152"

	status, err := repairListenerWithGate(path, true, ListenerRepairRequest{
		ExpectedRevisionState:  RevisionStateVersioned,
		ExpectedConfigRevision: uint64Ptr(7),
		Listen:                 "127.0.0.1:49152",
		Port:                   49152,
		PreviousListen:         &previous,
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.ConfigRevision == nil || *status.ConfigRevision != 8 {
		t.Fatalf("returned ConfigRevision = %v, want 8", revisionString(status.ConfigRevision))
	}
	if status.SafeMode {
		t.Fatal("returned SafeMode = true after repair")
	}

	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 8)
	assertJSONValueEqual(t, "127.0.0.1:49152", after["listen"])
	assertJSONNumber(t, after["port"], 49152)
	assertJSONValueEqual(t, previous, after["previous_listen"])
}

func TestRepairListenerPreservesUnknownV2Fields(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 11,
		"shared_key": "k",
		"listen": "[::]:9002",
		"max_history": 50,
		"future_top": {"nested": [1, true, {"x": "y"}]},
		"ssh": {"peers": [{"id": "p1", "future": {"keep": true}}]}
	}`)
	before := readJSONMap(t, path)
	previous := "[::]:9002"

	_, err := repairListenerWithGate(path, true, ListenerRepairRequest{
		ExpectedRevisionState:  RevisionStateVersioned,
		ExpectedConfigRevision: uint64Ptr(11),
		Listen:                 "127.0.0.1:9002",
		Port:                   9002,
		PreviousListen:         &previous,
	})
	if err != nil {
		t.Fatal(err)
	}

	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 12)
	assertJSONValueEqual(t, before["future_top"], after["future_top"])
	assertJSONValueEqual(t, before["ssh"], after["ssh"])
	assertJSONValueEqual(t, "k", after["shared_key"])
	assertJSONNumber(t, after["max_history"], 50)
}

func TestRepairListenerRejectsStaleRevisionWithoutWriting(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "k",
		"listen": "0.0.0.0:9000",
		"max_history": 50
	}`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	previous := "0.0.0.0:9000"

	_, err = repairListenerWithGate(path, true, ListenerRepairRequest{
		ExpectedRevisionState:  RevisionStateVersioned,
		ExpectedConfigRevision: uint64Ptr(6),
		Listen:                 "127.0.0.1:9000",
		Port:                   9000,
		PreviousListen:         &previous,
	})
	if !errors.Is(err, ErrConfigRevisionConflict) {
		t.Fatalf("error = %v, want ErrConfigRevisionConflict", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("stale repair changed file\nbefore: %s\nafter: %s", before, after)
	}
}

func TestRepairListenerRejectsUnsafeConfigModeWithoutWriting(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "k",
		"listen": "0.0.0.0:9000",
		"max_history": 50
	}`)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	previous := "0.0.0.0:9000"

	_, err = repairListenerWithGate(path, true, ListenerRepairRequest{
		ExpectedRevisionState:  RevisionStateVersioned,
		ExpectedConfigRevision: uint64Ptr(7),
		Listen:                 "127.0.0.1:9000",
		Port:                   9000,
		PreviousListen:         &previous,
	})
	if !errors.Is(err, ErrConfigFileUnsafe) {
		t.Fatalf("repairListenerWithGate error = %v, want ErrConfigFileUnsafe", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("unsafe repair changed file\nbefore: %s\nafter: %s", before, after)
	}
}

func TestRepairListenerGeneratedGateFalseFailsClosedWithoutWriting(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"shared_key": "k",
		"listen": "0.0.0.0:9000",
		"max_history": 50
	}`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	previous := "0.0.0.0:9000"

	_, err = RepairListener(path, ListenerRepairRequest{
		ExpectedRevisionState: RevisionStatePreV2,
		Listen:                "127.0.0.1:9000",
		Port:                  9000,
		PreviousListen:        &previous,
	})
	if !errors.Is(err, ErrConfigV2WritesDisabled) {
		t.Fatalf("RepairListener error = %v, want ErrConfigV2WritesDisabled", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("disabled repair changed file\nbefore: %s\nafter: %s", before, after)
	}
}

func TestRepairListenerWithBackupWritesOriginalBeforeRepair(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 5,
		"shared_key": "k",
		"listen": "0.0.0.0:9000",
		"max_history": 50,
		"future_top": {"keep": true}
	}`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	backupPath := path + ".listener-repair.20260602T203000Z.bak"
	previous := "0.0.0.0:9000"

	_, err = repairListenerWithBackupAndGate(path, true, ListenerRepairRequest{
		ExpectedRevisionState:  RevisionStateVersioned,
		ExpectedConfigRevision: uint64Ptr(5),
		Listen:                 "127.0.0.1:9000",
		Port:                   9000,
		PreviousListen:         &previous,
	}, backupPath)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backup, before) {
		t.Fatalf("backup differs from original\nbackup: %s\nbefore: %s", backup, before)
	}
	info, err := os.Lstat(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("backup mode = %o, want 600", got)
	}
	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 6)
	assertJSONValueEqual(t, "127.0.0.1:9000", after["listen"])
	assertJSONValueEqual(t, map[string]any{"keep": true}, after["future_top"])
}

func TestRepairListenerBackupFailureLeavesConfigUnchanged(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 5,
		"shared_key": "k",
		"listen": "0.0.0.0:9000",
		"max_history": 50
	}`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	backupPath := path + ".existing.bak"
	if err := os.WriteFile(backupPath, []byte("already exists"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := "0.0.0.0:9000"

	_, err = repairListenerWithBackupAndGate(path, true, ListenerRepairRequest{
		ExpectedRevisionState:  RevisionStateVersioned,
		ExpectedConfigRevision: uint64Ptr(5),
		Listen:                 "127.0.0.1:9000",
		Port:                   9000,
		PreviousListen:         &previous,
	}, backupPath)
	if err == nil {
		t.Fatal("repair succeeded despite backup path collision")
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("backup failure changed file\nbefore: %s\nafter: %s", before, after)
	}
}

func TestDecodeListenerRepairRequestAcceptsStrictListenerFields(t *testing.T) {
	req, err := DecodeListenerRepairRequest(strings.NewReader(`{
		"expected_config_revision": 7,
		"expected_revision_state": "versioned",
		"listen": "127.0.0.1:9000",
		"port": 9000,
		"previous_listen": "0.0.0.0:9000"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.ExpectedConfigRevision == nil || *req.ExpectedConfigRevision != 7 {
		t.Fatalf("ExpectedConfigRevision = %v, want 7", revisionString(req.ExpectedConfigRevision))
	}
	if req.ExpectedRevisionState != RevisionStateVersioned {
		t.Fatalf("ExpectedRevisionState = %q, want versioned", req.ExpectedRevisionState)
	}
	if req.Listen != "127.0.0.1:9000" || req.Port != 9000 {
		t.Fatalf("listen/port = %q/%d, want 127.0.0.1:9000/9000", req.Listen, req.Port)
	}
	if req.PreviousListen == nil || *req.PreviousListen != "0.0.0.0:9000" {
		t.Fatalf("PreviousListen = %v, want 0.0.0.0:9000", req.PreviousListen)
	}
}

func TestDecodeListenerRepairRequestRejectsMalformedBodies(t *testing.T) {
	for _, body := range []string{`{`, `[]`, `null`} {
		t.Run(body, func(t *testing.T) {
			_, err := DecodeListenerRepairRequest(strings.NewReader(body))
			if err == nil || !strings.Contains(err.Error(), "malformed_listener_repair_request") {
				t.Fatalf("error = %v, want malformed_listener_repair_request", err)
			}
		})
	}
}

func TestDecodeListenerRepairRequestRejectsForbiddenAndUnknownFields(t *testing.T) {
	for _, field := range []string{
		"shared_key",
		"static_peers",
		"ssh",
		"private_key_path",
		"known_hosts_path",
		"authorized_keys_path",
		"future_top",
	} {
		t.Run(field, func(t *testing.T) {
			body := fmt.Sprintf(`{
				"expected_config_revision": null,
				"expected_revision_state": "pre_v2",
				"listen": "127.0.0.1:9000",
				"port": 9000,
				%q: "forbidden"
			}`, field)
			_, err := DecodeListenerRepairRequest(strings.NewReader(body))
			if err == nil || !strings.Contains(err.Error(), "listener_repair_field_not_allowed: "+field) {
				t.Fatalf("error = %v, want field rejection for %s", err, field)
			}
		})
	}
}
