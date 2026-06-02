package config

import (
	"strings"
	"testing"
)

func TestValidateSSHConfigAcceptsSchemaAndDirectionalProofs(t *testing.T) {
	home := t.TempDir()
	cfg := Config{
		ConfigVersion: intPtr(2),
		Hostname:      "m4",
		Transport:     TransportSSH,
		SSH: &SSHConfig{
			SyncKey:            home + "/.config/clipfan/ssh/sync_ed25519",
			KnownHosts:         home + "/.config/clipfan/ssh/known_hosts",
			MaxSessions:        16,
			MaxSessionsPerPeer: 2,
			LogLimitBytes:      65536,
			Peers: []SSHPeer{{
				ID:             "fsck",
				SSHHost:        "fsck.com",
				SSHUser:        "jesse",
				SSHPort:        22,
				InstallPath:    "/home/jesse/.local/bin/clipfan",
				GatewayPath:    "/home/jesse/.local/bin/clipfan",
				Enabled:        true,
				Accept:         true,
				Connect:        true,
				Persistent:     true,
				OnDemand:       true,
				MigrationState: MigrationStateSSHKeysReady,
				Proof: SSHProof{
					AcceptKeyID:        "a4a4a4a4a4a4a4a4",
					AcceptGatewayPath:  "/Users/jesse/.local/bin/clipfan",
					AcceptVerifiedAt:   "2026-06-01T12:34:56Z",
					AcceptVerifiedBy:   ProofVerifiedByLocalFile,
					ConnectKeyID:       "b5b5b5b5b5b5b5b5",
					ConnectGatewayPath: "/home/jesse/.local/bin/clipfan",
					ConnectVerifiedAt:  "2026-06-01T12:35:10Z",
					ConnectVerifiedBy:  ProofVerifiedByRegularSSH,
				},
			}},
		},
	}

	if err := ValidateSSHTransportConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if got := ExpandLocalSSHPath("~/.config/clipfan/ssh/sync_ed25519", "/Users/jesse"); got != "/Users/jesse/.config/clipfan/ssh/sync_ed25519" {
		t.Fatalf("ExpandLocalSSHPath = %q", got)
	}
}

