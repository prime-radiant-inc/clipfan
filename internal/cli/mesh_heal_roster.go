package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

// trustFn trusts a host's key into the orchestrator's regular known_hosts so the
// subsequent admin SSH to it succeeds. rosterReader runs roster-read on a host
// over that admin SSH and decodes its self-report. Both are injected so
// discoverRoster is testable without SSH.
type trustFn func(context.Context, rosterEndpoint) error
type rosterReader func(context.Context, rosterEndpoint) (RosterReadReport, error)

// unreachableHost records a host mesh-heal could not trust or read, so the run
// can report it rather than aborting the whole heal.
type unreachableHost struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// rosterDiscovery is the closed roster: every host mesh-heal could reach, its
// self-report and the endpoint used to reach it, plus the hosts it could not.
type rosterDiscovery struct {
	Reports     map[string]RosterReadReport
	Endpoints   map[string]rosterEndpoint
	Unreachable []unreachableHost
}

// discoverRoster walks the mesh breadth-first starting from seed. For each
// unseen, locator-bearing endpoint it trusts the host key, reads the host's
// roster-read self-report, and enqueues that host's peers that carry an SSH
// locator (peers without one — e.g. accept-only edges — are skipped, since they
// cannot be reached). selfID is pre-marked seen so the local host is never
// contacted over SSH (the orchestrator seeds the local report from config). Each
// host id is attempted once; trust/read failures are captured in Unreachable
// rather than aborting the walk.
func discoverRoster(ctx context.Context, seed []rosterEndpoint, selfID string, trust trustFn, read rosterReader) rosterDiscovery {
	result := rosterDiscovery{
		Reports:   map[string]RosterReadReport{},
		Endpoints: map[string]rosterEndpoint{},
	}
	seen := map[string]bool{}
	if selfID != "" {
		seen[selfID] = true
	}

	var queue []rosterEndpoint
	enqueue := func(ep rosterEndpoint) {
		if ep.ID == "" || ep.SSHHost == "" || seen[ep.ID] {
			return
		}
		seen[ep.ID] = true
		queue = append(queue, ep)
	}
	for _, ep := range seed {
		enqueue(ep)
	}

	for len(queue) > 0 {
		ep := queue[0]
		queue = queue[1:]

		if err := trust(ctx, ep); err != nil {
			result.Unreachable = append(result.Unreachable, unreachableHost{ID: ep.ID, Reason: "trust: " + err.Error()})
			continue
		}
		report, err := read(ctx, ep)
		if err != nil {
			result.Unreachable = append(result.Unreachable, unreachableHost{ID: ep.ID, Reason: "read: " + err.Error()})
			continue
		}
		result.Reports[ep.ID] = report
		result.Endpoints[ep.ID] = ep

		for _, peer := range report.Peers {
			enqueue(rosterEndpoint{
				ID:          peer.ID,
				SSHUser:     peer.SSHUser,
				SSHHost:     peer.SSHHost,
				SSHPort:     peer.SSHPort,
				InstallPath: peer.InstallPath,
			})
		}
	}
	return result
}

// readRosterEndpoint runs roster-read on a host over the user's regular,
// strict-host-key-checked SSH and strict-decodes its self-report. It is the
// production rosterReader; the orchestrator binds runner and regularKnownHostsPath.
func readRosterEndpoint(ctx context.Context, runner sshprovision.CommandRunner, ep rosterEndpoint, regularKnownHostsPath string) (RosterReadReport, error) {
	command, err := sshprovision.RegularSSHRosterReadCommand(sshprovision.RegularSSHRosterReadSpec{
		User:           ep.SSHUser,
		Host:           ep.SSHHost,
		Port:           ep.SSHPort,
		KnownHostsPath: regularKnownHostsPath,
		InstallPath:    ep.InstallPath,
	})
	if err != nil {
		return RosterReadReport{}, err
	}
	output, err := runner.Run(ctx, command)
	if err != nil {
		return RosterReadReport{}, err
	}
	return decodeRosterReadReport(output)
}

// decodeRosterReadReport strict-decodes a roster-read JSON report, rejecting
// truncation, stderr noise, malformed or trailing JSON, and an empty origin
// (mirrors regular_ssh_driver.runJSON). Strictness matters: a half-read or
// noise-contaminated report must fail loudly rather than silently mis-describe a
// host's edges to change-detection.
func decodeRosterReadReport(output sshprovision.CommandOutput) (RosterReadReport, error) {
	if output.StdoutTruncated || output.StderrTruncated {
		return RosterReadReport{}, fmt.Errorf("roster_read_output_truncated")
	}
	if len(bytes.TrimSpace(output.Stderr)) != 0 {
		return RosterReadReport{}, fmt.Errorf("roster_read_stderr_not_empty")
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Stdout))
	var report RosterReadReport
	if err := decoder.Decode(&report); err != nil {
		return RosterReadReport{}, fmt.Errorf("roster_read_malformed_json: %v", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return RosterReadReport{}, fmt.Errorf("roster_read_trailing_json")
		}
		return RosterReadReport{}, fmt.Errorf("roster_read_malformed_json: %v", err)
	}
	if report.Origin == "" {
		return RosterReadReport{}, fmt.Errorf("roster_read_missing_origin")
	}
	return report, nil
}
