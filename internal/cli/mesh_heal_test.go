package cli

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

func TestReportPeerView(t *testing.T) {
	report := RosterReadReport{Origin: "A", Peers: []RosterReadPeer{
		{ID: "B", Enabled: true, Accept: true, Connect: true, MigrationState: "ssh_keys_ready", AcceptKeyID: "ak", ConnectKeyID: "ck"},
	}}
	v := reportPeerView(report, "B")
	if !v.Found || !v.Enabled || !v.Accept || !v.Connect || v.MigrationState != "ssh_keys_ready" || v.AcceptKeyID != "ak" || v.ConnectKeyID != "ck" {
		t.Fatalf("view = %+v", v)
	}
	if missing := reportPeerView(report, "Z"); missing.Found {
		t.Fatalf("absent peer should be not-found: %+v", missing)
	}
}

func TestEdgeHealthyFromReports(t *testing.T) {
	healthyPeer := func(id string) RosterReadPeer {
		return RosterReadPeer{ID: id, Enabled: true, Accept: true, Connect: true, MigrationState: "ssh_keys_ready", AcceptKeyID: "a", ConnectKeyID: "c"}
	}
	reports := map[string]RosterReadReport{
		"A": {Origin: "A", Peers: []RosterReadPeer{healthyPeer("B")}},
		"B": {Origin: "B", Peers: []RosterReadPeer{healthyPeer("A")}},
		"C": {Origin: "C", Peers: []RosterReadPeer{healthyPeer("A")}}, // C sees A, but A doesn't see C
	}
	if !edgeHealthyFromReports(reports, "A", "B") {
		t.Fatalf("A<->B should be healthy")
	}
	if edgeHealthyFromReports(reports, "A", "C") {
		t.Fatalf("A<->C should be unhealthy (A has no view of C)")
	}
}

func TestEnumerateMeshEdgesDeterministic(t *testing.T) {
	reports := map[string]RosterReadReport{"C": {}, "A": {}, "B": {}}
	edges := enumerateMeshEdges(reports)
	want := []meshEdge{{"A", "B"}, {"A", "C"}, {"B", "C"}}
	if len(edges) != len(want) {
		t.Fatalf("edges = %+v", edges)
	}
	for i := range want {
		if edges[i] != want[i] {
			t.Fatalf("edges[%d] = %+v, want %+v", i, edges[i], want[i])
		}
	}
}

func TestSeedEndpointsFromConfig(t *testing.T) {
	if eps := seedEndpointsFromConfig(&config.Config{Hostname: "solo"}); eps != nil {
		t.Fatalf("no SSH config should seed nothing, got %+v", eps)
	}
	cfg := &config.Config{SSH: &config.SSHConfig{Peers: []config.SSHPeer{
		{ID: "m4", SSHUser: "jesse", SSHHost: "100.114.54.38", SSHPort: 22, InstallPath: "/m4/clipfan"},
	}}}
	eps := seedEndpointsFromConfig(cfg)
	if len(eps) != 1 || eps[0].ID != "m4" || eps[0].SSHHost != "100.114.54.38" || eps[0].SSHUser != "jesse" || eps[0].InstallPath != "/m4/clipfan" {
		t.Fatalf("eps = %+v", eps)
	}
}

func TestBuildProvisionHostsSkipsHostsWithoutEndpoint(t *testing.T) {
	reports := map[string]RosterReadReport{
		"A": {Origin: "A", InstallPath: "/a/clipfan", GatewayPath: "/a/clipfan", KnownHostsPath: "/a/kh", SyncKeyPath: "/a/sk", ConfigPath: "/a/config.json"},
		"B": {Origin: "B", InstallPath: "/b/clipfan", GatewayPath: "/b/clipfan", KnownHostsPath: "/b/kh", SyncKeyPath: "/b/sk", ConfigPath: "/b/config.json"},
	}
	endpoints := map[string]rosterEndpoint{
		"A": {ID: "A", SSHUser: "jesse", SSHHost: "hostA", SSHPort: 22},
		// B has no endpoint (e.g. self-address unobserved) -> must be skipped.
	}
	hosts, configPaths := buildProvisionHosts(reports, endpoints)
	if len(hosts) != 1 || hosts[0].Host.ID != "A" {
		t.Fatalf("hosts = %+v", hosts)
	}
	h := hosts[0]
	if h.Host.SSHHost != "hostA" || h.Host.InstallPath != "/a/clipfan" || h.Host.GatewayPath != "/a/clipfan" || h.KnownHostsPath != "/a/kh" || h.SyncKeyPath != "/a/sk" {
		t.Fatalf("host A = %+v", h)
	}
	if configPaths["A"] != "/a/config.json" {
		t.Fatalf("config paths = %+v", configPaths)
	}
	if _, ok := configPaths["B"]; ok {
		t.Fatalf("B should be absent from config paths")
	}
}

