package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

const (
	fleetPeerGatherTimeout = 10 * time.Second
	fleetGatherConcurrency = 6
	fleetViewCacheTTL      = 8 * time.Second
)

// FleetView is the daemon's aggregated, redacted view of the whole mesh, served at
// GET /v1/fleet for the Mac app. It carries the local host's own snapshot plus each
// directly-connected peer's snapshot (gathered over that peer's pinned sync key), or
// an unreachable marker for any peer that did not answer. The Mac reconstructs the
// undirected edge graph from the per-host snapshots, so it never needs an SSH client.
type FleetView struct {
	Origin  string          `json:"origin"`
	Version string          `json:"version"`
	Hosts   []FleetViewHost `json:"hosts"`
}

// FleetViewHost is one host's contribution: its self-reported snapshot when reached,
// or Reachable:false plus a short reason when the gather failed. The Mac renders an
// edge only one endpoint observed as "unknown" rather than down.
type FleetViewHost struct {
	ID        string         `json:"id"`
	Reachable bool           `json:"reachable"`
	Snapshot  *FleetSnapshot `json:"snapshot,omitempty"`
	Error     string         `json:"error,omitempty"`
}

// fleetPeerTarget is a built fleet-snapshot command for one peer, or the error from
// trying to build it — so an unbuildable peer still surfaces as unreachable rather
// than vanishing from the fleet view.
type fleetPeerTarget struct {
	ID       string
	Command  sshprovision.SSHCommand
	BuildErr error
}

// gatherFleetView runs each peer's fleet-snapshot command concurrently (bounded by
// concurrency), strict-decodes the result, and assembles the fleet view. The local
// host is always present with its own snapshot; a peer that errors, times out, or
// returns undecodable output is marked unreachable rather than dropped. Hosts are
// sorted by id for a stable payload.
func gatherFleetView(ctx context.Context, runner sshprovision.CommandRunner, local FleetSnapshot, targets []fleetPeerTarget, perPeerTimeout time.Duration, concurrency int) FleetView {
	localSnapshot := local
	hosts := make([]FleetViewHost, 0, len(targets)+1)
	hosts = append(hosts, FleetViewHost{ID: local.Origin, Reachable: true, Snapshot: &localSnapshot})

	if concurrency < 1 {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)
	results := make([]FleetViewHost, len(targets))
	var wg sync.WaitGroup
	for i := range targets {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = gatherPeerSnapshot(ctx, runner, targets[i], perPeerTimeout)
		}(i)
	}
	wg.Wait()
	hosts = append(hosts, results...)
	sort.Slice(hosts, func(i, j int) bool { return hosts[i].ID < hosts[j].ID })

	return FleetView{Origin: local.Origin, Version: local.Version, Hosts: hosts}
}

// gatherPeerSnapshot fetches and decodes one peer's snapshot, translating any
// failure (unbuildable command, run error, undecodable output) into an unreachable
// host entry carrying the reason.
func gatherPeerSnapshot(ctx context.Context, runner sshprovision.CommandRunner, target fleetPeerTarget, perPeerTimeout time.Duration) FleetViewHost {
	if target.BuildErr != nil {
		return FleetViewHost{ID: target.ID, Reachable: false, Error: target.BuildErr.Error()}
	}
	peerCtx, cancel := context.WithTimeout(ctx, perPeerTimeout)
	defer cancel()
	output, err := runner.Run(peerCtx, target.Command)
	if err != nil {
		return FleetViewHost{ID: target.ID, Reachable: false, Error: err.Error()}
	}
	snapshot, err := decodeFleetSnapshot(output)
	if err != nil {
		return FleetViewHost{ID: target.ID, Reachable: false, Error: err.Error()}
	}
	return FleetViewHost{ID: target.ID, Reachable: true, Snapshot: &snapshot}
}

// decodeFleetSnapshot strict-decodes a peer's fleet-snapshot JSON, rejecting
// truncation, stderr noise, malformed or trailing JSON, and an empty origin (mirrors
// regular_ssh_driver.runJSON). A half-read or noise-contaminated snapshot must fail
// loudly — surfacing the host as unreachable — rather than silently mis-describe its
// edges in the aggregated view.
func decodeFleetSnapshot(output sshprovision.CommandOutput) (FleetSnapshot, error) {
	if output.StdoutTruncated || output.StderrTruncated {
		return FleetSnapshot{}, fmt.Errorf("fleet_snapshot_output_truncated")
	}
	if len(bytes.TrimSpace(output.Stderr)) != 0 {
		return FleetSnapshot{}, fmt.Errorf("fleet_snapshot_stderr_not_empty")
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Stdout))
	var snapshot FleetSnapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return FleetSnapshot{}, fmt.Errorf("fleet_snapshot_malformed_json: %v", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return FleetSnapshot{}, fmt.Errorf("fleet_snapshot_trailing_json")
		}
		return FleetSnapshot{}, fmt.Errorf("fleet_snapshot_malformed_json: %v", err)
	}
	if snapshot.Origin == "" {
		return FleetSnapshot{}, fmt.Errorf("fleet_snapshot_missing_origin")
	}
	return snapshot, nil
}

// buildFleetPeerTargets maps each connect-able sync peer to its pinned fleet-snapshot
// command. The identity mirrors the sync stream: this host is the authorized-peer and
// the peer's connect key id is the authorized-key-id (the credential the peer issued
// us). A peer whose command cannot be built keeps its id with the build error.
func buildFleetPeerTargets(peers []sshSyncPeer, localID string) []fleetPeerTarget {
	targets := make([]fleetPeerTarget, 0, len(peers))
	for _, p := range peers {
		cmd, err := sshprovision.PinnedSSHFleetSnapshotCommand(sshprovision.PinnedSSHCommand{
			User:            p.user,
			Host:            p.host,
			Port:            p.port,
			PrivateKeyPath:  p.privateKeyPath,
			KnownHostsPath:  p.knownHostsPath,
			GatewayPath:     p.gatewayPath,
			AuthorizedPeer:  localID,
			AuthorizedKeyID: p.connectKeyID,
			DirectGateway:   p.directGateway,
		})
		targets = append(targets, fleetPeerTarget{ID: p.id, Command: cmd, BuildErr: err})
	}
	return targets
}

// fleetHandler serves GET /v1/fleet with the daemon's aggregated mesh view, behind a
// short TTL cache so a Mac refresh does not re-SSH the whole fleet on every poll.
func (d *Daemon) fleetHandler() any {
	return d.fleetView()
}

func (d *Daemon) fleetView() FleetView {
	d.fleetMu.Lock()
	defer d.fleetMu.Unlock()
	if !d.fleetFetchedAt.IsZero() && time.Since(d.fleetFetchedAt) < fleetViewCacheTTL {
		return d.fleetCached
	}
	ctx, cancel := context.WithTimeout(context.Background(), fleetPeerGatherTimeout+5*time.Second)
	defer cancel()
	local := BuildFleetSnapshot(d.cfg, d.origin, d.Snapshot(ctx))
	targets := buildFleetPeerTargets(sshSyncPeersFromConfig(d.cfg), d.origin)
	runner := sshprovision.ExecCommandRunner{MaxOutputBytes: 256 * 1024}
	view := gatherFleetView(ctx, runner, local, targets, fleetPeerGatherTimeout, fleetGatherConcurrency)
	d.fleetCached = view
	d.fleetFetchedAt = time.Now()
	return view
}
