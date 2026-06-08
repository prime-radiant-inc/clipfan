package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/daemon"
	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
)

func TestRunSSHGatewayAllowsInjectedFleetSnapshotCommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	var got SSHGatewayIdentity
	err := runSSHGatewayWithHandlers(
		[]string{"--authorized-peer", "linux-a", "--authorized-key-id", "key-123456"},
		strings.NewReader(""),
		&stdout,
		&stderr,
		func(key string) string {
			if key == "SSH_ORIGINAL_COMMAND" {
				return sshprovision.SSHGatewayFleetSnapshotCommand
			}
			return ""
		},
		SSHGatewayHandlers{
			FleetSnapshot: func(identity SSHGatewayIdentity, stdout io.Writer) error {
				got = identity
				_, err := stdout.Write([]byte("snapshot-owned\n"))
				return err
			},
		},
	)
	if err != nil {
		t.Fatalf("runSSHGatewayWithHandlers() error = %v", err)
	}
	if got.PeerID != "linux-a" || got.KeyID != "key-123456" {
		t.Fatalf("identity = %#v", got)
	}
	if stdout.String() != "snapshot-owned\n" {
		t.Fatalf("stdout = %q, want handler-owned output", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunSSHGatewayRejectsFleetSnapshotWhenHandlerNil(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	err := runSSHGatewayWithHandlers(
		[]string{"--authorized-peer", "linux-a", "--authorized-key-id", "key-123456"},
		strings.NewReader(""),
		&stdout,
		&stderr,
		func(key string) string {
			if key == "SSH_ORIGINAL_COMMAND" {
				return sshprovision.SSHGatewayFleetSnapshotCommand
			}
			return ""
		},
		SSHGatewayHandlers{},
	)
	if !errors.Is(err, ErrSSHGatewayCommandRejected) {
		t.Fatalf("error = %v, want ErrSSHGatewayCommandRejected", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

// fleetSnapshotAuthConfig builds a config whose single peer satisfies the full
// sync predicate; mutate adjusts the peer to exercise rejection paths.
func fleetSnapshotAuthConfig(mutate func(*config.SSHPeer)) *config.Config {
	peer := config.SSHPeer{
		ID:             "m4",
		Enabled:        true,
		Accept:         true,
		Connect:        true,
		Persistent:     true,
		MigrationState: config.MigrationStateSSHKeysReady,
		Proof:          config.SSHProof{AcceptKeyID: "key-123456"},
	}
	if mutate != nil {
		mutate(&peer)
	}
	return &config.Config{
		Transport: config.TransportSSH,
		SSH: &config.SSHConfig{
			SyncKey:    "/tmp/clipfan-sync",
			KnownHosts: "/tmp/clipfan-known-hosts",
			Peers:      []config.SSHPeer{peer},
		},
	}
}

func TestValidateSSHGatewayFleetSnapshotPeerEnforcesFullSyncPredicate(t *testing.T) {
	t.Parallel()

	identity := SSHGatewayIdentity{PeerID: "m4", KeyID: "key-123456"}
	for _, tc := range []struct {
		name    string
		cfg     *config.Config
		wantErr bool
	}{
		{name: "full predicate accepted", cfg: fleetSnapshotAuthConfig(nil), wantErr: false},
		{name: "accept-only (no connect) rejected", cfg: fleetSnapshotAuthConfig(func(p *config.SSHPeer) { p.Connect = false }), wantErr: true},
		{name: "non-persistent rejected", cfg: fleetSnapshotAuthConfig(func(p *config.SSHPeer) { p.Persistent = false }), wantErr: true},
		{name: "disabled rejected", cfg: fleetSnapshotAuthConfig(func(p *config.SSHPeer) { p.Enabled = false }), wantErr: true},
		{name: "not ready rejected", cfg: fleetSnapshotAuthConfig(func(p *config.SSHPeer) { p.MigrationState = config.MigrationStateSSHMaterialStaged }), wantErr: true},
		{name: "wrong key id rejected", cfg: fleetSnapshotAuthConfig(func(p *config.SSHPeer) { p.Proof.AcceptKeyID = "key-999999" }), wantErr: true},
		{name: "missing peer rejected", cfg: fleetSnapshotAuthConfig(func(p *config.SSHPeer) { p.ID = "other" }), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateSSHGatewayFleetSnapshotPeer(tc.cfg, identity)
			if tc.wantErr {
				if !errors.Is(err, ErrSSHGatewayCommandRejected) {
					t.Fatalf("error = %v, want ErrSSHGatewayCommandRejected", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
		})
	}
}

func TestRunSSHGatewayDefaultFleetSnapshotEmitsRedactedSnapshot(t *testing.T) {
	configRoot := t.TempDir()
	stateRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_STATE_HOME", stateRoot)
	sharedKey := config.NewSharedKey()
	auth, err := transport.NewAuth(sharedKey)
	if err != nil {
		t.Fatal(err)
	}

	recvTS := time.Unix(1780000000, 0).UTC()
	peersPayload := map[string]any{
		"origin": "linux-b",
		"peers": []daemon.PeerState{{
			Hostname:   "m4",
			SSHStatus:  "connected",
			SSHActive:  true,
			LastRecvTS: recvTS,
		}},
	}
	srv := transport.NewServer("127.0.0.1:0", auth, func(clipboard.Content, string) {}, func() any { return peersPayload })
	srv.SetRecipientIdentity("linux-b")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ServeListener(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		<-serveErr
	})
	listenPort := ln.Addr().(*net.TCPAddr).Port
	writeGatewayConfig(t, sharedKey, listenPort)

	var stdout, stderr bytes.Buffer
	err = runSSHGateway(
		[]string{"--authorized-peer", "m4", "--authorized-key-id", "key-123456"},
		strings.NewReader(""),
		&stdout,
		&stderr,
		func(key string) string {
			if key == "SSH_ORIGINAL_COMMAND" {
				return sshprovision.SSHGatewayFleetSnapshotCommand
			}
			return ""
		},
	)
	if err != nil {
		t.Fatalf("runSSHGateway() error = %v; stderr=%q", err, stderr.String())
	}

	if strings.Contains(stdout.String(), sharedKey) || strings.Contains(stdout.String(), "shared_key") {
		t.Fatalf("snapshot leaked a secret: %s", stdout.String())
	}
	var snapshot daemon.FleetSnapshot
	if err := json.Unmarshal(stdout.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v; raw=%q", err, stdout.String())
	}
	if snapshot.Origin != "linux-b" {
		t.Fatalf("origin = %q, want linux-b", snapshot.Origin)
	}
	if len(snapshot.Peers) != 1 {
		t.Fatalf("want 1 peer, got %d: %#v", len(snapshot.Peers), snapshot.Peers)
	}
	p := snapshot.Peers[0]
	if p.ID != "m4" || p.SSHHost != "m4.example.com" || p.MigrationState != string(config.MigrationStateSSHKeysReady) {
		t.Fatalf("static fields wrong: %#v", p)
	}
	if p.SSHStatus != "connected" || !p.SSHActive || !p.LastRecvTS.Equal(recvTS) {
		t.Fatalf("live status not merged from /v1/peers: %#v", p)
	}
}

func TestRunSSHGatewayDefaultFleetSnapshotRejectsAcceptOnlyPeer(t *testing.T) {
	configRoot := t.TempDir()
	stateRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_STATE_HOME", stateRoot)
	writeAcceptOnlyGatewayConfig(t, config.NewSharedKey(), 7853)

	var stdout, stderr bytes.Buffer
	err := runSSHGateway(
		[]string{"--authorized-peer", "m4", "--authorized-key-id", "key-123456"},
		strings.NewReader(""),
		&stdout,
		&stderr,
		func(key string) string {
			if key == "SSH_ORIGINAL_COMMAND" {
				return sshprovision.SSHGatewayFleetSnapshotCommand
			}
			return ""
		},
	)
	if !errors.Is(err, ErrSSHGatewayCommandRejected) {
		t.Fatalf("error = %v, want ErrSSHGatewayCommandRejected", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}