// fakeMeshRunner answers every non-provision command runMeshHeal issues over SSH:
// ssh -G resolve, ssh-keyscan, ssh-keygen -F (always "no match"), roster-read,
// the self-address snippet, and the restart script.
type fakeMeshRunner struct {
	reports     map[string]string // ssh target host -> roster-read JSON
	selfAddr    string
	failRead    map[string]bool // ssh target host -> roster-read errors
	restarts    []string        // ssh target hosts asked to restart
	rosterReads []string        // ssh target hosts roster-read
}

func targetHost(args []string) string {
	if len(args) < 2 {
		return ""
	}
	t := args[len(args)-2]
	if i := strings.Index(t, "@"); i >= 0 {
		return t[i+1:]
	}
	return t
}

func (r *fakeMeshRunner) Run(_ context.Context, command sshprovision.SSHCommand) (sshprovision.CommandOutput, error) {
	args := command.Args
	switch {
	case args[0] == "ssh-keygen" && len(args) > 1 && args[1] == "-F":
		return sshprovision.CommandOutput{}, fakeExitError{code: 1}
	case args[0] == "ssh-keyscan":
		host := args[len(args)-1]
		return sshprovision.CommandOutput{Stdout: []byte(host + " ssh-ed25519 " + testDirectProvisionPublicKey(host))}, nil
	case args[0] == "ssh" && len(args) > 1 && args[1] == "-G":
		host := args[len(args)-1]
		return sshprovision.CommandOutput{Stdout: []byte("hostname " + host + "\nport 22\n")}, nil
	}
	host := targetHost(args)
	remote := args[len(args)-1]
	switch {
	case strings.Contains(remote, "roster-read"):
		r.rosterReads = append(r.rosterReads, host)
		if r.failRead[host] {
			return sshprovision.CommandOutput{}, errors.New("connection refused")
		}
		return sshprovision.CommandOutput{Stdout: []byte(r.reports[host])}, nil
	case strings.Contains(remote, "SSH_CONNECTION"):
		return sshprovision.CommandOutput{Stdout: []byte(r.selfAddr + "\n")}, nil
	case strings.Contains(remote, "clipfan.service"):
		r.restarts = append(r.restarts, host)
		return sshprovision.CommandOutput{}, nil
	}
	return sshprovision.CommandOutput{}, errors.New("unexpected: " + strings.Join(args, " "))
}

func healthyMeshPeer(id, user, host, install string) RosterReadPeer {
	return RosterReadPeer{
		ID: id, SSHUser: user, SSHHost: host, SSHPort: 22, InstallPath: install, GatewayPath: install,
		Enabled: true, Accept: true, Connect: true, MigrationState: "ssh_keys_ready",
		AcceptKeyID: "ak-" + id, ConnectKeyID: "ck-" + id,
	}
}

func meshHealTestOptions(t *testing.T, runner sshprovision.CommandRunner, cfg *config.Config, env rosterReadEnv) ([]string, meshHealOptions) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"--trust-keyscan", "--regular-known-hosts", dir + "/regular_known_hosts"}
	return args, meshHealOptions{
		Runner:            runner,
		ConfigV2WriteGate: func() bool { return true },
		SharedKey:         func() (string, error) { return testDirectProvisionSharedKey, nil },
		LoadConfig:        func() (*config.Config, error) { return cfg, nil },
		SelfEnv:           func() rosterReadEnv { return env },
	}
}

