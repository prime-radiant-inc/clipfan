package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
)

func TestParseConfigDocumentRevisionStates(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		state RevisionState
		rev   *uint64
	}{
		{
			name:  "pre v2",
			body:  `{"shared_key":"k","max_history":50}`,
			state: RevisionStatePreV2,
		},
		{
			name:  "v2 missing revision",
			body:  `{"config_version":2,"shared_key":"k","max_history":50}`,
			state: RevisionStateMissingRevision,
		},
		{
			name:  "v2 null revision",
			body:  `{"config_version":2,"config_revision":null,"shared_key":"k","max_history":50}`,
			state: RevisionStateMissingRevision,
		},
		{
			name:  "v2 versioned",
			body:  `{"config_version":2,"config_revision":7,"shared_key":"k","max_history":50}`,
			state: RevisionStateVersioned,
			rev:   uint64Ptr(7),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := parseConfigDocument([]byte(tc.body))
			if err != nil {
				t.Fatalf("parseConfigDocument: %v", err)
			}
			if doc.RevisionState != tc.state {
				t.Fatalf("RevisionState = %q, want %q", doc.RevisionState, tc.state)
			}
			if !sameRevision(doc.ConfigRevision, tc.rev) {
				t.Fatalf("ConfigRevision = %v, want %v", revisionString(doc.ConfigRevision), revisionString(tc.rev))
			}
			if doc.Config.SharedKey != "k" {
				t.Fatalf("SharedKey = %q, want k", doc.Config.SharedKey)
			}
		})
	}
}

func TestParseConfigDocumentRejectsInvalidConfigVersion(t *testing.T) {
	cases := []struct {
		name string
		body string
		code string
	}{
		{"null", `{"config_version":null}`, "invalid_config_version"},
		{"string", `{"config_version":"2"}`, "invalid_config_version"},
		{"float", `{"config_version":2.1}`, "invalid_config_version"},
		{"boolean", `{"config_version":true}`, "invalid_config_version"},
		{"array", `{"config_version":[]}`, "invalid_config_version"},
		{"object", `{"config_version":{}}`, "invalid_config_version"},
		{"unsupported one", `{"config_version":1}`, "unsupported_config_version"},
		{"unsupported three", `{"config_version":3}`, "unsupported_config_version"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseConfigDocument([]byte(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error = %v, want code %s", err, tc.code)
			}
		})
	}
}

func TestParseConfigDocumentRejectsInvalidConfigRevision(t *testing.T) {
	cases := []string{
		`0`,
		`-1`,
		`1.2`,
		`"1"`,
		`true`,
		`[]`,
		`{}`,
		`18446744073709551616`,
	}

	for _, revision := range cases {
		t.Run(revision, func(t *testing.T) {
			body := []byte(`{"config_version":2,"config_revision":` + revision + `}`)
			_, err := parseConfigDocument(body)
			if err == nil || !strings.Contains(err.Error(), "invalid_config_revision") {
				t.Fatalf("error = %v, want invalid_config_revision", err)
			}
		})
	}
}

func TestSaveV1DoesNotWriteConfigV2Metadata(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)

	if err := Save(&Config{SharedKey: NewSharedKey(), MaxHistory: 75}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "clipfan", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("config_version")) || bytes.Contains(data, []byte("config_revision")) {
		t.Fatalf("v1 save wrote v2 metadata: %s", data)
	}
}

