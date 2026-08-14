package cli

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

// RunAddPeer provisions a single new peer by wrapping ssh-provision-direct.
// It auto-fills the local host from the local config and accepts the peer's
// details via flags (or interactive prompts if flags are omitted).
func RunAddPeer(args []string, stdout io.Writer, stderr io.Writer) error {
	fs := flag.NewFlagSet("add-peer", flag.ContinueOnError)
	fs.SetOutput(stderr)

	peerHost := fs.String("host", "", "peer SSH host (Tailscale IP or hostname)")
	peerUser := fs.String("user", "", "peer SSH username")
	peerPort := fs.Int("port", 22, "peer SSH port")
	peerHome := fs.String("peer-home", "", "peer home dir (e.g. /home/user, /Users/user, C:\\Users\\Name)")
	localSSH := fs.String("local-ssh", "", "local SSH address the peer uses to reach us (auto-detected if blank)")
	localUserOverride := fs.String("local-user", "", "local SSH username (defaults to the current account name)")

	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: clipfan add-peer [flags]")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Provisions a new clipfan peer over SSH. Run with no flags for interactive mode.")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Flags:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	reader := bufio.NewReader(os.Stdin)
	if *peerHost == "" {
		fmt.Fprint(stderr, "Peer SSH host (Tailscale IP/hostname): ")
		line, _ := reader.ReadString('\n')
		*peerHost = strings.TrimSpace(line)
	}
	if *peerUser == "" {
		fmt.Fprint(stderr, "Peer SSH username: ")
		line, _ := reader.ReadString('\n')
		*peerUser = strings.TrimSpace(line)
	}
	if *peerHome == "" {
		fmt.Fprint(stderr, "Peer home dir (e.g. /home/user, C:\\Users\\Name): ")
		line, _ := reader.ReadString('\n')
		*peerHome = strings.TrimSpace(line)
	}
	if *peerHost == "" || *peerUser == "" || *peerHome == "" {
		return fmt.Errorf("host, user, and peer-home are required")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading local config: %w", err)
	}

	currentUser, _ := user.Current()
	localUser := currentUser.Username
	// Windows may report the SAM-compatible form ("OAK-WINDOWS\Will Wade");
	// SSH usernames are the bare account name, so strip any domain prefix.
	if i := strings.LastIndex(localUser, `\`); i >= 0 {
		localUser = localUser[i+1:]
	}
	if *localUserOverride != "" {
		localUser = *localUserOverride
	}
	if localUser == "" {
		localUser = "clipfan"
	}
	localID := cfg.Hostname
	if localID == "" {
		hostname, _ := os.Hostname()
		localID = hostname
	}

	localAddr := *localSSH
	if localAddr == "" {
		localAddr = autoDetectTailscaleIP()
	}
	if localAddr == "" {
		return fmt.Errorf("could not auto-detect local SSH address; pass --local-ssh <ip>")
	}

	localConfigPath := crossPeerPath(config.Path())
	localInstallPath, _ := os.Executable()
	if localInstallPath == "" {
		localInstallPath = crossPeerPath(currentUser.HomeDir, ".local", "bin", "clipfan")
	} else {
		// Remote commands execute through the peer's shell (zsh/bash/Git
		// Bash); backslash Windows paths break there ("C:UsersWill: command
		// not found"), so normalize to forward slashes for cross-shell use.
		localInstallPath = crossPeerPath(localInstallPath)
	}
	// cfg.SSH is nil on a freshly-installed host (no SSH transport configured
	// yet); fall back to the conventional <configdir>/ssh paths, matching the
	// Mac app's spec (AddPeerSheet) and the daemon's defaults.
	configDir := crossPeerPath(filepath.Dir(config.Path()))
	localKnownHosts := crossPeerPath(configDir, "ssh", "known_hosts")
	localSyncKey := crossPeerPath(configDir, "ssh", "sync_ed25519")
	if cfg.SSH != nil {
		if cfg.SSH.KnownHosts != "" {
			localKnownHosts = crossPeerPath(cfg.SSH.KnownHosts)
		}
		if cfg.SSH.SyncKey != "" {
			localSyncKey = crossPeerPath(cfg.SSH.SyncKey)
		}
	}

	// The peer's pair-plan ID must match the host_id recorded in its sync
	// key sidecar (identity checks reject a mismatch: sync_key_identity_
	// mismatch). The sidecar uses the hostname, so probe it over SSH rather
	// than defaulting to the raw IP.
	peerID := *peerHost
	if probed, err := probePeerHostname(*peerHost, *peerUser, *peerPort); err == nil && probed != "" {
		peerID = probed
	}
	binaryName := "clipfan"
	if len(*peerHome) >= 2 && (*peerHome)[1] == ':' {
		binaryName = "clipfan.exe"
	}
	peerConfigPath := crossPeerPath(*peerHome, ".config", "clipfan", "config.json")
	peerInstallPath := crossPeerPath(*peerHome, ".local", "bin", binaryName)
	peerKnownHosts := crossPeerPath(*peerHome, ".config", "clipfan", "ssh", "known_hosts")
	peerSyncKey := crossPeerPath(*peerHome, ".config", "clipfan", "ssh", "sync_ed25519")

	localSpec := fmt.Sprintf("id=%s,ssh=%s,user=%s,port=22,install=%s,config=%s,known_hosts=%s,sync_key=%s",
		localID, localAddr, localUser, localInstallPath, localConfigPath, localKnownHosts, localSyncKey)
	peerSpec := fmt.Sprintf("id=%s,ssh=%s,user=%s,port=%s,install=%s,config=%s,known_hosts=%s,sync_key=%s",
		peerID, *peerHost, *peerUser, strconv.Itoa(*peerPort),
		peerInstallPath, peerConfigPath, peerKnownHosts, peerSyncKey)

	provisionArgs := []string{"--trust-keyscan", "--host", localSpec, "--host", peerSpec}
	// The provisioning flow SSHes to BOTH hosts over the user's regular SSH
	// (BatchMode + StrictHostKeyChecking=yes against ~/.ssh/known_hosts) —
	// including to this host itself. On macOS bootstrap-self-ssh.sh seeds the
	// self pin there via accept-new; nothing does that on Windows or a bare
	// Linux box, so add-peer TOFU-seeds missing pins for both endpoints
	// (consistent with --trust-keyscan, which the caller opted into).
	if err := ensureRegularKnownHostPins(*peerPort, localAddr, *peerHost); err != nil {
		fmt.Fprintf(stderr, "add-peer: warning: seeding regular known_hosts pins: %v\n", err)
	}
	fmt.Fprintf(stderr, "Provisioning peer %s@%s:%d...\n", *peerUser, *peerHost, *peerPort)
	return runSSHProvisionDirect(provisionArgs, stdout, stderr, sshProvisionDirectOptions{})
}

// ensureRegularKnownHostPins appends ssh-ed25519 pins for any of hosts that
// are missing from the user's ~/.ssh/known_hosts, fetched via ssh-keyscan.
// Best-effort: errors are reported by the caller as warnings.
func ensureRegularKnownHostPins(port int, hosts ...string) error {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return fmt.Errorf("home dir unavailable")
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return err
	}
	khPath := filepath.Join(sshDir, "known_hosts")
	existing, err := os.ReadFile(khPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var appended bool
	for _, host := range hosts {
		if host == "" {
			continue
		}
		if bytes.Contains(existing, []byte(host+" ssh-ed25519 ")) {
			continue
		}
		out, err := (sshprovision.ExecCommandRunner{}).Run(context.Background(), sshprovision.SSHCommand{Args: []string{
			"ssh-keyscan", "-T", "5", "-p", strconv.Itoa(port), "-t", "ed25519", host,
		}})
		if err != nil || len(out.Stdout) == 0 {
			continue // keyscan unavailable/offline: leave pinning to ssh itself
		}
		existing = append(existing, out.Stdout...)
		appended = true
	}
	if !appended {
		return nil
	}
	if err := os.WriteFile(khPath, existing, 0o600); err != nil {
		return err
	}
	return nil
}

// probePeerHostname asks the peer for its short hostname over regular SSH.
// Best-effort: on failure the caller falls back to the SSH address as the
// pair-plan host ID. Runs through ExecCommandRunner: on Windows, ssh.exe
// hangs at exit when its stdio is Go pipes, so the runner's temp-file
// capture is required here too.
func probePeerHostname(host, user string, port int) (string, error) {
	args := []string{
		"ssh",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=8",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "LogLevel=ERROR",
		"-p", strconv.Itoa(port),
		user + "@" + host,
		"hostname -s || hostname",
	}
	out, err := (sshprovision.ExecCommandRunner{}).Run(context.Background(), sshprovision.SSHCommand{Args: args})
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(string(out.Stdout))
	if idx := strings.Index(name, "\n"); idx >= 0 {
		name = strings.TrimSpace(name[:idx])
	}
	return name, nil
}

func autoDetectTailscaleIP() string {
	out, err := exec.Command("tailscale", "ip", "-4").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func crossPeerPath(base string, parts ...string) string {
	base = strings.ReplaceAll(base, "\\", "/")
	var b strings.Builder
	b.WriteString(base)
	for _, p := range parts {
		b.WriteString("/")
		b.WriteString(p)
	}
	return b.String()
}
