package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	TransportSSH = "ssh"

	MigrationStateLoopbackUnprovisioned      = "loopback_unprovisioned"
	MigrationStateSSHMaterialStaged          = "ssh_material_staged"
	MigrationStateSharedKeyWrittenUnverified = "shared_key_written_unverified"
	MigrationStateSSHKeysReady               = "ssh_keys_ready"
	MigrationStateProvisionFailed            = "provision_failed"

	DirectionAccept  = "accept"
	DirectionConnect = "connect"

	ProofVerifiedByLocalFile    = "local_file"
	ProofVerifiedByRegularSSH   = "regular_ssh"
	ProofVerifiedByTailscaleSSH = "tailscale_ssh"
)

type MigrationState string
type ProofDirection string

type SSHConfig struct {
	SyncKey            string    `json:"sync_key,omitempty"`
	KnownHosts         string    `json:"known_hosts,omitempty"`
	MaxSessions        int       `json:"max_sessions,omitempty"`
	MaxSessionsPerPeer int       `json:"max_sessions_per_peer,omitempty"`
	LogLimitBytes      int       `json:"log_limit_bytes,omitempty"`
	Peers              []SSHPeer `json:"peers,omitempty"`
}

type SSHPeer struct {
	ID             string         `json:"id"`
	SSHHost        string         `json:"ssh_host,omitempty"`
	SSHUser        string         `json:"ssh_user,omitempty"`
	SSHPort        int            `json:"ssh_port,omitempty"`
	InstallPath    string         `json:"install_path,omitempty"`
	GatewayPath    string         `json:"gateway_path,omitempty"`
	Enabled        bool           `json:"enabled,omitempty"`
	Accept         bool           `json:"accept,omitempty"`
	Connect        bool           `json:"connect,omitempty"`
	Persistent     bool           `json:"persistent,omitempty"`
	OnDemand       bool           `json:"on_demand,omitempty"`
	MigrationState MigrationState `json:"migration_state,omitempty"`
	Proof          SSHProof       `json:"proof,omitempty"`
}

type SSHProof struct {
	AcceptKeyID        string `json:"accept_key_id,omitempty"`
	AcceptGatewayPath  string `json:"accept_gateway_path,omitempty"`
	AcceptVerifiedAt   string `json:"accept_verified_at,omitempty"`
	AcceptVerifiedBy   string `json:"accept_verified_by,omitempty"`
	ConnectKeyID       string `json:"connect_key_id,omitempty"`
	ConnectGatewayPath string `json:"connect_gateway_path,omitempty"`
	ConnectVerifiedAt  string `json:"connect_verified_at,omitempty"`
	ConnectVerifiedBy  string `json:"connect_verified_by,omitempty"`
}

var (
	proofKeyIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)
	// sshPathPattern is the strict POSIX path charset (no spaces). Windows
	// drive-letter paths use sshWindowsPathPattern instead: they legitimately
	// contain a colon (drive), backslashes, and spaces ("Program Files").
	// Selecting the charset by path shape keeps a spaced POSIX path rejected
	// (TestRunSSHInstallAuthorizedKeyRejectsInvalidInputWithoutWriting) while
	// Windows peer paths pass.
	sshPathPattern        = regexp.MustCompile(`^[A-Za-z0-9._/@+-]+$`)
	sshWindowsPathPattern = regexp.MustCompile(`^[A-Za-z0-9._/ \\:-]+$`)
	// sshUserPattern permits a space (e.g. "will wade") and '@' (email-style
	// names), which Windows and other systems allow in logon names. This is safe:
	// the SSH user is only ever passed to ssh as an exec argument ("user@host"),
	// never interpolated into a shell string. Shell metacharacters remain rejected.
	sshUserPattern = regexp.MustCompile(`^[A-Za-z0-9._@ -]{1,128}$`)
)

