package sshprovision

import (
	"errors"
	"testing"
)

func TestPinnedSSHProbeCommand(t *testing.T) {
	t.Parallel()

	cmd, err := PinnedSSHProbeCommand(PinnedSSHCommand{
		User:           "jesse",
		Host:           "Example.COM.",
		Port:           2200,
		PrivateKeyPath: "/home/jesse/.config/clipfan/ssh/sync_ed25519",
		KnownHostsPath: "/home/jesse/.config/clipfan/ssh/known_hosts",
	})
	if err != nil {
		t.Fatalf("PinnedSSHProbeCommand() error = %v", err)
	}

	want := []string{
		"ssh",
		"-F", "/dev/null",
		"-o", "BatchMode=yes",
		"-o", "IdentitiesOnly=yes",
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
		SSHGatewayProbeCommand,
	}
	if len(cmd.Args) != len(want) {
		t.Fatalf("args length = %d, want %d: %#v", len(cmd.Args), len(want), cmd.Args)
	}
	for i := range want {
		if cmd.Args[i] != want[i] {
			t.Fatalf("arg[%d] = %q, want %q; args=%#v", i, cmd.Args[i], want[i], cmd.Args)
		}
	}
	if got := cmd.Args[len(cmd.Args)-1]; got != SSHGatewayProbeCommand {
		t.Fatalf("last arg = %q, want %q", got, SSHGatewayProbeCommand)
	}
}

func TestPinnedSSHProbeCommandRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	valid := PinnedSSHCommand{
		User:           "jesse",
		Host:           "example.com",
		Port:           22,
		PrivateKeyPath: "/home/jesse/.config/clipfan/ssh/sync_ed25519",
		KnownHostsPath: "/home/jesse/.config/clipfan/ssh/known_hosts",
	}

	for _, tc := range []struct {
		name   string
		mutate func(*PinnedSSHCommand)
	}{
		{name: "invalid user", mutate: func(cmd *PinnedSSHCommand) { cmd.User = "-bad" }},
		{name: "invalid host", mutate: func(cmd *PinnedSSHCommand) { cmd.Host = "example.com:22" }},
		{name: "host shell separator", mutate: func(cmd *PinnedSSHCommand) { cmd.Host = "example.com;sh" }},
		{name: "host whitespace", mutate: func(cmd *PinnedSSHCommand) { cmd.Host = "example .com" }},
		{name: "user newline", mutate: func(cmd *PinnedSSHCommand) { cmd.User = "jesse\nroot" }},
		{name: "invalid port", mutate: func(cmd *PinnedSSHCommand) { cmd.Port = 0 }},
		{name: "relative key", mutate: func(cmd *PinnedSSHCommand) { cmd.PrivateKeyPath = "sync_ed25519" }},
		{name: "key path shell separator", mutate: func(cmd *PinnedSSHCommand) { cmd.PrivateKeyPath = "/home/jesse/.config/clipfan/ssh/sync;sh" }},
		{name: "key path dollar", mutate: func(cmd *PinnedSSHCommand) { cmd.PrivateKeyPath = "/home/jesse/.config/clipfan/ssh/$key" }},
		{name: "known hosts quote", mutate: func(cmd *PinnedSSHCommand) { cmd.KnownHostsPath = "/home/jesse/.config/clipfan/ssh/known\"hosts" }},
		{name: "known hosts tab", mutate: func(cmd *PinnedSSHCommand) { cmd.KnownHostsPath = "/home/jesse/.config/clipfan/ssh/known\thosts" }},
		{name: "unsafe known hosts", mutate: func(cmd *PinnedSSHCommand) { cmd.KnownHostsPath = "/home/jesse/.config/clipfan/ssh/../known_hosts" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cmd := valid
			tc.mutate(&cmd)
			_, err := PinnedSSHProbeCommand(cmd)
			if !errors.Is(err, ErrInvalidPinnedSSHCommand) {
				t.Fatalf("PinnedSSHProbeCommand() error = %v, want ErrInvalidPinnedSSHCommand", err)
			}
		})
	}
}

func TestPinnedSSHProbeCommandDoesNotUseShell(t *testing.T) {
	t.Parallel()

	cmd, err := PinnedSSHProbeCommand(PinnedSSHCommand{
		User:           "jesse",
		Host:           "example.com",
		Port:           22,
		PrivateKeyPath: "/home/jesse/.config/clipfan/ssh/sync_ed25519",
		KnownHostsPath: "/home/jesse/.config/clipfan/ssh/known_hosts",
	})
	if err != nil {
		t.Fatalf("PinnedSSHProbeCommand() error = %v", err)
	}
	for _, arg := range cmd.Args {
		if arg == "sh" || arg == "-c" {
			t.Fatalf("shell arg present in %#v", cmd.Args)
		}
	}
}