func TestRunMeshHealSkipsHealthyMeshAndReportsNoFailures(t *testing.T) {
	cfg := &config.Config{
		Hostname:  "L",
		SharedKey: testDirectProvisionSharedKey,
		SSH: &config.SSHConfig{
			KnownHosts: "/l/kh", SyncKey: "/l/sk",
			Peers: []config.SSHPeer{{
				ID: "R", SSHUser: "jesse", SSHHost: "rhost", SSHPort: 22, InstallPath: "/r/clipfan",
				Enabled: true, Accept: true, Connect: true, MigrationState: "ssh_keys_ready",
				Proof: config.SSHProof{AcceptKeyID: "ak-R", ConnectKeyID: "ck-R"},
			}},
		},
	}
	rReport, _ := json.Marshal(RosterReadReport{
		Origin: "R", Platform: "linux", UID: 1000,
		ConfigPath: "/r/config.json", KnownHostsPath: "/r/kh", SyncKeyPath: "/r/sk",
		InstallPath: "/r/clipfan", GatewayPath: "/r/clipfan",
		Peers: []RosterReadPeer{healthyMeshPeer("L", "jesse", "lhost", "/l/clipfan")},
	})
	runner := &fakeMeshRunner{reports: map[string]string{"rhost": string(rReport)}, selfAddr: "100.114.54.38"}
	env := rosterReadEnv{GOOS: "darwin", UID: 501, SelfBinaryPath: "/l/clipfan", ConfigPath: "/l/config.json"}

	args, opts := meshHealTestOptions(t, runner, cfg, env)
	var out, errBuf strings.Builder
	if err := runMeshHeal(args, &out, &errBuf, opts); err != nil {
		t.Fatalf("runMeshHeal: %v (stderr=%s)", err, errBuf.String())
	}

	var report MeshHealReport
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatalf("decode report: %v (%s)", err, out.String())
	}
	if len(report.Skipped) != 1 || report.Skipped[0] != "L<->R" {
		t.Fatalf("skipped = %+v", report.Skipped)
	}
	if len(report.Healed) != 0 || len(report.Failed) != 0 || len(report.Restarted) != 0 || len(report.Unreachable) != 0 {
		t.Fatalf("expected a clean healthy-mesh report, got %+v", report)
	}
}

func TestRunMeshHealReportsUnreachablePeer(t *testing.T) {
	cfg := &config.Config{
		Hostname:  "L",
		SharedKey: testDirectProvisionSharedKey,
		SSH: &config.SSHConfig{
			KnownHosts: "/l/kh", SyncKey: "/l/sk",
			Peers: []config.SSHPeer{{
				ID: "R", SSHUser: "jesse", SSHHost: "rhost", SSHPort: 22, InstallPath: "/r/clipfan",
				Enabled: true, Accept: true, Connect: true, MigrationState: "ssh_keys_ready",
				Proof: config.SSHProof{AcceptKeyID: "ak-R", ConnectKeyID: "ck-R"},
			}},
		},
	}
	runner := &fakeMeshRunner{
		reports:  map[string]string{},
		selfAddr: "100.114.54.38",
		failRead: map[string]bool{"rhost": true},
	}
	env := rosterReadEnv{GOOS: "darwin", UID: 501, SelfBinaryPath: "/l/clipfan", ConfigPath: "/l/config.json"}

	args, opts := meshHealTestOptions(t, runner, cfg, env)
	var out, errBuf strings.Builder
	if err := runMeshHeal(args, &out, &errBuf, opts); err != nil {
		t.Fatalf("runMeshHeal: %v (stderr=%s)", err, errBuf.String())
	}

	var report MeshHealReport
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatalf("decode: %v (%s)", err, out.String())
	}
	if len(report.Unreachable) != 1 || report.Unreachable[0].ID != "R" {
		t.Fatalf("unreachable = %+v", report.Unreachable)
	}
	// Only L remains in the roster, so there are no edges to heal or skip.
	if len(report.Healed) != 0 || len(report.Skipped) != 0 || len(report.Failed) != 0 {
		t.Fatalf("expected no edges, got %+v", report)
	}
}