func ValidateSSHTransportConfig(cfg Config) error {
	activeSSH := false
	switch cfg.Transport {
	case "":
	case TransportSSH:
		activeSSH = true
	default:
		return fmt.Errorf("invalid_transport: %s", cfg.Transport)
	}
	if cfg.SSH == nil {
		return nil
	}
	if cfg.SSH.MaxSessions < 0 {
		return fmt.Errorf("invalid_ssh_max_sessions")
	}
	if cfg.SSH.MaxSessionsPerPeer < 0 {
		return fmt.Errorf("invalid_ssh_max_sessions_per_peer")
	}
	if cfg.SSH.MaxSessions > 0 && cfg.SSH.MaxSessionsPerPeer > cfg.SSH.MaxSessions {
		return fmt.Errorf("invalid_ssh_max_sessions_per_peer")
	}
	if cfg.SSH.LogLimitBytes < 0 {
		return fmt.Errorf("invalid_ssh_log_limit_bytes")
	}
	if cfg.SSH.SyncKey != "" {
		if err := ValidateSSHExecutablePath(cfg.SSH.SyncKey); err != nil {
			return fmt.Errorf("invalid_sync_key: %w", err)
		}
	}
	if cfg.SSH.KnownHosts != "" {
		if err := ValidateSSHExecutablePath(cfg.SSH.KnownHosts); err != nil {
			return fmt.Errorf("invalid_known_hosts: %w", err)
		}
	}

	seen := map[string]struct{}{}
	targets := map[string]string{}
	for _, peer := range cfg.SSH.Peers {
		if err := validateSSHPeer(cfg.Hostname, peer, activeSSH); err != nil {
			return err
		}
		if _, ok := seen[peer.ID]; ok {
			return fmt.Errorf("duplicate_ssh_peer_id: %s", peer.ID)
		}
		seen[peer.ID] = struct{}{}
		if activeSSH && peer.Enabled && peer.Connect {
			host, err := CanonicalSSHHost(peer.SSHHost)
			if err != nil {
				return fmt.Errorf("invalid_ssh_host: %w", err)
			}
			key := peer.SSHUser + "\x00" + host + "\x00" + strconv.Itoa(peer.SSHPort)
			if other := targets[key]; other != "" {
				return fmt.Errorf("duplicate_ssh_target: %s and %s", other, peer.ID)
			}
			targets[key] = peer.ID
		}
	}
	return nil
}

func validateSSHPeer(localHostID string, peer SSHPeer, activeSSH bool) error {
	if err := ValidateHostID(peer.ID); err != nil {
		return fmt.Errorf("invalid_ssh_peer_id: %w", err)
	}
	if localHostID != "" && peer.ID == localHostID {
		return fmt.Errorf("ssh_peer_id_is_local_host: %s", peer.ID)
	}
	if peer.SSHPort < 0 || peer.SSHPort > 65535 {
		return fmt.Errorf("invalid_ssh_port: %d", peer.SSHPort)
	}
	if peer.InstallPath != "" {
		if err := ValidateSSHExecutablePath(peer.InstallPath); err != nil {
			return fmt.Errorf("invalid_install_path: %w", err)
		}
	}
	if peer.GatewayPath != "" {
		if err := ValidateSSHExecutablePath(peer.GatewayPath); err != nil {
			return fmt.Errorf("invalid_gateway_path: %w", err)
		}
	}
	if !activeSSH {
		return nil
	}
	if !validMigrationState(peer.MigrationState) {
		return fmt.Errorf("invalid_migration_state: %s", peer.MigrationState)
	}
	if peer.Connect {
		if peer.SSHHost == "" || peer.SSHUser == "" || peer.SSHPort == 0 {
			return fmt.Errorf("missing_connect_locator: %s", peer.ID)
		}
		if err := ValidateSSHUser(peer.SSHUser); err != nil {
			return fmt.Errorf("invalid_ssh_user: %w", err)
		}
		if _, err := CanonicalSSHHost(peer.SSHHost); err != nil {
			return fmt.Errorf("invalid_ssh_host: %w", err)
		}
	} else if peer.Persistent || peer.OnDemand {
		return fmt.Errorf("invalid_accept_only_peer_outbound_mode: %s", peer.ID)
	}
	if hasDirectionalProof(peer.Proof, DirectionAccept) {
		if err := validateProofFields(peer.Proof.AcceptKeyID, peer.Proof.AcceptGatewayPath, peer.Proof.AcceptVerifiedAt, peer.Proof.AcceptVerifiedBy); err != nil {
			return fmt.Errorf("invalid_accept_proof: %w", err)
		}
	}
	if hasDirectionalProof(peer.Proof, DirectionConnect) {
		if err := validateProofFields(peer.Proof.ConnectKeyID, peer.Proof.ConnectGatewayPath, peer.Proof.ConnectVerifiedAt, peer.Proof.ConnectVerifiedBy); err != nil {
			return fmt.Errorf("invalid_connect_proof: %w", err)
		}
	}
	if peer.MigrationState == MigrationStateSSHKeysReady {
		if err := ValidateDirectionalProof(peer, DirectionAccept); err != nil {
			return err
		}
		if err := ValidateDirectionalProof(peer, DirectionConnect); err != nil {
			return err
		}
	}
	return nil
}

