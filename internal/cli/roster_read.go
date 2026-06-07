package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

// RosterReadReport is what a host reports about ITSELF when the mesh-heal
// orchestrator runs `clipfan roster-read` on it over regular SSH. It carries the
// host's own paths/platform/uid (which a peer entry does not record) and its view
// of its peers. It contains NO secrets (no shared key, no key material) — only
// paths and public identifiers. Booleans are NOT omitempty so a false value
// (e.g. connect:false on a half-built edge) survives, which change-detection
// relies on.
type RosterReadReport struct {
	Origin         string           `json:"origin"`
	Platform       string           `json:"platform"`
	UID            int              `json:"uid"`
	ConfigPath     string           `json:"config_path"`
	KnownHostsPath string           `json:"known_hosts_path"`
	SyncKeyPath    string           `json:"sync_key_path"`
	InstallPath    string           `json:"install_path"`
	GatewayPath    string           `json:"gateway_path"`
	Peers          []RosterReadPeer `json:"peers"`
}

// RosterReadPeer is one host's view of one of its peers.
type RosterReadPeer struct {
	ID             string `json:"id"`
	SSHHost        string `json:"ssh_host"`
	SSHPort        int    `json:"ssh_port"`
	SSHUser        string `json:"ssh_user"`
	InstallPath    string `json:"install_path"`
	GatewayPath    string `json:"gateway_path"`
	MigrationState string `json:"migration_state"`
	Enabled        bool   `json:"enabled"`
	Accept         bool   `json:"accept"`
	Connect        bool   `json:"connect"`
	AcceptKeyID    string `json:"accept_key_id"`
	ConnectKeyID   string `json:"connect_key_id"`
}

// rosterReadEnv are the host-environment inputs, injected so buildRosterReadReport
// is testable without touching the real filesystem/runtime.
type rosterReadEnv struct {
	GOOS           string
	UID            int
	SelfBinaryPath string
	ConfigPath     string
}

// buildRosterReadReport assembles the self-report from the host's config and
// environment. gateway_path mirrors the self binary path, matching the codebase
// convention that gateway defaults to install (parseSSHProvisionDirectHost).
func buildRosterReadReport(cfg *config.Config, env rosterReadEnv) RosterReadReport {
	report := RosterReadReport{
		Origin:      cfg.Hostname,
		Platform:    env.GOOS,
		UID:         env.UID,
		ConfigPath:  env.ConfigPath,
		InstallPath: env.SelfBinaryPath,
		GatewayPath: env.SelfBinaryPath,
		Peers:       []RosterReadPeer{},
	}
	if cfg.SSH != nil {
		report.KnownHostsPath = cfg.SSH.KnownHosts
		report.SyncKeyPath = cfg.SSH.SyncKey
		for _, p := range cfg.SSH.Peers {
			report.Peers = append(report.Peers, RosterReadPeer{
				ID:             p.ID,
				SSHHost:        p.SSHHost,
				SSHPort:        p.SSHPort,
				SSHUser:        p.SSHUser,
				InstallPath:    p.InstallPath,
				GatewayPath:    p.GatewayPath,
				MigrationState: string(p.MigrationState),
				Enabled:        p.Enabled,
				Accept:         p.Accept,
				Connect:        p.Connect,
				AcceptKeyID:    p.Proof.AcceptKeyID,
				ConnectKeyID:   p.Proof.ConnectKeyID,
			})
		}
	}
	return report
}

// RunRosterRead prints this host's roster-read self-report as JSON. It is invoked
// over the user's regular SSH by mesh-heal; it reads only local config and reports
// no secrets, so it needs no authentication of its own.
func RunRosterRead(args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) != 0 {
		return fmt.Errorf("unexpected roster-read argument")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	report := buildRosterReadReport(cfg, rosterReadEnv{
		GOOS:           runtime.GOOS,
		UID:            os.Getuid(),
		SelfBinaryPath: self,
		ConfigPath:     config.Path(),
	})
	enc := json.NewEncoder(stdout)
	return enc.Encode(report)
}
