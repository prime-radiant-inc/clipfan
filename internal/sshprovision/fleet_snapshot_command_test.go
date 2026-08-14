package sshprovision

import (
	"strings"
	"testing"
)

func TestPinnedSSHFleetSnapshotCommand(t *testing.T) {
	t.Parallel()

	cmd, err := PinnedSSHFleetSnapshotCommand(PinnedSSHCommand{
		User:           "jesse",
		Host:           "Example.COM.",
		Port:           2200,
		PrivateKeyPath: "/home/jesse/.config/clipfan/ssh/sync_ed25519",
		KnownHostsPath: "/home/jesse/.config/clipfan/ssh/known_hosts",
	})
	if err != nil {
		t.Fatalf("PinnedSSHFleetSnapshotCommand() error = %v", err)
	}

	want := []string{
		"ssh",
		"-F", "/dev/null",
		"-o", "BatchMode=yes",
		"-o", "IdentitiesOnly=yes",
		"-o", "IdentityFile=none",
		"-o", "IdentityAgent=none",
		"-o", "ConnectTimeout=5",
		"-o", "ConnectionAttempts=1",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=/home/jesse/.config/clipfan/ssh/known_hosts",
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "ProxyCommand=none",
		"-o", "ProxyJump=none",
		"-o", "PermitLocalCommand=no",
		"-o", "RequestTTY=no",
		"-o", "ClearAllForwardings=yes",
		"-o", "LogLevel=ERROR",
		"-i", "/home/jesse/.config/clipfan/ssh/sync_ed25519",
		"-p", "2200",
		"jesse@example.com",
		SSHGatewayFleetSnapshotCommand,
	}
	assertSSHCommandArgs(t, cmd.Args, want)
	if got := cmd.Args[len(cmd.Args)-1]; got != SSHGatewayFleetSnapshotCommand {
		t.Fatalf("last arg = %q, want %q", got, SSHGatewayFleetSnapshotCommand)
	}
}

func TestPinnedSSHDirectGatewayFleetSnapshotCommand(t *testing.T) {
	t.Parallel()

	cmd, err := PinnedSSHFleetSnapshotCommand(PinnedSSHCommand{
		User:            "jesse",
		Host:            "magic-kingdom",
		Port:            22,
		KnownHostsPath:  "/home/jesse/.config/clipfan/ssh/known_hosts",
		GatewayPath:     "/home/jesse/.local/bin/clipfan",
		AuthorizedPeer:  "m4",
		AuthorizedKeyID: "key-123456",
		DirectGateway:   true,
	})
	if err != nil {
		t.Fatalf("PinnedSSHFleetSnapshotCommand() error = %v", err)
	}
	got := cmd.Args[len(cmd.Args)-1]
	if !strings.Contains(got, "\"ssh-gateway\"") ||
		!strings.Contains(got, "\"--direct-command\" \"fleet-snapshot\"") ||
		strings.TrimSpace(got) == SSHGatewayFleetSnapshotCommand {
		t.Fatalf("direct command = %q", got)
	}
}