func validMigrationState(state MigrationState) bool {
	switch state {
	case "",
		MigrationStateLoopbackUnprovisioned,
		MigrationStateSSHMaterialStaged,
		MigrationStateSharedKeyWrittenUnverified,
		MigrationStateSSHKeysReady,
		MigrationStateProvisionFailed:
		return true
	default:
		return false
	}
}

func ValidateDirectionalProof(peer SSHPeer, direction ProofDirection) error {
	switch direction {
	case DirectionAccept:
		if !peer.Enabled || !peer.Accept {
			return nil
		}
		return validateProofFields(peer.Proof.AcceptKeyID, peer.Proof.AcceptGatewayPath, peer.Proof.AcceptVerifiedAt, peer.Proof.AcceptVerifiedBy)
	case DirectionConnect:
		if !peer.Enabled || !peer.Connect {
			return nil
		}
		return validateProofFields(peer.Proof.ConnectKeyID, peer.Proof.ConnectGatewayPath, peer.Proof.ConnectVerifiedAt, peer.Proof.ConnectVerifiedBy)
	default:
		return fmt.Errorf("invalid_proof_direction: %s", direction)
	}
}

func hasDirectionalProof(proof SSHProof, direction ProofDirection) bool {
	switch direction {
	case DirectionAccept:
		return proof.AcceptKeyID != "" || proof.AcceptGatewayPath != "" || proof.AcceptVerifiedAt != "" || proof.AcceptVerifiedBy != ""
	case DirectionConnect:
		return proof.ConnectKeyID != "" || proof.ConnectGatewayPath != "" || proof.ConnectVerifiedAt != "" || proof.ConnectVerifiedBy != ""
	default:
		return false
	}
}

func validateProofFields(keyID, gatewayPath, verifiedAt, verifiedBy string) error {
	if !proofKeyIDPattern.MatchString(keyID) {
		return fmt.Errorf("invalid_proof_key_id")
	}
	if err := ValidateSSHExecutablePath(gatewayPath); err != nil {
		return fmt.Errorf("invalid_proof_gateway_path: %w", err)
	}
	if _, err := time.Parse(time.RFC3339, verifiedAt); err != nil {
		return fmt.Errorf("invalid_proof_verified_at: %w", err)
	}
	switch verifiedBy {
	case ProofVerifiedByLocalFile, ProofVerifiedByRegularSSH, ProofVerifiedByTailscaleSSH:
		return nil
	default:
		return fmt.Errorf("invalid_proof_verified_by: %s", verifiedBy)
	}
}

func ValidateSSHExecutablePath(value string) error {
	if err := ValidateSafeAbsolutePath(value); err != nil {
		return fmt.Errorf("invalid_ssh_path: %w", err)
	}
	pattern := sshPathPattern
	if isWindowsDrivePath(value) {
		pattern = sshWindowsPathPattern
	}
	if !pattern.MatchString(value) {
		return fmt.Errorf("invalid_ssh_path: invalid characters")
	}
	return nil
}

