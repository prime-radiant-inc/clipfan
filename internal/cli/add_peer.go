package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"

	"github.com/prime-radiant-inc/clipfan/internal/config"
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

	localConfigPath := config.Path()
	localInstallPath, _ := os.Executable()
	if localInstallPath == "" {
		localInstallPath = crossPeerPath(currentUser.HomeDir, ".local", "bin", "clipfan")
	}
	localKnownHosts := cfg.SSH.KnownHosts
	localSyncKey := cfg.SSH.SyncKey

	peerID := *peerHost
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
	fmt.Fprintf(stderr, "Provisioning peer %s@%s:%d...\n", *peerUser, *peerHost, *peerPort)
	return runSSHProvisionDirect(provisionArgs, stdout, stderr, sshProvisionDirectOptions{})
}

func autoDetectTailscaleIP() string {
	out, err := exec.Command("tailscale", "ip", "-4").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func crossPeerPath(base string, parts ...string) string {
	sep := "/"
	if strings.Contains(base, `\`) {
		sep = `\`
	}
	var b strings.Builder
	b.WriteString(base)
	for _, p := range parts {
		b.WriteString(sep)
		b.WriteString(p)
	}
	return b.String()
}
