// Command clipfan-menu is the macOS menubar app for clipfan. It polls the
// local daemon for peer status and offers a one-click "install on remote..."
// action that scp's the right binary + unit file + shared config to a
// SSH-reachable host and starts the daemon there.
//
// Designed to be cheap: it does NOT replace the daemon, just observes and
// nudges it. The daemon keeps running independently.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/getlantern/systray"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/daemon"
)

//go:embed assets/icon.png
var iconBytes []byte

const (
	maxPeerSlots = 12
	pollInterval = 3 * time.Second
	healthURL    = "http://127.0.0.1:7853"
)

type peersResponse struct {
	Origin string             `json:"origin"`
	Peers  []daemon.PeerState `json:"peers"`
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	systray.Run(onReady, func() {})
}

func onReady() {
	systray.SetTemplateIcon(iconBytes, iconBytes)
	systray.SetTooltip("clipfan")

	mTitle := systray.AddMenuItem("clipfan", "")
	mTitle.Disable()
	mOrigin := systray.AddMenuItem("(daemon not running)", "")
	mOrigin.Disable()

	systray.AddSeparator()

	peerItems := make([]*systray.MenuItem, maxPeerSlots)
	for i := range peerItems {
		peerItems[i] = systray.AddMenuItem("", "")
		peerItems[i].Hide()
		peerItems[i].Disable()
	}

	systray.AddSeparator()
	mInstall := systray.AddMenuItem("Install on remote…", "scp + install clipfan on an SSH-reachable host")
	mOpenCfg := systray.AddMenuItem("Open config", "open ~/.config/clipfan/config.json")
	mRestart := systray.AddMenuItem("Restart daemon", "kill + relaunch the local clipfan daemon")
	mOpenLog := systray.AddMenuItem("Open daemon log", "open /tmp/clipfan-shell.log")

	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit menubar app", "(does not stop the daemon)")

	go handleClicks(mInstall, mOpenCfg, mRestart, mOpenLog, mQuit)
	go refreshLoop(mOrigin, peerItems)
}

