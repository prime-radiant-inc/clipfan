package daemon

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/config"
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
			name:   "malformed stored proof",
			err:    errors.New("invalid_ssh_peer_proof: json: cannot unmarshal string into Go value"),
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