func isAbsoluteCrossPlatform(value string) bool {
	if strings.HasPrefix(value, "/") {
		return true
	}
	return isWindowsDrivePath(value)
}

func isWindowsDrivePath(value string) bool {
	return len(value) >= 3 &&
		((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) &&
		value[1] == ':' &&
		(value[2] == '\\' || value[2] == '/')
}

func ValidateSafeAbsolutePath(value string) error {
	if value == "" {
		return errors.New("empty")
	}
	if !isAbsoluteCrossPlatform(value) {
		return errors.New("relative")
	}
	if strings.ContainsAny(value, "\x00\n\r\t") {
		return errors.New("invalid characters")
	}
	if !isWindowsDrivePath(value) {
		cleaned := path.Clean(value)
		if cleaned != value {
			return errors.New("not canonical")
		}
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return errors.New("parent traversal")
		}
	}
	return nil
}

func ValidateSSHUser(value string) error {
	if !sshUserPattern.MatchString(value) {
		return fmt.Errorf("invalid_ssh_user")
	}
	if strings.HasPrefix(value, "-") {
		return fmt.Errorf("invalid_ssh_user")
	}
	return nil
}

func CanonicalSSHHost(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("empty")
	}
	if len(value) > 253 {
		return "", fmt.Errorf("too long")
	}
	if strings.TrimSpace(value) != value {
		return "", fmt.Errorf("contains whitespace")
	}
	if strings.HasPrefix(value, "-") {
		return "", fmt.Errorf("starts with hyphen")
	}
	if strings.ContainsAny(value, "@/\\\"'`$;&|<>(){}[]*?!") {
		return "", fmt.Errorf("contains invalid character")
	}
	if ip := net.ParseIP(value); ip != nil {
		if strings.Contains(value, "%") {
			return "", fmt.Errorf("scoped ipv6 is unsupported")
		}
		return strings.ToLower(ip.String()), nil
	}
	if strings.Contains(value, ":") {
		return "", fmt.Errorf("contains port suffix")
	}
	if !isASCII(value) {
		return "", fmt.Errorf("non-ascii host")
	}
	host := strings.ToLower(strings.TrimSuffix(value, "."))
	if host == "" {
		return "", fmt.Errorf("empty")
	}
	if len(host) > 253 {
		return "", fmt.Errorf("too long")
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" {
			return "", fmt.Errorf("empty label")
		}
		if len(label) > 63 {
			return "", fmt.Errorf("label too long")
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", fmt.Errorf("invalid label hyphen")
		}
		for _, ch := range label {
			if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-' {
				continue
			}
			return "", fmt.Errorf("invalid dns label")
		}
	}
	return host, nil
}

func isASCII(value string) bool {
	for _, ch := range value {
		if ch > 127 {
			return false
		}
	}
	return true
}

func NormalizeLocalSSHPaths(cfg *Config) error {
	if cfg == nil || cfg.SSH == nil {
		return nil
	}
	if !needsHomeExpansion(cfg.SSH.SyncKey) && !needsHomeExpansion(cfg.SSH.KnownHosts) {
		return nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home_dir_unavailable: %w", err)
	}
	cfg.SSH.SyncKey = ExpandLocalSSHPath(cfg.SSH.SyncKey, homeDir)
	cfg.SSH.KnownHosts = ExpandLocalSSHPath(cfg.SSH.KnownHosts, homeDir)
	return nil
}

func needsHomeExpansion(value string) bool {
	return value == "~" || strings.HasPrefix(value, "~/")
}

func ExpandLocalSSHPath(value, homeDir string) string {
	if value == "~" {
		return homeDir
	}
	if strings.HasPrefix(value, "~/") {
		return path.Join(homeDir, strings.TrimPrefix(value, "~/"))
	}
	return value
}
