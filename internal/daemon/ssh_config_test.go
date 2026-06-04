package daemon

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
)

func TestSSHPeerConfigReadIsSignedAndRedacted(t *testing.T) {
	sharedKey := config.NewSharedKey()
	configPath := writeSSHPeerDaemonConfig(t, sharedKey)
	d := newSSHPeerConfigDaemon(t, sharedKey, configPath)

	rec := serveSignedDaemonRequest(t, d, http.MethodGet, "/v1/config/ssh/peers/fsck", "ssh-peer-read", nil, transport.AuthVersionRequestHMAC)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/config/ssh/peers/fsck = %d %q, want 200", rec.Code, rec.Body.String())
	}
	requireDaemonSignedResponse(t, d.auth, rec, "ssh-peer-read")
	payload := decodeDaemonJSONMap(t, rec)
	if payload["config_revision"] != float64(7) || payload["revision_state"] != "versioned" {
		t.Fatalf("read payload revision = %#v", payload)
	}
	peer := payload["peer"].(map[string]any)
	if peer["id"] != "fsck" || peer["ssh_host"] != "fsck.com" {
		t.Fatalf("peer payload = %#v", peer)
	}
	for _, key := range []string{"shared_key", "private_key", "sync_key", "known_hosts"} {
		if _, ok := peer[key]; ok {
			t.Fatalf("peer payload exposed %s: %#v", key, peer)
		}
	}
}

func TestSSHPeerConfigPutFailsClosedWhenConfigV2WritesDisabled(t *testing.T) {
	if releaseflags.ConfigV2WriteEnabled {
		t.Skip("requires generated ConfigV2WriteEnabled=false")
	}
	sharedKey := config.NewSharedKey()
	configPath := writeSSHPeerDaemonConfig(t, sharedKey)
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	d := newSSHPeerConfigDaemon(t, sharedKey, configPath)

	body := []byte(`{"expected_config_revision":7,"peer":{"id":"fsck","enabled":false}}`)
	rec := serveSignedDaemonRequest(t, d, http.MethodPut, "/v1/config/ssh/peers/fsck", "ssh-peer-put-disabled", body, transport.AuthVersionRequestHMAC)
	requireDaemonSignedError(t, d.auth, rec, "ssh-peer-put-disabled", http.StatusServiceUnavailable, "config_v2_writes_disabled")

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("disabled ssh peer PUT changed config\nbefore=%s\nafter=%s", before, after)
	}
}

func TestSSHPeerConfigProofPatchFailsClosedWhenConfigV2WritesDisabled(t *testing.T) {
	if releaseflags.ConfigV2WriteEnabled {
		t.Skip("requires generated ConfigV2WriteEnabled=false")
	}
	sharedKey := config.NewSharedKey()
	configPath := writeSSHPeerDaemonConfig(t, sharedKey)
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	d := newSSHPeerConfigDaemon(t, sharedKey, configPath)

	body := []byte(`{"expected_config_revision":7,"accept_proof":{"key_id":"a4a4a4a4","gateway_path":"/Users/jesse/.local/bin/clipfan","verified_at":"2026-06-01T12:34:56Z","verified_by":"local_file"}}`)
	rec := serveSignedDaemonRequest(t, d, http.MethodPatch, "/v1/config/ssh/peers/fsck/proof", "ssh-peer-proof-disabled", body, transport.AuthVersionRequestHMAC)
	requireDaemonSignedError(t, d.auth, rec, "ssh-peer-proof-disabled", http.StatusServiceUnavailable, "config_v2_writes_disabled")

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("disabled ssh peer proof PATCH changed config\nbefore=%s\nafter=%s", before, after)
	}
}

func TestSSHPeerConfigTransitionFailsClosedWhenConfigV2WritesDisabled(t *testing.T) {
	if releaseflags.ConfigV2WriteEnabled {
		t.Skip("requires generated ConfigV2WriteEnabled=false")
	}
	sharedKey := config.NewSharedKey()
	configPath := writeSSHPeerDaemonConfig(t, sharedKey)
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	d := newSSHPeerConfigDaemon(t, sharedKey, configPath)

	body := []byte(`{"expected_config_revision":7,"from_state":"loopback_unprovisioned","to_state":"ssh_material_staged","reason":"staged","log_id":"peer-log-1780257600"}`)
	rec := serveSignedDaemonRequest(t, d, http.MethodPost, "/v1/config/ssh/peers/fsck/transition", "ssh-peer-transition-disabled", body, transport.AuthVersionRequestHMAC)
	requireDaemonSignedError(t, d.auth, rec, "ssh-peer-transition-disabled", http.StatusServiceUnavailable, "config_v2_writes_disabled")

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("disabled ssh peer transition changed config\nbefore=%s\nafter=%s", before, after)
	}
}

