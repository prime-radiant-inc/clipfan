package cli

import (
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/config"
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
