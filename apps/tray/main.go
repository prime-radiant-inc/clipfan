//go:build windows

// Package main is the clipfan system-tray app for Windows. It complements the
// macOS Swift menubar app (apps/mac): a lightweight tray icon + menu that
// reflects the local daemon's status and gives quick access to its config and
// restart. The native macOS app remains the first-class Mac UI.
//
// On Windows the daemon runs as a per-user Scheduled Task (see dist/install.ps1)
// in the interactive session, so the tray app talks to it over the loopback
// listener (127.0.0.1:7853) for a liveness check.
package main

import (
	_ "embed"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"fyne.io/systray"
)

//go:embed icon.png
var iconBytes []byte

const daemonAddr = "127.0.0.1:7853"

func main() {
	systray.SetIcon(iconBytes)
	systray.SetTitle("clipfan")
	systray.SetTooltip("clipfan clipboard sync")
	systray.Run(onReady, nil)
}

func onReady() {
	mStatus := systray.AddMenuItem("daemon: …", "clipfan daemon status")
	systray.AddSeparator()
	mAddPeer := systray.AddMenuItem("Add Peer...", "provision a new clipfan peer")
	systray.AddSeparator()
	mConfig := systray.AddMenuItem("Open config folder", "")
	mRestart := systray.AddMenuItem("Restart daemon", "restart the clipfan daemon")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "quit the tray app")

	go pollStatus(mStatus)
	go handleMenu(mAddPeer, mConfig, mRestart, mQuit)
}

func handleMenu(mAddPeer, mConfig, mRestart, mQuit *systray.MenuItem) {
	for {
		select {
		case <-mAddPeer.ClickedCh:
			openAddPeerTerminal()
		case <-mConfig.ClickedCh:
			openPath(configDir())
		case <-mRestart.ClickedCh:
			restartDaemon()
		case <-mQuit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

// pollStatus refreshes the status menu item by probing the daemon's loopback
// listener (a plain TCP connect — no signed API needed for a liveness check).
func pollStatus(m *systray.MenuItem) {
	for {
		if alive(daemonAddr) {
			m.SetTitle("daemon: running")
		} else {
			m.SetTitle("daemon: not running")
		}
		time.Sleep(5 * time.Second)
	}
}

func alive(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func configDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "clipfan")
}

func openPath(p string) {
	_ = exec.Command("explorer", p).Start()
}

func restartDaemon() {
	// The daemon runs as a per-user Scheduled Task (dist/install.ps1).
	_ = exec.Command("powershell", "-NoProfile", "-Command",
		"Get-ScheduledTask clipfan | Stop-ScheduledTask; Get-ScheduledTask clipfan | Start-ScheduledTask").Start()
}

func openAddPeerTerminal() {
	home, _ := os.UserHomeDir()
	clipfanExe := filepath.Join(home, ".local", "bin", "clipfan.exe")
	_ = exec.Command("powershell", "-NoProfile", "-NoExit", "-Command",
		fmt.Sprintf("& '%s' add-peer", clipfanExe)).Start()
}