func TestSSHPeerConfigDisableFailsClosedWhenConfigV2WritesDisabled(t *testing.T) {
	if releaseflags.ConfigV2WriteEnabled {
		t.Skip("requires generated ConfigV2WriteEnabled=false")
	}
	sharedKey := config.NewSharedKey()
	configPath := writeSSHPeerDaemonConfig(t, sharedKey)
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	d := newSSHPeerConfigDaemon(t, sharedKey, configPath)

	body := []byte(`{"expected_config_revision":7,"reason":"user_disabled"}`)
	rec := serveSignedDaemonRequest(t, d, http.MethodPost, "/v1/config/ssh/peers/fsck/disable", "ssh-peer-disable-disabled", body, transport.AuthVersionRequestHMAC)
	requireDaemonSignedError(t, d.auth, rec, "ssh-peer-disable-disabled", http.StatusServiceUnavailable, "config_v2_writes_disabled")

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("disabled ssh peer disable changed config\nbefore=%s\nafter=%s", before, after)
	}
}

func TestSSHPeerConfigDeleteFailsClosedWhenConfigV2WritesDisabled(t *testing.T) {
	if releaseflags.ConfigV2WriteEnabled {
		t.Skip("requires generated ConfigV2WriteEnabled=false")
	}
	sharedKey := config.NewSharedKey()
	configPath := writeSSHPeerDaemonConfig(t, sharedKey)
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	d := newSSHPeerConfigDaemon(t, sharedKey, configPath)

	body := []byte(`{"expected_config_revision":7,"reason":"user_deleted","log_id":"peer-log-1780257600"}`)
	rec := serveSignedDaemonRequest(t, d, http.MethodDelete, "/v1/config/ssh/peers/fsck", "ssh-peer-delete-disabled", body, transport.AuthVersionRequestHMAC)
	requireDaemonSignedError(t, d.auth, rec, "ssh-peer-delete-disabled", http.StatusServiceUnavailable, "config_v2_writes_disabled")

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("disabled ssh peer delete changed config\nbefore=%s\nafter=%s", before, after)
	}
}

