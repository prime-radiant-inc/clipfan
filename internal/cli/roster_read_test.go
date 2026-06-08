package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

func TestBuildRosterReadReportIncludesPathsAndPeersWithoutSecrets(t *testing.T) {
	cfg := &config.Config{
		Hostname:  "jesse-paradise-park",
		SharedKey: "SECRET_SHARED_KEY_SHOULD_NOT_APPEAR",
		SSH: &config.SSHConfig{
			KnownHosts: "/Users/jesse/.config/clipfan/ssh/known_hosts",
			SyncKey:    "/Users/jesse/.config/clipfan/ssh/sync_ed25519",
			Peers: []config.SSHPeer{
				{
					ID:             "m4",
					SSHHost:        "100.114.54.38",
					SSHUser:        "jesse",
					SSHPort:        22,
					InstallPath:    "/Users/jesse/.local/bin/clipfan",
					GatewayPath:    "/Users/jesse/.local/bin/clipfan",
					Enabled:        true,
					Accept:         true,
					Connect:        false, // must serialize as connect:false, not be dropped
					MigrationState: config.MigrationStateSSHKeysReady,
					Proof:          config.SSHProof{AcceptKeyID: "accept-kid", ConnectKeyID: "connect-kid"},
				},
			},
		},
	}
	env := rosterReadEnv{
		GOOS:           "darwin",
		UID:            501,
		SelfBinaryPath: "/Users/jesse/.local/bin/clipfan",
		ConfigPath:     "/Users/jesse/.config/clipfan/config.json",
		LocalAddresses: []string{"192.168.118.49", "192.168.118.83"},
	}

	report := buildRosterReadReport(cfg, env)

	if report.Origin != "jesse-paradise-park" {
		t.Fatalf("origin = %q", report.Origin)
	}
	if report.Platform != "darwin" || report.UID != 501 {
		t.Fatalf("platform/uid = %q/%d", report.Platform, report.UID)
	}
	if report.ConfigPath != env.ConfigPath {
		t.Fatalf("config_path = %q", report.ConfigPath)
	}
	if report.KnownHostsPath != cfg.SSH.KnownHosts || report.SyncKeyPath != cfg.SSH.SyncKey {
		t.Fatalf("known_hosts/sync_key = %q/%q", report.KnownHostsPath, report.SyncKeyPath)
	}
	// gateway_path defaults to the self install path (the codebase convention).
	if report.InstallPath != env.SelfBinaryPath || report.GatewayPath != env.SelfBinaryPath {
		t.Fatalf("install/gateway = %q/%q", report.InstallPath, report.GatewayPath)
	}
	if strings.Join(report.LocalAddresses, ",") != "192.168.118.49,192.168.118.83" {
		t.Fatalf("local_addresses = %v", report.LocalAddresses)
	}
	if len(report.Peers) != 1 {
		t.Fatalf("peers = %d", len(report.Peers))
	}
	p := report.Peers[0]
	if p.ID != "m4" || p.SSHHost != "100.114.54.38" || p.SSHPort != 22 || p.SSHUser != "jesse" {
		t.Fatalf("peer endpoint = %+v", p)
	}
	if p.MigrationState != "ssh_keys_ready" || p.AcceptKeyID != "accept-kid" || p.ConnectKeyID != "connect-kid" {
		t.Fatalf("peer proof/state = %+v", p)
	}

	blob, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(blob)
	if strings.Contains(s, "SECRET_SHARED_KEY") {
		t.Fatalf("shared key leaked into roster-read output: %s", s)
	}
	// false booleans must be present, not omitted, so change-detection on the
	// other side can read them.
	if !strings.Contains(s, `"connect":false`) {
		t.Fatalf("connect:false was dropped (omitempty?): %s", s)
	}
	if !strings.Contains(s, `"accept":true`) || !strings.Contains(s, `"enabled":true`) {
		t.Fatalf("accept/enabled missing: %s", s)
	}
	if !strings.Contains(s, `"local_addresses":["192.168.118.49","192.168.118.83"]`) {
		t.Fatalf("local_addresses missing from JSON: %s", s)
	}
}

func TestBuildRosterReadReportNilSSHIsEmpty(t *testing.T) {
	report := buildRosterReadReport(&config.Config{Hostname: "solo"}, rosterReadEnv{GOOS: "linux", UID: 1000})
	if report.Origin != "solo" || len(report.Peers) != 0 {
		t.Fatalf("expected solo host with no peers, got %+v", report)
	}
	blob, _ := json.Marshal(report)
	if strings.Contains(string(blob), "local_addresses") {
		t.Fatalf("empty local_addresses must be omitted: %s", blob)
	}
}

// TestMeshHealSelfEnvPopulatesLocalAddresses guards the /par fix: the LOCAL host's
// self-report must carry LAN candidates too (not just remote hosts read over SSH),
// or local<->remote cross-tailnet edges can't fall back.
func TestMeshHealSelfEnvPopulatesLocalAddresses(t *testing.T) {
	env, err := meshHealSelfEnv(nil)
	if err != nil {
		t.Fatalf("meshHealSelfEnv() error = %v", err)
	}
	want, err := enumerateLocalIPv4Addresses(nil)
	if err != nil {
		t.Fatalf("enumerateLocalIPv4Addresses() error = %v", err)
	}
	if strings.Join(env.LocalAddresses, ",") != strings.Join(want, ",") {
		t.Fatalf("meshHealSelfEnv LocalAddresses = %v, want %v", env.LocalAddresses, want)
	}
}
