package sshprovision

import (
	"errors"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/config"
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

func TestSSHKeyscanCommand(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		spec SSHKeyscanSpec
		want []string
	}{
		{name: "dns default port", spec: SSHKeyscanSpec{Host: "Example.COM.", Port: 22}, want: []string{"ssh-keyscan", "-T", "5", "-p", "22", "example.com"}},
		{name: "dns non default port", spec: SSHKeyscanSpec{Host: "Example.COM.", Port: 2200}, want: []string{"ssh-keyscan", "-T", "5", "-p", "2200", "example.com"}},
		{name: "ipv4", spec: SSHKeyscanSpec{Host: "192.0.2.10", Port: 22, TimeoutSeconds: 9}, want: []string{"ssh-keyscan", "-T", "9", "-p", "22", "192.0.2.10"}},
		{name: "ipv6", spec: SSHKeyscanSpec{Host: "2001:DB8::1", Port: 2200}, want: []string{"ssh-keyscan", "-T", "5", "-p", "2200", "2001:db8::1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cmd, err := SSHKeyscanCommand(tc.spec)
			if err != nil {
				t.Fatalf("SSHKeyscanCommand() error = %v", err)
			}
			assertSSHCommandArgs(t, cmd.Args, tc.want)
			for _, arg := range cmd.Args {
				if arg == "-t" || arg == "-F" || arg == "-o" || arg == "sh" || arg == "-c" {
					t.Fatalf("unexpected shell/config/key-type arg present in %#v", cmd.Args)
				}
			}
		})
	}
}

func TestSSHKeyscanCommandRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		spec SSHKeyscanSpec
	}{
		{name: "invalid host", spec: SSHKeyscanSpec{Host: "example.com;sh", Port: 22}},
		{name: "leading dash host", spec: SSHKeyscanSpec{Host: "-example.com", Port: 22}},
		{name: "host with user", spec: SSHKeyscanSpec{Host: "me@example.com", Port: 22}},
		{name: "host with port suffix", spec: SSHKeyscanSpec{Host: "example.com:22", Port: 22}},
		{name: "host whitespace", spec: SSHKeyscanSpec{Host: "example .com", Port: 22}},
		{name: "invalid port", spec: SSHKeyscanSpec{Host: "example.com", Port: 0}},
		{name: "negative timeout", spec: SSHKeyscanSpec{Host: "example.com", Port: 22, TimeoutSeconds: -1}},
		{name: "too large timeout", spec: SSHKeyscanSpec{Host: "example.com", Port: 22, TimeoutSeconds: 61}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := SSHKeyscanCommand(tc.spec)
			if !errors.Is(err, ErrInvalidRegularSSHCommand) {
				t.Fatalf("SSHKeyscanCommand() error = %v, want ErrInvalidRegularSSHCommand", err)
			}
		})
	}
}

func TestRegularSSHInstallAuthorizedKeyCommand(t *testing.T) {
	t.Parallel()

	cmd, err := RegularSSHInstallAuthorizedKeyCommand(RegularSSHInstallAuthorizedKeySpec{
		User:           "jesse",
		Host:           "Example.COM.",
		Port:           2200,
		KnownHostsPath: "/home/jesse/.config/clipfan/ssh/known_hosts",
		InstallPath:    "/home/jesse/.local/bin/clipfan",
		GatewayPath:    "/home/jesse/.local/bin/clipfan",
		PeerID:         "linux-b",
		KeyID:          "key-123456",
		PublicKey:      testEd25519Key,
	})
	if err != nil {
		t.Fatalf("RegularSSHInstallAuthorizedKeyCommand() error = %v", err)
	}

	want := []string{
		"ssh",
		"-F", "/dev/null",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=/home/jesse/.config/clipfan/ssh/known_hosts",
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "ProxyCommand=none",
		"-o", "ProxyJump=none",
		"-o", "PermitLocalCommand=no",
		"-o", "RequestTTY=no",
		"-o", "ClearAllForwardings=yes",
		"-o", "LogLevel=ERROR",
		"-p", "2200",
		"jesse@example.com",
		"'/home/jesse/.local/bin/clipfan' 'ssh-install-authorized-key' '--peer' 'linux-b' '--key-id' 'key-123456' '--gateway-path' '/home/jesse/.local/bin/clipfan' '--public-key' '" + testEd25519Key + "'",
	}
	assertSSHCommandArgs(t, cmd.Args, want)
	for _, arg := range cmd.Args {
		if arg == "-i" || arg == "IdentitiesOnly=yes" || arg == "-c" || arg == "sh" {
			t.Fatalf("regular SSH install command should not force sync identity or shell locally: %#v", cmd.Args)
		}
	}
}

