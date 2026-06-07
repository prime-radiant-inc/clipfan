package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

type fakeRosterRunner struct {
	stdout      []byte
	stderr      []byte
	truncated   bool
	lastCommand string
}

func (r *fakeRosterRunner) Run(_ context.Context, command sshprovision.SSHCommand) (sshprovision.CommandOutput, error) {
	r.lastCommand = strings.Join(command.Args, " ")
	return sshprovision.CommandOutput{Stdout: r.stdout, Stderr: r.stderr, StdoutTruncated: r.truncated}, nil
}

func countIDs(xs []string, want string) int {
	n := 0
	for _, x := range xs {
		if x == want {
			n++
		}
	}
	return n
}

func TestDiscoverRosterBFSClosesAndCapturesUnreachable(t *testing.T) {
	// A connects to B (locator) and lists C with NO locator (accept-only -> skip).
	// B lists A (already seen) and C with a locator. C is reachable via B but its
	// read fails, so it must land in Unreachable, not Reports.
	reports := map[string]RosterReadReport{
		"A": {Origin: "A", Peers: []RosterReadPeer{
			{ID: "B", SSHUser: "jesse", SSHHost: "hostB", SSHPort: 22, InstallPath: "/b/clipfan", Connect: true},
			{ID: "C", SSHHost: "", Accept: true},
		}},
		"B": {Origin: "B", Peers: []RosterReadPeer{
			{ID: "A", SSHUser: "jesse", SSHHost: "hostA", SSHPort: 22, InstallPath: "/a/clipfan"},
			{ID: "C", SSHUser: "jesse", SSHHost: "hostC", SSHPort: 22, InstallPath: "/c/clipfan"},
		}},
	}
	var trustCalls, readCalls []string
	trust := func(_ context.Context, ep rosterEndpoint) error {
		trustCalls = append(trustCalls, ep.ID)
		return nil
	}
	read := func(_ context.Context, ep rosterEndpoint) (RosterReadReport, error) {
		readCalls = append(readCalls, ep.ID)
		if ep.ID == "C" {
			return RosterReadReport{}, errors.New("connection refused")
		}
		return reports[ep.ID], nil
	}

	got := discoverRoster(context.Background(), []rosterEndpoint{
		{ID: "A", SSHUser: "jesse", SSHHost: "hostA", SSHPort: 22, InstallPath: "/a/clipfan"},
	}, "", trust, read)

	if len(got.Reports) != 2 || got.Reports["A"].Origin != "A" || got.Reports["B"].Origin != "B" {
		t.Fatalf("reports = %+v", got.Reports)
	}
	if _, ok := got.Reports["C"]; ok {
		t.Fatalf("C should have no report (read failed)")
	}
	if len(got.Unreachable) != 1 || got.Unreachable[0].ID != "C" || !strings.Contains(got.Unreachable[0].Reason, "read") {
		t.Fatalf("unreachable = %+v", got.Unreachable)
	}
	if countIDs(readCalls, "A") != 1 {
		t.Fatalf("A read %d times, want 1 (dedup): %v", countIDs(readCalls, "A"), readCalls)
	}
	if countIDs(readCalls, "C") != 1 {
		t.Fatalf("C read %d times, want 1: %v", countIDs(readCalls, "C"), readCalls)
	}
	if _, ok := got.Endpoints["C"]; ok {
		t.Fatalf("C should not be in Endpoints")
	}
	if got.Endpoints["B"].SSHHost != "hostB" {
		t.Fatalf("B endpoint = %+v", got.Endpoints["B"])
	}
	// trust must precede read for every host that was read.
	if len(trustCalls) < len(readCalls) {
		t.Fatalf("trust calls %v fewer than read calls %v", trustCalls, readCalls)
	}
}

func TestDiscoverRosterSkipsSelf(t *testing.T) {
	reports := map[string]RosterReadReport{
		"A": {Origin: "A", Peers: []RosterReadPeer{
			{ID: "SELF", SSHUser: "jesse", SSHHost: "selfhost", SSHPort: 22, InstallPath: "/clipfan", Connect: true},
		}},
	}
	var readCalls []string
	trust := func(_ context.Context, _ rosterEndpoint) error { return nil }
	read := func(_ context.Context, ep rosterEndpoint) (RosterReadReport, error) {
		readCalls = append(readCalls, ep.ID)
		return reports[ep.ID], nil
	}

	got := discoverRoster(context.Background(), []rosterEndpoint{
		{ID: "A", SSHUser: "jesse", SSHHost: "hostA", SSHPort: 22, InstallPath: "/a/clipfan"},
	}, "SELF", trust, read)

	if countIDs(readCalls, "SELF") != 0 {
		t.Fatalf("self was read over SSH: %v", readCalls)
	}
	if _, ok := got.Reports["SELF"]; ok {
		t.Fatalf("self should not be discovered via SSH")
	}
}

