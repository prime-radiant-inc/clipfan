package daemon

import (
	"context"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/discovery"
)

func TestSnapshotIncludesConfiguredReadySSHPeersBeforeActivity(t *testing.T) {
	cfg := sshSyncManagerTestConfig()
	cfg.Port = 7853
	cfg.SSH.Peers = append(cfg.SSH.Peers,
		readySSHPeerForTest("magic-kingdom"),
		config.SSHPeer{ID: "disabled", Enabled: false, Connect: true, Persistent: true, MigrationState: config.MigrationStateSSHKeysReady},
		config.SSHPeer{ID: "staged", Enabled: true, Connect: true, Persistent: true, MigrationState: config.MigrationStateSSHMaterialStaged},
	)
	d := &Daemon{
		cfg:        cfg,
		disc:       discovery.NewStatic(nil, cfg.Port),
		origin:     "m4",
		peerStatus: map[string]*PeerState{},
	}

	got := d.Snapshot(context.Background())
	if len(got) != 2 {
		t.Fatalf("snapshot len = %d, want two ready SSH peers: %#v", len(got), got)
	}
	if got[0].Hostname != "linux-b" || got[0].Port != 7853 {
		t.Fatalf("first peer = %#v, want linux-b on fleet port", got[0])
	}
	if got[1].Hostname != "magic-kingdom" || got[1].Port != 7853 {
		t.Fatalf("second peer = %#v, want magic-kingdom on fleet port", got[1])
	}
}

func TestSnapshotIncludesConfiguredSSHPeerRuntimeState(t *testing.T) {
	cfg := sshSyncManagerTestConfig()
	cfg.Port = 7853
	d := &Daemon{
		cfg:        cfg,
		disc:       discovery.NewStatic(nil, cfg.Port),
		origin:     "m4",
		peerStatus: map[string]*PeerState{},
		sshSync: &fakeSSHSyncRuntime{runtime: map[string]SSHPeerRuntimeState{
			"linux-b": {
				PeerID:        "linux-b",
				Active:        true,
				Status:        "live",
				LastConnectTS: fixedTime,
				LastAckTS:     fixedTime,
			},
		}},
	}

	got := d.Snapshot(context.Background())
	if len(got) != 1 {
		t.Fatalf("snapshot len = %d, want configured peer: %#v", len(got), got)
	}
	peer := got[0]
	if peer.Transport != config.TransportSSH || peer.SSHHost != "linux-b.example.com" || peer.SSHUser != "jesse" || peer.SSHPort != 22 {
		t.Fatalf("ssh identity fields = %#v", peer)
	}
	if !peer.SSHActive || peer.SSHStatus != "live" || peer.SSHLastConnectTS != fixedTime || peer.SSHLastAckTS != fixedTime {
		t.Fatalf("ssh runtime fields = %#v", peer)
	}
}

func TestSnapshotMergesConfiguredSSHPeerWithActivity(t *testing.T) {
	cfg := sshSyncManagerTestConfig()
	cfg.Port = 7853
	d := &Daemon{
		cfg:    cfg,
		disc:   discovery.NewStatic(nil, cfg.Port),
		origin: "m4",
		peerStatus: map[string]*PeerState{
			"linux-b": {
				Hostname:    "linux-b",
				LastPushTS:  fixedTime,
				LastPushOK:  true,
				LastRecvTS:  fixedTime,
				LastPushErr: "old error",
			},
		},
	}

	got := d.Snapshot(context.Background())
	if len(got) != 1 {
		t.Fatalf("snapshot len = %d, want merged configured peer: %#v", len(got), got)
	}
	if got[0].Hostname != "linux-b" || got[0].Port != 7853 {
		t.Fatalf("peer identity = %#v, want configured linux-b", got[0])
	}
	if !got[0].LastPushOK || got[0].LastPushTS != fixedTime || got[0].LastRecvTS != fixedTime {
		t.Fatalf("peer activity was not merged into configured row: %#v", got[0])
	}
	if got[0].LastPushErr != "old error" {
		t.Fatalf("last push err = %q, want activity error preserved", got[0].LastPushErr)
	}
}