func TestRegularSSHInstallAuthorizedKeyCommandRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	valid := RegularSSHInstallAuthorizedKeySpec{
		User:           "jesse",
		Host:           "example.com",
		Port:           22,
		KnownHostsPath: "/home/jesse/.config/clipfan/ssh/known_hosts",
		InstallPath:    "/home/jesse/.local/bin/clipfan",
		GatewayPath:    "/home/jesse/.local/bin/clipfan",
		PeerID:         "linux-b",
		KeyID:          "key-123456",
		PublicKey:      testEd25519Key,
	}
	for _, tc := range []struct {
		name   string
		mutate func(*RegularSSHInstallAuthorizedKeySpec)
	}{
		{name: "invalid user", mutate: func(spec *RegularSSHInstallAuthorizedKeySpec) { spec.User = "-bad" }},
		{name: "invalid host", mutate: func(spec *RegularSSHInstallAuthorizedKeySpec) { spec.Host = "example.com;sh" }},
		{name: "invalid port", mutate: func(spec *RegularSSHInstallAuthorizedKeySpec) { spec.Port = 0 }},
		{name: "unsafe known_hosts", mutate: func(spec *RegularSSHInstallAuthorizedKeySpec) {
			spec.KnownHostsPath = "/home/jesse/.config/clipfan/ssh/../known_hosts"
		}},
		{name: "unsafe install path", mutate: func(spec *RegularSSHInstallAuthorizedKeySpec) { spec.InstallPath = "/home/jesse/.local/bin/clip fan" }},
		{name: "unsafe gateway path", mutate: func(spec *RegularSSHInstallAuthorizedKeySpec) { spec.GatewayPath = "/home/jesse/.local/bin/clip fan" }},
		{name: "invalid peer", mutate: func(spec *RegularSSHInstallAuthorizedKeySpec) { spec.PeerID = "bad peer" }},
		{name: "invalid key id", mutate: func(spec *RegularSSHInstallAuthorizedKeySpec) { spec.KeyID = "short" }},
		{name: "invalid public key", mutate: func(spec *RegularSSHInstallAuthorizedKeySpec) { spec.PublicKey = "not-base64" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			spec := valid
			tc.mutate(&spec)
			_, err := RegularSSHInstallAuthorizedKeyCommand(spec)
			if !errors.Is(err, ErrInvalidRegularSSHCommand) {
				t.Fatalf("RegularSSHInstallAuthorizedKeyCommand() error = %v, want ErrInvalidRegularSSHCommand", err)
			}
		})
	}
}

func TestSyncKeyMaterialFromConfigConvertsOpenSSHPublicKeyLine(t *testing.T) {
	t.Parallel()

	material, err := SyncKeyMaterialFromConfig(config.SyncKeyCreateResult{
		PrivateKeyPath: "/home/jesse/.config/clipfan/ssh/sync_ed25519",
		PublicKey:      "ssh-ed25519 " + testEd25519Key + " clipfan:linux-b",
		KeyID:          syncKeyIDFromPublicBlob(mustDecodePublicBlobForTest(t, testEd25519Key)),
	})
	if err != nil {
		t.Fatalf("SyncKeyMaterialFromConfig() error = %v", err)
	}
	if material.PrivateKeyPath != "/home/jesse/.config/clipfan/ssh/sync_ed25519" || material.PublicKey != testEd25519Key || material.KeyID != syncKeyIDFromPublicBlob(mustDecodePublicBlobForTest(t, testEd25519Key)) {
		t.Fatalf("material = %#v", material)
	}
}

func TestSyncKeyMaterialFromConfigRejectsInvalidMaterial(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		result config.SyncKeyCreateResult
	}{
		{name: "missing key type", result: config.SyncKeyCreateResult{PrivateKeyPath: "/home/jesse/.config/clipfan/ssh/sync_ed25519", PublicKey: testEd25519Key, KeyID: "key-123456"}},
		{name: "wrong key type", result: config.SyncKeyCreateResult{PrivateKeyPath: "/home/jesse/.config/clipfan/ssh/sync_ed25519", PublicKey: "ssh-rsa " + testRSAKey, KeyID: "key-123456"}},
		{name: "malformed base64", result: config.SyncKeyCreateResult{PrivateKeyPath: "/home/jesse/.config/clipfan/ssh/sync_ed25519", PublicKey: "ssh-ed25519 not-base64", KeyID: "key-123456"}},
		{name: "invalid key id", result: config.SyncKeyCreateResult{PrivateKeyPath: "/home/jesse/.config/clipfan/ssh/sync_ed25519", PublicKey: "ssh-ed25519 " + testEd25519Key, KeyID: "short"}},
		{name: "mismatched key id", result: config.SyncKeyCreateResult{PrivateKeyPath: "/home/jesse/.config/clipfan/ssh/sync_ed25519", PublicKey: "ssh-ed25519 " + testEd25519Key, KeyID: "key-123456"}},
		{name: "invalid private path", result: config.SyncKeyCreateResult{PrivateKeyPath: "sync_ed25519", PublicKey: "ssh-ed25519 " + testEd25519Key, KeyID: "key-123456"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := SyncKeyMaterialFromConfig(tc.result)
			if err == nil {
				t.Fatal("SyncKeyMaterialFromConfig() error = nil, want error")
			}
		})
	}
}

func TestShellQuoteArg(t *testing.T) {
	t.Parallel()

	got := shellQuoteCommand([]string{"clipfan", "arg with space", "don't"})
	want := "'clipfan' 'arg with space' 'don'\\''t'"
	if got != want {
		t.Fatalf("shellQuoteCommand() = %q, want %q", got, want)
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("shellQuoteCommand() contains newline: %q", got)
	}
}

func mustDecodePublicBlobForTest(t *testing.T, publicKey string) []byte {
	t.Helper()
	blob, err := decodeKnownHostPublicKeyBlob(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func assertSSHCommandArgs(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("args length = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("arg[%d] = %q, want %q; args=%#v", i, got[i], want[i], got)
		}
	}
}