func TestValidateSSHConfigRejectsInvalidTransportAndPeers(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		code string
	}{
		{
			name: "invalid transport",
			cfg:  Config{ConfigVersion: intPtr(2), Transport: "http"},
			code: "invalid_transport",
		},
		{
			name: "duplicate peer id",
			cfg: Config{ConfigVersion: intPtr(2), Transport: TransportSSH, SSH: &SSHConfig{
				Peers: []SSHPeer{{ID: "fsck"}, {ID: "fsck"}},
			}},
			code: "duplicate_ssh_peer_id",
		},
		{
			name: "peer id equals local hostname",
			cfg: Config{ConfigVersion: intPtr(2), Hostname: "m4", Transport: TransportSSH, SSH: &SSHConfig{
				Peers: []SSHPeer{{ID: "m4"}},
			}},
			code: "ssh_peer_id_is_local_host",
		},
		{
			name: "invalid migration state",
			cfg: Config{ConfigVersion: intPtr(2), Transport: TransportSSH, SSH: &SSHConfig{
				Peers: []SSHPeer{{ID: "fsck", MigrationState: "legacy_http"}},
			}},
			code: "invalid_migration_state",
		},
		{
			name: "connect peer missing locator",
			cfg: Config{ConfigVersion: intPtr(2), Transport: TransportSSH, SSH: &SSHConfig{
				Peers: []SSHPeer{{ID: "fsck", Connect: true}},
			}},
			code: "missing_connect_locator",
		},
		{
			name: "connect peer invalid user",
			cfg: Config{ConfigVersion: intPtr(2), Transport: TransportSSH, SSH: &SSHConfig{
				Peers: []SSHPeer{{ID: "fsck", Connect: true, SSHHost: "fsck.com", SSHUser: "-jesse", SSHPort: 22}},
			}},
			code: "invalid_ssh_user",
		},
		{
			name: "connect peer invalid host",
			cfg: Config{ConfigVersion: intPtr(2), Transport: TransportSSH, SSH: &SSHConfig{
				Peers: []SSHPeer{{ID: "fsck", Connect: true, SSHHost: "fsck.com:22", SSHUser: "jesse", SSHPort: 22}},
			}},
			code: "invalid_ssh_host",
		},
		{
			name: "duplicate enabled outbound target",
			cfg: Config{ConfigVersion: intPtr(2), Transport: TransportSSH, SSH: &SSHConfig{
				Peers: []SSHPeer{
					{ID: "fsck", Enabled: true, Connect: true, SSHHost: "FSCK.COM.", SSHUser: "jesse", SSHPort: 22},
					{ID: "fsck2", Enabled: true, Connect: true, SSHHost: "fsck.com", SSHUser: "jesse", SSHPort: 22},
				},
			}},
			code: "duplicate_ssh_target",
		},
		{
			name: "accept only peer cannot request persistent",
			cfg: Config{ConfigVersion: intPtr(2), Transport: TransportSSH, SSH: &SSHConfig{
				Peers: []SSHPeer{{ID: "fsck", Accept: true, Persistent: true}},
			}},
			code: "invalid_accept_only_peer_outbound_mode",
		},
		{
			name: "accept only peer cannot request on demand",
			cfg: Config{ConfigVersion: intPtr(2), Transport: TransportSSH, SSH: &SSHConfig{
				Peers: []SSHPeer{{ID: "fsck", Accept: true, OnDemand: true}},
			}},
			code: "invalid_accept_only_peer_outbound_mode",
		},
		{
			name: "invalid gateway path",
			cfg: Config{ConfigVersion: intPtr(2), Transport: TransportSSH, SSH: &SSHConfig{
				Peers: []SSHPeer{{ID: "fsck", GatewayPath: "/home/jesse/bin/../clipfan"}},
			}},
			code: "invalid_gateway_path",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSSHTransportConfig(tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
		})
	}
}

func intPtr(v int) *int { return &v }

func TestValidateSSHConfigChecksDormantShapeButAllowsFutureState(t *testing.T) {
	if err := ValidateSSHTransportConfig(Config{ConfigVersion: intPtr(2), SSH: &SSHConfig{
		Peers: []SSHPeer{{
			ID:             "fsck",
			MigrationState: "future",
			Proof: SSHProof{
				ConnectVerifiedBy: "future_verifier",
			},
		}},
	}}); err != nil {
		t.Fatalf("dormant future ssh state should be preserved: %v", err)
	}

	cases := []struct {
		name string
		cfg  Config
		code string
	}{
		{
			name: "duplicate dormant peer id",
			cfg: Config{ConfigVersion: intPtr(2), SSH: &SSHConfig{
				Peers: []SSHPeer{{ID: "fsck"}, {ID: "fsck"}},
			}},
			code: "duplicate_ssh_peer_id",
		},
		{
			name: "unsafe dormant local path",
			cfg: Config{ConfigVersion: intPtr(2), SSH: &SSHConfig{
				SyncKey: "/Users/jesse/.config/clipfan/ssh/../sync_ed25519",
			}},
			code: "invalid_sync_key",
		},
		{
			name: "invalid dormant peer id",
			cfg: Config{ConfigVersion: intPtr(2), SSH: &SSHConfig{
				Peers: []SSHPeer{{ID: ""}},
			}},
			code: "invalid_ssh_peer_id",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSSHTransportConfig(tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
		})
	}
}

func TestValidateSSHConfigRejectsUnsafeLocalPaths(t *testing.T) {
	cases := []struct {
		name string
		ssh  SSHConfig
		code string
	}{
		{
			name: "relative sync key",
			ssh:  SSHConfig{SyncKey: "clipfan/ssh/sync_ed25519"},
			code: "invalid_sync_key",
		},
		{
			name: "unsafe known hosts",
			ssh:  SSHConfig{KnownHosts: "/Users/jesse/.config/clipfan/ssh/../known_hosts"},
			code: "invalid_known_hosts",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSSHTransportConfig(Config{ConfigVersion: intPtr(2), Transport: TransportSSH, SSH: &tc.ssh})
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
		})
	}
}