func TestSaveRejectsConstructedConfigV2WhenGeneratedGateFalse(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	version := 2
	revision := uint64(1)

	err := Save(&Config{
		ConfigVersion:  &version,
		ConfigRevision: &revision,
		SharedKey:      NewSharedKey(),
		MaxHistory:     75,
	})
	if !errors.Is(err, ErrConfigV2WritesDisabled) {
		t.Fatalf("Save error = %v, want ErrConfigV2WritesDisabled", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "clipfan", "config.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("config file stat error = %v, want not exist", statErr)
	}
}

func TestSaveRejectsConstructedConfigRevisionWithoutVersionWhenGeneratedGateFalse(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	revision := uint64(1)

	err := Save(&Config{
		ConfigRevision: &revision,
		SharedKey:      NewSharedKey(),
		MaxHistory:     75,
	})
	if !errors.Is(err, ErrConfigV2WritesDisabled) {
		t.Fatalf("Save error = %v, want ErrConfigV2WritesDisabled", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "clipfan", "config.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("config file stat error = %v, want not exist", statErr)
	}
}

func TestSaveRejectsUnsupportedConstructedConfigVersion(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	version := 3

	err := Save(&Config{
		ConfigVersion: &version,
		SharedKey:     NewSharedKey(),
		MaxHistory:    75,
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported_config_version") {
		t.Fatalf("Save error = %v, want unsupported_config_version", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "clipfan", "config.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("config file stat error = %v, want not exist", statErr)
	}
}

func TestUpdateConfigV2ScopedPreservesUnknownFieldsAndIncrementsRevision(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "k",
		"max_history": 50,
		"future_top": {"nested": [1, true, {"x": "y"}]},
		"ssh": {"peers": [{"id": "p1", "future": {"keep": true}}]}
	}`)
	before := readJSONMap(t, path)

	err := updateConfigV2ScopedWithGate(path, true, RevisionExpectation{
		State:    RevisionStateVersioned,
		Revision: uint64Ptr(7),
	}, func(c *Config) error {
		c.MaxHistory = 300
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 8)
	assertJSONNumber(t, after["max_history"], 300)
	assertJSONValueEqual(t, before["future_top"], after["future_top"])
	assertJSONValueEqual(t, before["ssh"], after["ssh"])
}

func TestParseConfigDocumentAcceptsSSHTransportSchema(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	doc, err := parseConfigDocument([]byte(`{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "k",
		"hostname": "m4",
		"transport": "ssh",
		"static_peers": ["legacy-suggestion"],
		"ssh": {
			"sync_key": "~/.config/clipfan/ssh/sync_ed25519",
			"known_hosts": "~/.config/clipfan/ssh/known_hosts",
			"max_sessions": 16,
			"max_sessions_per_peer": 2,
			"log_limit_bytes": 65536,
			"peers": [{
				"id": "fsck",
				"ssh_host": "fsck.com",
				"ssh_user": "jesse",
				"ssh_port": 22,
				"install_path": "/home/jesse/.local/bin/clipfan",
				"gateway_path": "/home/jesse/.local/bin/clipfan",
				"enabled": true,
				"accept": false,
				"connect": true,
				"persistent": true,
				"on_demand": true,
				"migration_state": "ssh_keys_ready",
				"proof": {
					"connect_key_id": "b5b5b5b5b5b5b5b5",
					"connect_gateway_path": "/home/jesse/.local/bin/clipfan",
					"connect_verified_at": "2026-06-01T12:35:10Z",
					"connect_verified_by": "regular_ssh"
				}
			}]
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Config.Transport != TransportSSH {
		t.Fatalf("Transport = %q, want ssh", doc.Config.Transport)
	}
	if doc.Config.SSH == nil || len(doc.Config.SSH.Peers) != 1 {
		t.Fatalf("SSH peers = %#v, want one peer", doc.Config.SSH)
	}
	if doc.Config.SSH.SyncKey != filepath.Join(home, ".config/clipfan/ssh/sync_ed25519") {
		t.Fatalf("SyncKey = %q, want expanded home path", doc.Config.SSH.SyncKey)
	}
	if doc.Config.SSH.KnownHosts != filepath.Join(home, ".config/clipfan/ssh/known_hosts") {
		t.Fatalf("KnownHosts = %q, want expanded home path", doc.Config.SSH.KnownHosts)
	}
	if doc.Config.SSH.Peers[0].MigrationState != MigrationStateSSHKeysReady {
		t.Fatalf("MigrationState = %q", doc.Config.SSH.Peers[0].MigrationState)
	}
	if len(doc.Config.StaticPeers) != 1 || doc.Config.StaticPeers[0] != "legacy-suggestion" {
		t.Fatalf("StaticPeers = %#v, want legacy suggestion preserved separately", doc.Config.StaticPeers)
	}
}

func TestParseConfigDocumentRejectsInvalidSSHTransportSchema(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cases := []struct {
		name string
		body string
		code string
	}{
		{
			name: "invalid transport",
			body: `{"config_version":2,"config_revision":7,"transport":"http","shared_key":"k"}`,
			code: "invalid_transport",
		},
		{
			name: "non object ssh",
			body: `{"config_version":2,"config_revision":7,"transport":"ssh","ssh":[]}`,
			code: "cannot unmarshal",
		},
		{
			name: "duplicate peer id",
			body: `{"config_version":2,"config_revision":7,"transport":"ssh","ssh":{"peers":[{"id":"fsck"},{"id":"fsck"}]}}`,
			code: "duplicate_ssh_peer_id",
		},
		{
			name: "peer id equals host",
			body: `{"config_version":2,"config_revision":7,"hostname":"m4","transport":"ssh","ssh":{"peers":[{"id":"m4"}]}}`,
			code: "ssh_peer_id_is_local_host",
		},
		{
			name: "unsafe sync key",
			body: `{"config_version":2,"config_revision":7,"transport":"ssh","ssh":{"sync_key":"~/clip fan"}}`,
			code: "invalid_sync_key",
		},
		{
			name: "relative known hosts",
			body: `{"config_version":2,"config_revision":7,"transport":"ssh","ssh":{"known_hosts":"clipfan/known_hosts"}}`,
			code: "invalid_known_hosts",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseConfigDocument([]byte(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
		})
	}
}

func TestUpdateConfigV2ScopedPreservesSSHTransportFields(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "k",
		"max_history": 50,
		"transport": "ssh",
		"ssh": {
			"peers": [{
				"id": "fsck",
				"enabled": true,
				"accept": false,
				"connect": true,
				"ssh_host": "fsck.com",
				"ssh_user": "jesse",
				"ssh_port": 22,
				"install_path": "/home/jesse/.local/bin/clipfan",
				"gateway_path": "/home/jesse/.local/bin/clipfan",
				"migration_state": "ssh_material_staged",
				"proof": {
					"connect_key_id": "b5b5b5b5b5b5b5b5",
					"connect_gateway_path": "/home/jesse/.local/bin/clipfan",
					"connect_verified_at": "2026-06-01T12:35:10Z",
					"connect_verified_by": "regular_ssh"
				},
				"future_peer_field": {"keep": true}
			}]
		}
	}`)
	before := readJSONMap(t, path)

	err := updateConfigV2ScopedWithGate(path, true, RevisionExpectation{
		State:    RevisionStateVersioned,
		Revision: uint64Ptr(7),
	}, func(c *Config) error {
		c.MaxHistory = 75
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 8)
	assertJSONNumber(t, after["max_history"], 75)
	assertJSONValueEqual(t, before["transport"], after["transport"])
	assertJSONValueEqual(t, before["ssh"], after["ssh"])
}

func TestUpdateConfigV2ScopedPersistsTypedSSHTransportChanges(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "k",
		"transport": "ssh",
		"ssh": {"peers": [{"id": "fsck", "future_peer_field": {"keep": true}}]}
	}`)

	err := updateConfigV2ScopedWithGate(path, true, RevisionExpectation{
		State:    RevisionStateVersioned,
		Revision: uint64Ptr(7),
	}, func(c *Config) error {
		c.SSH = &SSHConfig{Peers: []SSHPeer{{
			ID:             "fsck",
			Enabled:        true,
			Accept:         true,
			MigrationState: MigrationStateLoopbackUnprovisioned,
		}}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 8)
	assertJSONValueEqual(t, map[string]any{"peers": []any{map[string]any{
		"id":              "fsck",
		"enabled":         true,
		"accept":          true,
		"migration_state": string(MigrationStateLoopbackUnprovisioned),
		"proof":           map[string]any{},
	}}}, after["ssh"])
}

func TestUpdateConfigV2ScopedPersistsInPlaceSSHMutation(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "k",
		"transport": "ssh",
		"ssh": {"max_sessions": 1}
	}`)

	err := updateConfigV2ScopedWithGate(path, true, RevisionExpectation{
		State:    RevisionStateVersioned,
		Revision: uint64Ptr(7),
	}, func(c *Config) error {
		c.SSH.MaxSessions = 2
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 8)
	assertJSONValueEqual(t, map[string]any{"max_sessions": 2.0}, after["ssh"])
}

func TestUpdateConfigV2ScopedRejectsInvalidActiveSSHMutationWithoutWriting(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "k",
		"ssh": {"peers": [{"id": "fsck", "migration_state": "future"}]}
	}`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	err = updateConfigV2ScopedWithGate(path, true, RevisionExpectation{
		State:    RevisionStateVersioned,
		Revision: uint64Ptr(7),
	}, func(c *Config) error {
		c.Transport = TransportSSH
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "invalid_migration_state") {
		t.Fatalf("error = %v, want invalid_migration_state", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("invalid ssh mutation changed config\nbefore: %s\nafter: %s", before, after)
	}
}

func TestDormantNewClientScopedConfigUpdatePreservesConfigV2Fields(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 9,
		"shared_key": "k",
		"max_history": 50,
		"future_listener": {"mode": "safe"},
		"ssh": {"peers": [{"id": "p1", "migration_state": "future", "future": {"keep": true}}]}
	}`)

	err := updateConfigV2ScopedWithGate(path, true, RevisionExpectation{
		State:    RevisionStateVersioned,
		Revision: uint64Ptr(9),
	}, func(c *Config) error {
		c.MaxHistory = 75
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 10)
	assertJSONNumber(t, after["max_history"], 75)
	if _, ok := after["shared_key"]; !ok {
		t.Fatal("scoped update dropped shared_key")
	}
	assertJSONValueEqual(t, map[string]any{"mode": "safe"}, after["future_listener"])
	assertJSONValueEqual(t, map[string]any{"peers": []any{map[string]any{"id": "p1", "migration_state": "future", "future": map[string]any{"keep": true}}}}, after["ssh"])
}

func TestUpdateConfigV2ScopedFirstWritesStoreRevisionOne(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		expected RevisionExpectation
	}{
		{
			name: "pre v2",
			body: `{"shared_key":"k","max_history":50}`,
			expected: RevisionExpectation{
				State: RevisionStatePreV2,
			},
		},
		{
			name: "missing revision",
			body: `{"config_version":2,"shared_key":"k","max_history":50}`,
			expected: RevisionExpectation{
				State: RevisionStateMissingRevision,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfigForV2Test(t, tc.body)
			err := updateConfigV2ScopedWithGate(path, true, tc.expected, func(c *Config) error {
				c.MaxHistory = 125
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			after := readJSONMap(t, path)
			assertJSONNumber(t, after["config_version"], 2)
			assertJSONNumber(t, after["config_revision"], 1)
			assertJSONNumber(t, after["max_history"], 125)
		})
	}
}

func TestUpdateConfigV2ScopedRejectsStaleRevisionWithoutWriting(t *testing.T) {
	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"k","max_history":50}`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	err = updateConfigV2ScopedWithGate(path, true, RevisionExpectation{
		State:    RevisionStateVersioned,
		Revision: uint64Ptr(6),
	}, func(c *Config) error {
		c.MaxHistory = 300
		return nil
	})
	if !errors.Is(err, ErrConfigRevisionConflict) {
		t.Fatalf("error = %v, want ErrConfigRevisionConflict", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("stale update changed file\nbefore: %s\nafter: %s", before, after)
	}
}

func TestUpdateConfigV2ScopedRejectsRevisionOverflowWithoutWriting(t *testing.T) {
	const maxUint64 = ^uint64(0)
	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":18446744073709551615,"shared_key":"k","max_history":50}`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	err = updateConfigV2ScopedWithGate(path, true, RevisionExpectation{
		State:    RevisionStateVersioned,
		Revision: uint64Ptr(maxUint64),
	}, func(c *Config) error {
		c.MaxHistory = 300
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "invalid_config_revision") {
		t.Fatalf("error = %v, want invalid_config_revision", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("overflow update changed file\nbefore: %s\nafter: %s", before, after)
	}
}

func TestUpdateConfigV2ScopedRejectsInvalidExpectations(t *testing.T) {
	path := writeConfigForV2Test(t, `{"config_version":2,"shared_key":"k","max_history":50}`)
	cases := []RevisionExpectation{
		{State: RevisionStateMissingRevision, Revision: uint64Ptr(0)},
		{State: RevisionStateVersioned},
		{State: RevisionStateVersioned, Revision: uint64Ptr(0)},
		{State: RevisionStatePreV2},
	}

	for _, expected := range cases {
		err := updateConfigV2ScopedWithGate(path, true, expected, func(c *Config) error {
			c.MaxHistory = 300
			return nil
		})
		if !errors.Is(err, ErrConfigRevisionConflict) {
			t.Fatalf("expectation %#v error = %v, want ErrConfigRevisionConflict", expected, err)
		}
	}
}

func TestUpdateConfigV2ScopedDisabledGeneratedGateFailsClosed(t *testing.T) {
	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"k","max_history":50}`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	err = UpdateConfigV2Scoped(path, RevisionExpectation{
		State:    RevisionStateVersioned,
		Revision: uint64Ptr(7),
	}, func(c *Config) error {
		c.MaxHistory = 300
		return nil
	})
	if !errors.Is(err, ErrConfigV2WritesDisabled) {
		t.Fatalf("error = %v, want ErrConfigV2WritesDisabled", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("disabled update changed file\nbefore: %s\nafter: %s", before, after)
	}
}

func TestUpdateConfigV2ScopedPrivateModeAndOwnTempCleanup(t *testing.T) {
	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":1,"shared_key":"k","max_history":50}`)
	dir := filepath.Dir(path)
	ownTemp := filepath.Join(dir, ".config-v2-config.json.tmp.123.1")
	unrelatedTemp := filepath.Join(dir, "config.json.tmp")
	otherWriterTemp := filepath.Join(dir, ".config-v2-other.json.tmp.123.1")
	for _, p := range []string{ownTemp, unrelatedTemp, otherWriterTemp} {
		if err := os.WriteFile(p, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	err := updateConfigV2ScopedWithGate(path, true, RevisionExpectation{
		State:    RevisionStateVersioned,
		Revision: uint64Ptr(1),
	}, func(c *Config) error {
		c.MaxHistory = 500
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	assertMode(t, dir, 0o700)
	assertMode(t, path, 0o600)
	assertMode(t, path+".lock", 0o600)
	if _, err := os.Stat(ownTemp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("own stale temp stat = %v, want removed", err)
	}
	for _, p := range []string{unrelatedTemp, otherWriterTemp} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("unrelated temp %s stat = %v, want kept", p, err)
		}
	}
}

func TestLoadForDaemonStartMigratesGeneratedLegacyListensInMemory(t *testing.T) {
	cases := []string{`:7853`, `0.0.0.0:7853`, `[::]:7853`}

	for _, listen := range cases {
		t.Run(listen, func(t *testing.T) {
			path := writeConfigForV2Test(t, `{"shared_key":"k","listen":"`+listen+`","max_history":50}`)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			cfg, err := loadForDaemonStart(path, ListenerMigrationPolicy{
				GeneratedLoopbackListenEnabled: true,
				ConfigV2WriteEnabled:           false,
			})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Listen != "127.0.0.1:7853" {
				t.Fatalf("Listen = %q, want loopback migration", cfg.Listen)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Fatalf("pre-v2 daemon-start migration wrote file\nbefore: %s\nafter: %s", before, after)
			}
		})
	}
}

func TestLoadForDaemonStartLeavesExplicitNonDefaultWildcardListenUnchanged(t *testing.T) {
	path := writeConfigForV2Test(t, `{"shared_key":"k","listen":":9000","max_history":50}`)

	cfg, err := loadForDaemonStart(path, ListenerMigrationPolicy{
		GeneratedLoopbackListenEnabled: true,
		ConfigV2WriteEnabled:           true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":9000" {
		t.Fatalf("Listen = %q, want explicit non-default wildcard unchanged", cfg.Listen)
	}
	after := readJSONMap(t, path)
	if _, ok := after["config_version"]; ok {
		t.Fatalf("explicit non-default wildcard migration wrote config_version: %#v", after["config_version"])
	}
}

func TestLoadForDaemonStartPersistsVersionedGeneratedListenMigrationWhenGateEnabled(t *testing.T) {
	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 7,
		"shared_key": "k",
		"listen": "0.0.0.0:7853",
		"max_history": 50,
		"future_listener": {"keep": true}
	}`)

	cfg, err := loadForDaemonStart(path, ListenerMigrationPolicy{
		GeneratedLoopbackListenEnabled: true,
		ConfigV2WriteEnabled:           true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:7853" {
		t.Fatalf("Listen = %q, want loopback migration", cfg.Listen)
	}
	if cfg.ConfigRevision == nil || *cfg.ConfigRevision != 8 {
		t.Fatalf("ConfigRevision = %v, want 8", revisionString(cfg.ConfigRevision))
	}
	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_revision"], 8)
	assertJSONValueEqual(t, "127.0.0.1:7853", after["listen"])
	assertJSONValueEqual(t, map[string]any{"keep": true}, after["future_listener"])
}

func TestInternalTestProfilePersistsConfigV2GeneratedListenMigration(t *testing.T) {
	transport, runtime := readInternalTestGateBundle(t)
	if err := releaseflags.ValidateGateBundle(transport, runtime); err != nil {
		t.Fatal(err)
	}
	if !transport.PeerHTTPRuntimeDisabled || !transport.ConfigV2WriteEnabled {
		t.Fatalf("internal-test local gates = peerHTTP:%v configV2:%v, want both true", transport.PeerHTTPRuntimeDisabled, transport.ConfigV2WriteEnabled)
	}
	if transport.RemoteSecretWriteReleaseEnabled || transport.SSHPublicAddPeerSuccessEnabled {
		t.Fatalf("internal-test remote gates enabled early: %+v", transport)
	}

	path := writeConfigForV2Test(t, `{
		"config_version": 2,
		"config_revision": 11,
		"shared_key": "k",
		"listen": ":7853",
		"max_history": 50,
		"future_profile_field": {"keep": true}
	}`)

	cfg, err := loadForDaemonStart(path, ListenerMigrationPolicy{
		GeneratedLoopbackListenEnabled: transport.PeerHTTPRuntimeDisabled && transport.ConfigV2WriteEnabled,
		ConfigV2WriteEnabled:           transport.ConfigV2WriteEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:7853" {
		t.Fatalf("Listen = %q, want loopback migration", cfg.Listen)
	}
	if cfg.ConfigRevision == nil || *cfg.ConfigRevision != 12 {
		t.Fatalf("ConfigRevision = %v, want 12", revisionString(cfg.ConfigRevision))
	}
	after := readJSONMap(t, path)
	assertJSONNumber(t, after["config_version"], 2)
	assertJSONNumber(t, after["config_revision"], 12)
	assertJSONValueEqual(t, "127.0.0.1:7853", after["listen"])
	assertJSONValueEqual(t, map[string]any{"keep": true}, after["future_profile_field"])
}

func TestLoadForDaemonStartDoesNotPersistVersionedGeneratedListenMigrationWhenWriteGateDisabled(t *testing.T) {
	path := writeConfigForV2Test(t, `{"config_version":2,"config_revision":7,"shared_key":"k","listen":"[::]:7853","max_history":50}`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := loadForDaemonStart(path, ListenerMigrationPolicy{
		GeneratedLoopbackListenEnabled: true,
		ConfigV2WriteEnabled:           false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:7853" {
		t.Fatalf("Listen = %q, want loopback in-memory migration", cfg.Listen)
	}
	if cfg.ConfigRevision == nil || *cfg.ConfigRevision != 7 {
		t.Fatalf("ConfigRevision = %v, want 7", revisionString(cfg.ConfigRevision))
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("disabled write gate changed file\nbefore: %s\nafter: %s", before, after)
	}
}

func TestUpdateConfigV2ScopedRejectsSymlinkedConfigFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "clipfan")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target.json")
	before := []byte(`{"config_version":2,"config_revision":1,"shared_key":"k","max_history":50}`)
	if err := os.WriteFile(target, before, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	err := updateConfigV2ScopedWithGate(path, true, RevisionExpectation{
		State:    RevisionStateVersioned,
		Revision: uint64Ptr(1),
	}, func(c *Config) error {
		c.MaxHistory = 300
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "config file") {
		t.Fatalf("error = %v, want symlink rejection", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config path mode = %v, want symlink unchanged", info.Mode())
	}
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("symlink target changed\nbefore: %s\nafter: %s", before, after)
	}
}

func TestUpdateConfigV2ScopedRejectsSymlinkedConfigDirectory(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "clipfan")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(linkDir, "config.json")

	err := updateConfigV2ScopedWithGate(path, true, RevisionExpectation{
		State:    RevisionStateVersioned,
		Revision: uint64Ptr(1),
	}, func(c *Config) error {
		c.MaxHistory = 300
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "config directory") {
		t.Fatalf("error = %v, want symlinked directory rejection", err)
	}
	info, err := os.Lstat(linkDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config directory mode = %v, want symlink unchanged", info.Mode())
	}
}

func TestUpdateConfigV2ScopedRejectsMissingConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clipfan", "config.json")
	err := updateConfigV2ScopedWithGate(path, true, RevisionExpectation{State: RevisionStatePreV2}, func(c *Config) error {
		c.MaxHistory = 300
		return nil
	})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want not exist", err)
	}
}

func writeConfigForV2Test(t *testing.T, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clipfan")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readInternalTestGateBundle(t *testing.T) (releaseflags.TransportGates, releaseflags.RuntimeGates) {
	t.Helper()
	transportFile, err := os.Open(filepath.Join("..", "..", "release", "internal-test", "ssh-transport-gates.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer transportFile.Close()
	runtimeFile, err := os.Open(filepath.Join("..", "..", "release", "internal-test", "ssh-runtime-gates.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeFile.Close()

	transport, err := releaseflags.ReadTransportGates(transportFile)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := releaseflags.ReadRuntimeGates(runtimeFile)
	if err != nil {
		t.Fatal(err)
	}
	return transport, runtime
}

func readJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var out map[string]any
	if err := decoder.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func assertJSONNumber(t *testing.T, value any, want int64) {
	t.Helper()
	num, ok := value.(json.Number)
	if !ok {
		t.Fatalf("value = %#v, want json.Number", value)
	}
	got, err := num.Int64()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("number = %d, want %d", got, want)
	}
}

func assertJSONValueEqual(t *testing.T, want, got any) {
	t.Helper()
	wantData, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	gotData, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wantData, gotData) {
		t.Fatalf("json value mismatch\nwant: %s\ngot:  %s", wantData, gotData)
	}
}

func sameRevision(a, b *uint64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func revisionString(v *uint64) any {
	if v == nil {
		return nil
	}
	return *v
}

func uint64Ptr(n uint64) *uint64 {
	return &n
}