func TestDiscoverRosterTrustFailureIsUnreachable(t *testing.T) {
	trust := func(_ context.Context, ep rosterEndpoint) error {
		if ep.ID == "A" {
			return errors.New("host key conflict")
		}
		return nil
	}
	read := func(_ context.Context, _ rosterEndpoint) (RosterReadReport, error) {
		t.Fatalf("read must not run when trust fails")
		return RosterReadReport{}, nil
	}

	got := discoverRoster(context.Background(), []rosterEndpoint{
		{ID: "A", SSHUser: "jesse", SSHHost: "hostA", SSHPort: 22, InstallPath: "/a/clipfan"},
	}, "", trust, read)

	if len(got.Unreachable) != 1 || got.Unreachable[0].ID != "A" || !strings.Contains(got.Unreachable[0].Reason, "trust") {
		t.Fatalf("unreachable = %+v", got.Unreachable)
	}
	if len(got.Reports) != 0 {
		t.Fatalf("reports = %+v", got.Reports)
	}
}

func TestDiscoverRosterSkipsLocatorlessSeed(t *testing.T) {
	var readCalls []string
	trust := func(_ context.Context, _ rosterEndpoint) error { return nil }
	read := func(_ context.Context, ep rosterEndpoint) (RosterReadReport, error) {
		readCalls = append(readCalls, ep.ID)
		return RosterReadReport{Origin: ep.ID}, nil
	}

	got := discoverRoster(context.Background(), []rosterEndpoint{
		{ID: "NOLOC", SSHUser: "jesse", SSHHost: "", SSHPort: 22},
	}, "", trust, read)

	if len(readCalls) != 0 {
		t.Fatalf("a locator-less seed must not be contacted: %v", readCalls)
	}
	if len(got.Reports) != 0 || len(got.Unreachable) != 0 {
		t.Fatalf("got = %+v", got)
	}
}

func TestReadRosterEndpointDecodesReport(t *testing.T) {
	report := RosterReadReport{
		Origin: "A", Platform: "linux", UID: 1000,
		Peers: []RosterReadPeer{{ID: "B", SSHUser: "jesse", SSHHost: "hostB", SSHPort: 22, InstallPath: "/b/clipfan", Connect: true}},
	}
	blob, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRosterRunner{stdout: blob}

	got, err := readRosterEndpoint(context.Background(), runner, rosterEndpoint{
		ID: "A", SSHUser: "jesse", SSHHost: "hostA", SSHPort: 22, InstallPath: "/a/clipfan",
	}, "/Users/jesse/.ssh/known_hosts")
	if err != nil {
		t.Fatalf("readRosterEndpoint: %v", err)
	}
	if got.Origin != "A" || len(got.Peers) != 1 || got.Peers[0].ID != "B" {
		t.Fatalf("got = %+v", got)
	}
	if !strings.Contains(runner.lastCommand, "roster-read") || !strings.Contains(runner.lastCommand, "jesse@hosta") {
		t.Fatalf("command = %q", runner.lastCommand)
	}
}

func TestDecodeRosterReadReportRejections(t *testing.T) {
	valid, _ := json.Marshal(RosterReadReport{Origin: "A"})
	tests := []struct {
		name   string
		output sshprovision.CommandOutput
	}{
		{"truncated stdout", sshprovision.CommandOutput{Stdout: valid, StdoutTruncated: true}},
		{"stderr noise", sshprovision.CommandOutput{Stdout: valid, Stderr: []byte("warning: something")}},
		{"malformed", sshprovision.CommandOutput{Stdout: []byte("{not json")}},
		{"trailing json", sshprovision.CommandOutput{Stdout: append(append([]byte{}, valid...), []byte(`{"x":1}`)...)}},
		{"empty origin", sshprovision.CommandOutput{Stdout: []byte(`{}`)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := decodeRosterReadReport(tt.output); err == nil {
				t.Fatalf("expected error for %s", tt.name)
			}
		})
	}
}

func TestDecodeRosterReadReportAcceptsValid(t *testing.T) {
	blob, _ := json.Marshal(RosterReadReport{Origin: "A", Peers: []RosterReadPeer{{ID: "B"}}})
	got, err := decodeRosterReadReport(sshprovision.CommandOutput{Stdout: blob})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Origin != "A" || len(got.Peers) != 1 {
		t.Fatalf("got = %+v", got)
	}
}