func TestValidateSSHUserAndCanonicalSSHHost(t *testing.T) {
	for _, user := range []string{"jesse", "j.sse_1", "Jesse-2"} {
		t.Run("valid user "+user, func(t *testing.T) {
			if err := ValidateSSHUser(user); err != nil {
				t.Fatalf("ValidateSSHUser(%q): %v", user, err)
			}
		})
	}
	for _, user := range []string{"", "-jesse", "bad user", "jesse@host", "jesse/root", "jesse:root"} {
		t.Run("invalid user "+user, func(t *testing.T) {
			if err := ValidateSSHUser(user); err == nil {
				t.Fatalf("ValidateSSHUser(%q) = nil, want error", user)
			}
		})
	}

	validHosts := map[string]string{
		"FSCK.COM.":        "fsck.com",
		"m4":               "m4",
		"192.0.2.1":        "192.0.2.1",
		"2001:db8::1":      "2001:db8::1",
		"XN--BCHER-KVA.EX": "xn--bcher-kva.ex",
	}
	for host, want := range validHosts {
		t.Run("valid host "+host, func(t *testing.T) {
			got, err := CanonicalSSHHost(host)
			if err != nil {
				t.Fatalf("CanonicalSSHHost(%q): %v", host, err)
			}
			if got != want {
				t.Fatalf("CanonicalSSHHost(%q) = %q, want %q", host, got, want)
			}
		})
	}
	for _, host := range []string{"", "-fsck.com", "fsck..com", ".fsck.com", "fsck.com:22", "jesse@fsck.com", "fsck com", "[2001:db8::1]", "fsck/com", "exämple.com"} {
		t.Run("invalid host "+host, func(t *testing.T) {
			if _, err := CanonicalSSHHost(host); err == nil {
				t.Fatalf("CanonicalSSHHost(%q) = nil error, want error", host)
			}
		})
	}
}

func TestValidateDirectionalProof(t *testing.T) {
	peer := SSHPeer{
		ID:      "fsck",
		Enabled: true,
		Connect: true,
		Proof: SSHProof{
			ConnectKeyID:       "b5b5b5b5b5b5b5b5",
			ConnectGatewayPath: "/home/jesse/.local/bin/clipfan",
			ConnectVerifiedAt:  "2026-06-01T12:35:10Z",
			ConnectVerifiedBy:  ProofVerifiedByRegularSSH,
		},
	}
	if err := ValidateDirectionalProof(peer, DirectionConnect); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDirectionalProof(peer, DirectionAccept); err != nil {
		t.Fatalf("disabled accept direction should not require proof: %v", err)
	}

	peer.Proof.ConnectKeyID = "bad"
	err := ValidateDirectionalProof(peer, DirectionConnect)
	if err == nil || !strings.Contains(err.Error(), "invalid_proof_key_id") {
		t.Fatalf("error = %v, want invalid_proof_key_id", err)
	}
}

func TestValidateSSHPath(t *testing.T) {
	valid := []string{
		"/home/jesse/.local/bin/clipfan",
		"/Users/jesse/.local/bin/clipfan",
		"/opt/clipfan/bin/clipfan-1.2.3",
	}
	for _, path := range valid {
		t.Run("valid "+path, func(t *testing.T) {
			if err := ValidateSSHExecutablePath(path); err != nil {
				t.Fatalf("ValidateSSHExecutablePath(%q): %v", path, err)
			}
		})
	}

	invalid := []string{
		"",
		"relative/bin/clipfan",
		"/home/jesse/bin/../clipfan",
		"/home/jesse/bin/clip fan",
		"/home/jesse/bin/clipfan;rm",
		"/home/jesse/bin/\nclipfan",
	}
	for _, path := range invalid {
		t.Run("invalid "+path, func(t *testing.T) {
			err := ValidateSSHExecutablePath(path)
			if err == nil || !strings.Contains(err.Error(), "invalid_ssh_path") {
				t.Fatalf("error = %v, want invalid_ssh_path", err)
			}
		})
	}
}
