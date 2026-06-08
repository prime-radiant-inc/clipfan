package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/version"
)

// TestBuildFleetSnapshotRedactsSecrets is the load-bearing test: the snapshot is
// shipped off-box to peers, so it must be an explicit allowlist carrying no
// secrets — not the shared key, not key material, not the proof key ids, not the
// sync-key path — even when the source config holds all of them.
func TestBuildFleetSnapshotRedactsSecrets(t *testing.T) {
	const (
		sharedKeyMarker  = "SUPER-SECRET-SHARED-KEY"
		acceptKeyMarker  = "ACCEPT-KEY-ID-MARKER"
		connectKeyMarker = "CONNECT-KEY-ID-MARKER"
		syncKeyMarker    = "/secret/path/sync-key-MARKER"
	)
	cfg := &config.Config{
		Hostname:  "alpha",
		Transport: config.TransportSSH,
		SharedKey: sharedKeyMarker,
		SSH: &config.SSHConfig{
			SyncKey:    syncKeyMarker,
			KnownHosts: "/secret/path/known_hosts-MARKER",
			Peers: []config.SSHPeer{{
				ID:             "beta",
				SSHHost:        "100.64.0.2",
				SSHUser:        "user",
				SSHPort:        22,
				Enabled:        true,
				Accept:         true,
				Connect:        true,
				Persistent:     true,
				MigrationState: config.MigrationStateSSHKeysReady,
				Proof: config.SSHProof{
					AcceptKeyID:  acceptKeyMarker,
					ConnectKeyID: connectKeyMarker,
				},
			}},
		},
	}

	encoded, err := json.Marshal(BuildFleetSnapshot(cfg, nil))
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	payload := string(encoded)
	for _, forbidden := range []string{
		sharedKeyMarker, acceptKeyMarker, connectKeyMarker, syncKeyMarker,
		"shared_key", "proof", "sync_key", "accept_key_id", "connect_key_id",
	} {
		if strings.Contains(payload, forbidden) {
			t.Errorf("snapshot leaked %q: %s", forbidden, payload)
		}
	}
	// Guard against the trivial pass where redaction emptied the payload.
	if !strings.Contains(payload, "beta") || !strings.Contains(payload, string(config.MigrationStateSSHKeysReady)) {
		t.Errorf("snapshot missing expected non-secret fields: %s", payload)
	}
}

// TestBuildFleetSnapshotMergesConfigAndLiveStatus verifies the static edge
// description comes from config while the live status (set by the running
// daemon's peer tracking) overlays from the matching PeerState row.
func TestBuildFleetSnapshotMergesConfigAndLiveStatus(t *testing.T) {
	recvTS := time.Unix(1780000000, 0).UTC()
	ackTS := time.Unix(1780000100, 0).UTC()
	connectTS := time.Unix(1780000200, 0).UTC()
	cfg := &config.Config{
		Hostname:  "alpha",
		Transport: config.TransportSSH,
		SSH: &config.SSHConfig{
			Peers: []config.SSHPeer{{
				ID:             "beta",
				SSHHost:        "100.64.0.2",
				SSHUser:        "user",
				SSHPort:        22,
				MigrationState: config.MigrationStateSSHKeysReady,
			}},
		},
	}
	peers := []PeerState{{
		Hostname:         "beta",
		SSHStatus:        "connected",
		SSHActive:        true,
		LastRecvTS:       recvTS,
		SSHLastAckTS:     ackTS,
		SSHLastConnectTS: connectTS,
	}}

	snapshot := BuildFleetSnapshot(cfg, peers)
	if len(snapshot.Peers) != 1 {
		t.Fatalf("want 1 peer, got %d", len(snapshot.Peers))
	}
	p := snapshot.Peers[0]
	if p.ID != "beta" || p.SSHHost != "100.64.0.2" || p.SSHUser != "user" || p.SSHPort != 22 {
		t.Errorf("static fields not taken from config: %+v", p)
	}
	if p.MigrationState != string(config.MigrationStateSSHKeysReady) {
		t.Errorf("migration_state = %q, want ssh_keys_ready", p.MigrationState)
	}
	if p.SSHStatus != "connected" || !p.SSHActive {
		t.Errorf("live status not overlaid: %+v", p)
	}
	if !p.LastRecvTS.Equal(recvTS) || !p.SSHLastAckTS.Equal(ackTS) || !p.SSHLastConnectTS.Equal(connectTS) {
		t.Errorf("live timestamps not overlaid: %+v", p)
	}
}

// TestBuildFleetSnapshotIncludesConfiguredPeerWithoutLiveRow makes sure a peer
// that has no live session yet still appears (with its config-derived edge
// state), rather than being dropped because the daemon has no PeerState for it.
func TestBuildFleetSnapshotIncludesConfiguredPeerWithoutLiveRow(t *testing.T) {
	cfg := &config.Config{
		Hostname:  "alpha",
		Transport: config.TransportSSH,
		SSH: &config.SSHConfig{
			Peers: []config.SSHPeer{{
				ID:             "gamma",
				SSHHost:        "100.64.0.3",
				SSHUser:        "user",
				SSHPort:        22,
				MigrationState: config.MigrationStateSSHMaterialStaged,
			}},
		},
	}

	snapshot := BuildFleetSnapshot(cfg, nil)
	if len(snapshot.Peers) != 1 {
		t.Fatalf("want 1 peer, got %d", len(snapshot.Peers))
	}
	p := snapshot.Peers[0]
	if p.ID != "gamma" || p.MigrationState != string(config.MigrationStateSSHMaterialStaged) {
		t.Errorf("configured peer fields wrong: %+v", p)
	}
	if p.SSHActive || p.SSHStatus != "" {
		t.Errorf("expected empty live status for peer without a session: %+v", p)
	}
}

// TestBuildFleetSnapshotOriginVersionAndNilSSH covers the envelope fields and the
// nil-SSH guard: peers is a non-nil empty slice so the JSON renders "peers": [].
func TestBuildFleetSnapshotOriginVersionAndNilSSH(t *testing.T) {
	cfg := &config.Config{Hostname: "alpha"}
	snapshot := BuildFleetSnapshot(cfg, nil)
	if snapshot.Origin != "alpha" {
		t.Errorf("origin = %q, want alpha", snapshot.Origin)
	}
	if snapshot.Version != version.Version {
		t.Errorf("version = %q, want %q", snapshot.Version, version.Version)
	}
	if snapshot.Peers == nil {
		t.Errorf("peers should be a non-nil empty slice for clean JSON")
	}
	if len(snapshot.Peers) != 0 {
		t.Errorf("want 0 peers for nil SSH, got %d", len(snapshot.Peers))
	}
}