func TestSSHPeerConfigHandlerErrorMapsValidationFailures(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{
			name:   "invalid ssh host",
			err:    errors.New("invalid_ssh_host: invalid_ssh_host: bad"),
			status: http.StatusBadRequest,
			code:   "invalid_ssh_peer_config",
		},
		{
			name:   "missing connect locator",
			err:    errors.New("missing_connect_locator: fsck"),
			status: http.StatusBadRequest,
			code:   "invalid_ssh_peer_config",
		},
		{
			name:   "duplicate target",
			err:    errors.New("duplicate_ssh_target: one and two"),
			status: http.StatusConflict,
			code:   "ssh_peer_config_conflict",
		},
		{
			name:   "missing create field",
			err:    errors.New("ssh_peer_create_requires_enabled"),
			status: http.StatusBadRequest,
			code:   "invalid_ssh_peer_config",
		},
		{
			name:   "proof mismatch",
			err:    errors.New("proof_mismatch: accept"),
			status: http.StatusConflict,
			code:   "proof_mismatch",
		},
		{
			name:   "invalid proof patch body",
			err:    errors.New("invalid_ssh_peer_proof_patch_field: accept_proof.key_id"),
			status: http.StatusBadRequest,
			code:   "bad_request",
		},
		{
			name:   "invalid transition field body",
			err:    errors.New("invalid_ssh_peer_transition_field: remote_secret_absence_proof.verified_at"),
			status: http.StatusBadRequest,
			code:   "bad_request",
		},
		{
			name:   "invalid disable body",
			err:    errors.New("invalid_ssh_peer_disable_field: reason"),
			status: http.StatusBadRequest,
			code:   "bad_request",
		},
		{
			name:   "invalid delete body",
			err:    errors.New("missing_ssh_peer_delete_field: log_id"),
			status: http.StatusBadRequest,
			code:   "bad_request",
		},
		{
			name:   "malformed stored proof",
			err:    errors.New("invalid_ssh_peer_proof: json: cannot unmarshal string into Go value"),
			status: http.StatusBadRequest,
			code:   "invalid_ssh_peer_config",
		},
		{
			name:   "malformed stored migration log",
			err:    errors.New("invalid_ssh_peer_migration_log: json: cannot unmarshal string into Go value"),
			status: http.StatusBadRequest,
			code:   "invalid_ssh_peer_config",
		},
		{
			name:   "transition state mismatch",
			err:    errors.New("ssh_peer_transition_state_mismatch"),
			status: http.StatusConflict,
			code:   "ssh_peer_transition_state_mismatch",
		},
		{
			name:   "transition not allowed",
			err:    errors.New("ssh_peer_transition_not_allowed: loopback_unprovisioned_to_ssh_keys_ready"),
			status: http.StatusBadRequest,
			code:   "ssh_peer_transition_not_allowed",
		},
		{
			name:   "transition missing proof",
			err:    errors.New("ssh_peer_transition_requires_current_proof: invalid_accept_proof"),
			status: http.StatusBadRequest,
			code:   "invalid_ssh_peer_transition",
		},
		{
			name:   "transition invalid state",
			err:    errors.New("invalid_ssh_peer_transition_state: to_state"),
			status: http.StatusBadRequest,
			code:   "invalid_ssh_peer_transition",
		},
		{
			name:   "transition invalid reason",
			err:    errors.New("invalid_ssh_peer_transition_reason: typo"),
			status: http.StatusBadRequest,
			code:   "invalid_ssh_peer_transition",
		},
		{
			name:   "transition invalid failed phase",
			err:    errors.New("invalid_ssh_peer_transition_failed_phase: remote_shared_key_write"),
			status: http.StatusBadRequest,
			code:   "invalid_ssh_peer_transition",
		},
		{
			name:   "transition absence proof mismatch",
			err:    errors.New("ssh_peer_transition_absence_proof_failed_phase_mismatch"),
			status: http.StatusBadRequest,
			code:   "invalid_ssh_peer_transition",
		},
		{
			name:   "disable invalid state",
			err:    errors.New("invalid_ssh_peer_disable_state: removed"),
			status: http.StatusBadRequest,
			code:   "invalid_ssh_peer_disable",
		},
		{
			name:   "delete invalid state",
			err:    errors.New("invalid_ssh_peer_delete_state: removed"),
			status: http.StatusBadRequest,
			code:   "invalid_ssh_peer_delete",
		},
		{
			name:   "delete malformed remediation",
			err:    errors.New("invalid_ssh_peer_remediation: json: cannot unmarshal string into Go value"),
			status: http.StatusBadRequest,
			code:   "invalid_ssh_peer_config",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sshPeerConfigHandlerError(tc.err)
			if got == nil || got.Status != tc.status || got.Code != tc.code {
				t.Fatalf("handler error = %#v, want status=%d code=%s", got, tc.status, tc.code)
			}
		})
	}
}

func writeSSHPeerDaemonConfig(t *testing.T, sharedKey string) string {
	t.Helper()
	body := `{
  "config_version": 2,
  "config_revision": 7,
  "listen": "127.0.0.1:7853",
  "port": 7853,
  "shared_key": "` + sharedKey + `",
  "hostname": "m4",
  "transport": "ssh",
  "ssh": {
    "sync_key": "/Users/jesse/.config/clipfan/ssh/sync_ed25519",
    "known_hosts": "/Users/jesse/.config/clipfan/ssh/known_hosts",
    "peers": [{
      "id": "fsck",
      "enabled": true,
      "accept": true,
      "connect": false,
      "ssh_host": "fsck.com",
      "ssh_user": "jesse",
      "ssh_port": 22,
      "migration_state": "loopback_unprovisioned",
      "shared_key": "peer-secret",
      "private_key": "peer-private"
    }]
  }
}`
	return writeListenerRepairDaemonConfig(t, body)
}

func newSSHPeerConfigDaemon(t *testing.T, sharedKey string, configPath string) *Daemon {
	t.Helper()
	version := 2
	revision := uint64(7)
	d, err := NewWithOptions(&config.Config{
		ConfigVersion:  &version,
		ConfigRevision: &revision,
		Listen:         "127.0.0.1:7853",
		Port:           7853,
		SharedKey:      sharedKey,
		Hostname:       "m4",
		Discovery:      "static",
		Transport:      config.TransportSSH,
		SSH: &config.SSHConfig{
			SyncKey:    "/Users/jesse/.config/clipfan/ssh/sync_ed25519",
			KnownHosts: "/Users/jesse/.config/clipfan/ssh/known_hosts",
			Peers: []config.SSHPeer{{
				ID:             "fsck",
				Enabled:        true,
				Accept:         true,
				MigrationState: config.MigrationStateLoopbackUnprovisioned,
			}},
		},
	}, Options{ListenerBoundaryEnabled: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}
	d.configPath = configPath
	return d
}
