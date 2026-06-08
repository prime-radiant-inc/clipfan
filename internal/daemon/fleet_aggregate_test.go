package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
	"github.com/prime-radiant-inc/clipfan/internal/version"
)

// fakeFleetRunner dispatches on the last command arg (the test sets it to the host
// id) so gatherFleetView can be exercised without real SSH.
type fakeFleetRunner struct {
	outputs map[string]sshprovision.CommandOutput
	errs    map[string]error
}

func (f fakeFleetRunner) Run(_ context.Context, cmd sshprovision.SSHCommand) (sshprovision.CommandOutput, error) {
	key := cmd.Args[len(cmd.Args)-1]
	if err, ok := f.errs[key]; ok {
		return sshprovision.CommandOutput{}, err
	}
	return f.outputs[key], nil
}

func target(id string) fleetPeerTarget {
	return fleetPeerTarget{ID: id, Command: sshprovision.SSHCommand{Args: []string{"ssh", id}}}
}

func snapshotJSON(t *testing.T, origin string) []byte {
	t.Helper()
	data, err := json.Marshal(FleetSnapshot{Origin: origin, Version: "test", Peers: []FleetSnapshotPeer{}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestGatherFleetViewMergesLocalAndPeers(t *testing.T) {
	local := FleetSnapshot{Origin: "alpha", Version: "test", Peers: []FleetSnapshotPeer{}}
	runner := fakeFleetRunner{
		outputs: map[string]sshprovision.CommandOutput{
			"beta":  {Stdout: snapshotJSON(t, "beta")},
			"delta": {Stdout: []byte("not json")},
		},
		errs: map[string]error{
			"gamma": fmt.Errorf("dial timeout"),
		},
	}
	targets := []fleetPeerTarget{
		target("beta"),  // answers
		target("gamma"), // runner error
		target("delta"), // undecodable output
		{ID: "epsilon", BuildErr: fmt.Errorf("bad gateway path")}, // command never built
	}

	view := gatherFleetView(context.Background(), runner, local, targets, time.Second, 4)

	if view.Origin != "alpha" || view.Version != "test" {
		t.Fatalf("envelope = %#v", view)
	}
	byID := map[string]FleetViewHost{}
	for _, h := range view.Hosts {
		byID[h.ID] = h
	}
	if len(view.Hosts) != 5 {
		t.Fatalf("want 5 hosts, got %d: %#v", len(view.Hosts), view.Hosts)
	}
	// hosts must be sorted by id for a stable payload
	for i := 1; i < len(view.Hosts); i++ {
		if view.Hosts[i-1].ID > view.Hosts[i].ID {
			t.Fatalf("hosts not sorted: %#v", view.Hosts)
		}
	}
	if h := byID["alpha"]; !h.Reachable || h.Snapshot == nil || h.Snapshot.Origin != "alpha" {
		t.Errorf("local host wrong: %#v", h)
	}
	if h := byID["beta"]; !h.Reachable || h.Snapshot == nil || h.Snapshot.Origin != "beta" {
		t.Errorf("reachable peer wrong: %#v", h)
	}
	for _, id := range []string{"gamma", "delta", "epsilon"} {
		h := byID[id]
		if h.Reachable || h.Snapshot != nil || h.Error == "" {
			t.Errorf("host %q should be unreachable with an error: %#v", id, h)
		}
	}
}

func TestGatherFleetViewLocalOnly(t *testing.T) {
	local := FleetSnapshot{Origin: "solo", Version: "test", Peers: []FleetSnapshotPeer{}}
	view := gatherFleetView(context.Background(), fakeFleetRunner{}, local, nil, time.Second, 4)
	if len(view.Hosts) != 1 || view.Hosts[0].ID != "solo" || !view.Hosts[0].Reachable {
		t.Fatalf("local-only view wrong: %#v", view.Hosts)
	}
}

func TestDecodeFleetSnapshotStrict(t *testing.T) {
	good, err := json.Marshal(FleetSnapshot{Origin: "beta", Version: "v", Peers: []FleetSnapshotPeer{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		output  sshprovision.CommandOutput
		wantErr bool
	}{
		{name: "happy", output: sshprovision.CommandOutput{Stdout: good}, wantErr: false},
		{name: "stdout truncated", output: sshprovision.CommandOutput{Stdout: good, StdoutTruncated: true}, wantErr: true},
		{name: "stderr truncated", output: sshprovision.CommandOutput{Stdout: good, StderrTruncated: true}, wantErr: true},
		{name: "stderr noise", output: sshprovision.CommandOutput{Stdout: good, Stderr: []byte("warning: x")}, wantErr: true},
		{name: "malformed json", output: sshprovision.CommandOutput{Stdout: []byte("{")}, wantErr: true},
		{name: "trailing json", output: sshprovision.CommandOutput{Stdout: append(append([]byte{}, good...), []byte("\n{}")...)}, wantErr: true},
		{name: "missing origin", output: sshprovision.CommandOutput{Stdout: []byte(`{"version":"v","peers":[]}`)}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, err := decodeFleetSnapshot(tc.output)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got snapshot %#v", snapshot)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if snapshot.Origin != "beta" {
				t.Fatalf("origin = %q", snapshot.Origin)
			}
		})
	}
}

func TestBuildFleetPeerTargetsMapsPinnedCommand(t *testing.T) {
	peers := []sshSyncPeer{{
		id:             "beta",
		user:           "jesse",
		host:           "magic-kingdom",
		port:           22,
		privateKeyPath: "/home/jesse/.config/clipfan/ssh/sync_ed25519",
		knownHostsPath: "/home/jesse/.config/clipfan/ssh/known_hosts",
		gatewayPath:    "/home/jesse/.local/bin/clipfan",
		connectKeyID:   "key-abc12345",
		directGateway:  true,
	}}

	targets := buildFleetPeerTargets(peers, "alpha")
	if len(targets) != 1 {
		t.Fatalf("want 1 target, got %d", len(targets))
	}
	tg := targets[0]
	if tg.ID != "beta" || tg.BuildErr != nil {
		t.Fatalf("target = %#v", tg)
	}
	// Direct-gateway commands carry the identity inline; verify it is the LOCAL id
	// as authorized-peer and the peer's connect key id as authorized-key-id.
	last := tg.Command.Args[len(tg.Command.Args)-1]
	if !strings.Contains(last, "'--authorized-peer' 'alpha'") ||
		!strings.Contains(last, "'--authorized-key-id' 'key-abc12345'") ||
		!strings.Contains(last, "'--direct-command' 'fleet-snapshot'") {
		t.Fatalf("direct command = %q", last)
	}
}

func TestBuildFleetPeerTargetsCapturesBuildError(t *testing.T) {
	peers := []sshSyncPeer{{id: "bad", user: "", host: "", port: 0}}
	targets := buildFleetPeerTargets(peers, "alpha")
	if len(targets) != 1 || targets[0].ID != "bad" || targets[0].BuildErr == nil {
		t.Fatalf("expected a single target carrying a build error, got %#v", targets)
	}
}

// TestDaemonFleetHandlerReturnsLocalOnlyViewWithoutPeers exercises the daemon glue
// (origin, peer derivation, local snapshot, gather) end-to-end without SSH: a daemon
// with no connect-able sync peers serves a fleet view containing just itself.
func TestDaemonFleetHandlerReturnsLocalOnlyViewWithoutPeers(t *testing.T) {
	d, _, _ := newTestDaemon(t)
	got := d.fleetHandler()
	view, ok := got.(FleetView)
	if !ok {
		t.Fatalf("fleetHandler returned %T, want FleetView", got)
	}
	if view.Origin != "self" || view.Version != version.Version {
		t.Fatalf("view envelope = %#v", view)
	}
	if len(view.Hosts) != 1 || view.Hosts[0].ID != "self" || !view.Hosts[0].Reachable {
		t.Fatalf("expected local-only reachable host, got %#v", view.Hosts)
	}
	if view.Hosts[0].Snapshot == nil || view.Hosts[0].Snapshot.Origin != "self" {
		t.Fatalf("local snapshot wrong: %#v", view.Hosts[0])
	}
}