func handleClicks(mInstall, mOpenCfg, mRestart, mOpenLog, mQuit *systray.MenuItem) {
	for {
		select {
		case <-mInstall.ClickedCh:
			host, ok := askHostname()
			if !ok || host == "" {
				continue
			}
			notify("clipfan", "Installing on "+host+"…")
			go func(h string) {
				if err := installRemote(h); err != nil {
					notify("clipfan: install failed", err.Error())
					return
				}
				if err := addPeerToConfig(h); err != nil {
					slog.Warn("addPeerToConfig", "err", err)
				}
				notify("clipfan", "Installed on "+h+". Restarting local daemon.")
				_ = restartDaemon()
			}(host)
		case <-mOpenCfg.ClickedCh:
			_ = exec.Command("open", config.Path()).Start()
		case <-mRestart.ClickedCh:
			if err := restartDaemon(); err != nil {
				notify("clipfan", "restart failed: "+err.Error())
			} else {
				notify("clipfan", "daemon restarted")
			}
		case <-mOpenLog.ClickedCh:
			_ = exec.Command("open", "/tmp/clipfan-shell.log").Start()
		case <-mQuit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

func refreshLoop(mOrigin *systray.MenuItem, peers []*systray.MenuItem) {
	for {
		snap, err := fetchPeers()
		if err != nil {
			mOrigin.SetTitle("(daemon not running)")
			for _, p := range peers {
				p.Hide()
			}
		} else {
			mOrigin.SetTitle(fmt.Sprintf("origin: %s", snap.Origin))
			for i, item := range peers {
				if i < len(snap.Peers) {
					item.SetTitle(renderPeer(snap.Peers[i]))
					item.Show()
				} else {
					item.Hide()
				}
			}
		}
		time.Sleep(pollInterval)
	}
}

func renderPeer(p daemon.PeerState) string {
	indicator := "○"
	if p.LastPushOK {
		indicator = "●"
	} else if !p.LastPushTS.IsZero() {
		indicator = "✗"
	}
	suffix := ""
	if !p.LastRecvTS.IsZero() && time.Since(p.LastRecvTS) < 5*time.Minute {
		suffix = "  (rx)"
	}
	return fmt.Sprintf("%s  %s%s", indicator, p.Hostname, suffix)
}

func fetchPeers() (*peersResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", healthURL+"/v1/peers", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var pr peersResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

func askHostname() (string, bool) {
	script := `tell application "System Events"
    activate
    text returned of (display dialog "Install clipfan on which host?  (SSH-reachable name)" default answer "" with title "clipfan" buttons {"Cancel", "Install"} default button "Install")
end tell`
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

func notify(title, body string) {
	script := fmt.Sprintf(`display notification %q with title %q`, body, title)
	_ = exec.Command("osascript", "-e", script).Run()
}

// shareDir returns the directory holding the install payload (binaries +
// install.sh + unit files). Defaults to ~/.local/share/clipfan, with
// XDG_DATA_HOME / CLIPFAN_SHARE overrides.
func shareDir() string {
	if d := os.Getenv("CLIPFAN_SHARE"); d != "" {
		return d
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "clipfan")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".local", "share", "clipfan")
}

// installRemote runs the install playbook against `host`. The host name must
// be SSH-reachable (resolved via ssh_config / DNS / mDNS / tailnet).
func installRemote(host string) error {
	share := shareDir()
	if _, err := os.Stat(share); err != nil {
		return fmt.Errorf("share dir %s missing — run dist/install-share.sh first: %w", share, err)
	}

	// 1. Detect the remote arch/os.
	out, err := sshOutput(host, "uname -s; uname -m")
	if err != nil {
		return fmt.Errorf("ssh probe: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return fmt.Errorf("unexpected uname output: %q", string(out))
	}
	goos, err := mapOS(lines[0])
	if err != nil {
		return err
	}
	goarch, err := mapArch(lines[1])
	if err != nil {
		return err
	}

	binName := fmt.Sprintf("clipfan-%s-%s", goos, goarch)
	binPath := filepath.Join(share, binName)
	if _, err := os.Stat(binPath); err != nil {
		return fmt.Errorf("missing binary %s: %w", binPath, err)
	}

	// 2. Build per-host config inheriting our shared key + listing the
	//    Mac as a peer.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load local config: %w", err)
	}
	selfName := selfShortName()
	remoteCfg := config.Config{
		Listen:      ":7853",
		SharedKey:   cfg.SharedKey,
		Discovery:   "static",
		StaticPeers: []string{selfName},
		Port:        7853,
	}
	cfgJSON, _ := json.MarshalIndent(remoteCfg, "", "  ")

	tmp, err := os.MkdirTemp("", "clipfan-install-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	if err := copyFile(binPath, filepath.Join(tmp, binName)); err != nil {
		return err
	}
	if goos == "linux" {
		shim := fmt.Sprintf("clipfan-shim-%s-%s", goos, goarch)
		shimSrc := filepath.Join(share, shim)
		if _, err := os.Stat(shimSrc); err == nil {
			if err := copyFile(shimSrc, filepath.Join(tmp, shim)); err != nil {
				return err
			}
		}
	}
	for _, name := range []string{"install.sh", "clipfan.service", "com.primeradiant.clipfan.plist"} {
		src := filepath.Join(share, name)
		if _, err := os.Stat(src); err == nil {
			if err := copyFile(src, filepath.Join(tmp, name)); err != nil {
				return err
			}
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, "config.json"), cfgJSON, 0o600); err != nil {
		return err
	}

	// 3. scp & run install.sh
	if err := run("ssh", host, "mkdir -p /tmp/clipfan-install"); err != nil {
		return fmt.Errorf("ssh mkdir: %w", err)
	}
	scpArgs := []string{"-q"}
	files, _ := filepath.Glob(filepath.Join(tmp, "*"))
	scpArgs = append(scpArgs, files...)
	scpArgs = append(scpArgs, host+":/tmp/clipfan-install/")
	if err := run("scp", scpArgs...); err != nil {
		return fmt.Errorf("scp: %w", err)
	}

	// 4. Run install.sh — config.json is moved into ~/.config/clipfan/
	//    inside the script's environment before install.sh runs.
	cmd := fmt.Sprintf("set -e; mkdir -p ~/.config/clipfan; install -m 0600 /tmp/clipfan-install/config.json ~/.config/clipfan/config.json; cd /tmp/clipfan-install && bash install.sh")
	if out, err := exec.Command("ssh", host, cmd).CombinedOutput(); err != nil {
		return fmt.Errorf("install.sh: %w (output: %s)", err, string(out))
	}
	return nil
}

func mapOS(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "linux":
		return "linux", nil
	case "darwin":
		return "darwin", nil
	}
	return "", fmt.Errorf("unsupported os: %q", s)
}

func mapArch(s string) (string, error) {
	switch strings.TrimSpace(s) {
	case "x86_64", "amd64":
		return "amd64", nil
	case "arm64", "aarch64":
		return "arm64", nil
	}
	return "", fmt.Errorf("unsupported arch: %q", s)
}

func selfShortName() string {
	h, _ := os.Hostname()
	return strings.TrimSuffix(strings.SplitN(h, ".", 2)[0], ".local")
}

func addPeerToConfig(host string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	for _, p := range cfg.StaticPeers {
		if p == host {
			return nil
		}
	}
	cfg.StaticPeers = append(cfg.StaticPeers, host)
	return config.Save(cfg)
}

func restartDaemon() error {
	// Best-effort: try launchd kickstart, then fall back to pkill + nohup.
	if runtime.GOOS == "darwin" {
		uid := fmt.Sprint(os.Getuid())
		if err := exec.Command("launchctl", "kickstart", "-k", "gui/"+uid+"/com.primeradiant.clipfan").Run(); err == nil {
			return nil
		}
	}
	_ = exec.Command("pkill", "-f", "^/Users/jesse/.local/bin/clipfan$").Run()
	time.Sleep(500 * time.Millisecond)
	cmd := exec.Command("nohup", os.Getenv("HOME")+"/.local/bin/clipfan")
	logFile, _ := os.OpenFile("/tmp/clipfan-shell.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	cmd.SysProcAttr = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w (output: %s)", name, strings.Join(args, " "), err, string(out))
	}
	return nil
}

func sshOutput(host, cmd string) ([]byte, error) {
	return exec.Command("ssh", "-o", "ConnectTimeout=5", host, cmd).Output()
}
