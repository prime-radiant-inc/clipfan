package sshprovision

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

func TestRegularSSHProvisionDriverRunsRemoteHelperSequence(t *testing.T) {
	t.Parallel()

	runner := &fakeRegularSSHRunner{}
	var configMutation DirectPairConfigMutation
	driver := RegularSSHProvisionDriver{
		Runner:                runner,
		RegularKnownHostsPath: "/Users/jesse/.config/clipfan/ssh/regular_known_hosts",
		ConfirmedHostKeyLines: map[string]string{
			"mac-a":   "mac-a.tailnet ssh-ed25519 " + testEd25519Key,
			"linux-b": "linux-b.tailnet ssh-ed25519 " + testEd25519Key,
		},
		WriteConfigFunc: func(_ context.Context, mutation DirectPairConfigMutation) error {
			configMutation = mutation
			return nil
		},
	}
	provisioner := DirectPairProvisioner{Driver: driver, configV2WriteGate: func() bool { return true }}

	result, err := provisioner.Provision(context.Background(), validDirectPairProvisionInput())
	if err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	if result.SyncKeys["linux-b"].KeyID != testEd25519KeyID {
		t.Fatalf("linux-b SyncKey = %#v", result.SyncKeys["linux-b"])
	}
	if result.SyncKeys["mac-a"].KeyID != testMacEd25519KeyID {
		t.Fatalf("mac-a SyncKey = %#v", result.SyncKeys["mac-a"])
	}
	if configMutation.KnownHostsPaths["linux-b"] != "/home/jesse/.config/clipfan/ssh/known_hosts" {
		t.Fatalf("config mutation = %#v", configMutation)
	}
	if configMutation.KnownHostsPaths["mac-a"] != "/Users/jesse/.config/clipfan/ssh/known_hosts" {
		t.Fatalf("config mutation = %#v", configMutation)
	}
	if configMutation.SharedKey != testDirectPairSharedKey {
		t.Fatalf("config mutation shared key = %q, want fleet key", configMutation.SharedKey)
	}

	if len(runner.commands) != 8 {
		t.Fatalf("runner command count = %d, want 8: %#v", len(runner.commands), runner.commands)
	}
	assertRemoteCommand(t, runner.commands[0], "jesse@linux-b.tailnet", "ssh-install-known-host", "--host", "mac-a.tailnet")
	assertRemoteCommand(t, runner.commands[1], "jesse@mac-a.tailnet", "ssh-install-known-host", "--host", "linux-b.tailnet")
	assertRemoteCommand(t, runner.commands[2], "jesse@linux-b.tailnet", "ssh-ensure-sync-key", "--key-path", "/home/jesse/.config/clipfan/ssh/sync_ed25519")
	assertRemoteCommand(t, runner.commands[3], "jesse@mac-a.tailnet", "ssh-ensure-sync-key", "--key-path", "/Users/jesse/.config/clipfan/ssh/sync_ed25519")
	assertRemoteCommand(t, runner.commands[4], "jesse@mac-a.tailnet", "ssh-install-authorized-key", "--peer", "linux-b")
	assertRemoteCommand(t, runner.commands[5], "jesse@linux-b.tailnet", "ssh-install-authorized-key", "--peer", "mac-a")
	assertRemoteCommand(t, runner.commands[6], "jesse@linux-b.tailnet", "ssh-run-probe", "--host", "mac-a.tailnet")
	assertRemoteCommand(t, runner.commands[6], "jesse@linux-b.tailnet", "ssh-run-probe", "--expect-peer", "linux-b")
	assertRemoteCommand(t, runner.commands[6], "jesse@linux-b.tailnet", "ssh-run-probe", "--expect-key-id", testEd25519KeyID)
	assertRemoteCommand(t, runner.commands[7], "jesse@mac-a.tailnet", "ssh-run-probe", "--host", "linux-b.tailnet")
	assertRemoteCommand(t, runner.commands[7], "jesse@mac-a.tailnet", "ssh-run-probe", "--expect-peer", "mac-a")
	assertRemoteCommand(t, runner.commands[7], "jesse@mac-a.tailnet", "ssh-run-probe", "--expect-key-id", testMacEd25519KeyID)
}

func TestRegularSSHProvisionDriverRequiresConfirmedHostKey(t *testing.T) {
	t.Parallel()

	runner := &fakeRegularSSHRunner{}
	driver := RegularSSHProvisionDriver{
		Runner:                runner,
		RegularKnownHostsPath: "/Users/jesse/.config/clipfan/ssh/regular_known_hosts",
		WriteConfigFunc:       func(context.Context, DirectPairConfigMutation) error { return nil },
	}
	provisioner := DirectPairProvisioner{Driver: driver, configV2WriteGate: func() bool { return true }}

	_, err := provisioner.Provision(context.Background(), validDirectPairProvisionInput())
	if !errors.Is(err, ErrConfirmedHostKeyMissing) {
		t.Fatalf("Provision() error = %v, want ErrConfirmedHostKeyMissing", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("runner commands = %#v, want none", runner.commands)
	}
}

func TestRegularSSHProvisionDriverRejectsMalformedRemoteOutput(t *testing.T) {
	t.Parallel()

	runner := &fakeRegularSSHRunner{override: map[string]CommandOutput{
		"ssh-ensure-sync-key": {Stdout: []byte(`{"status":"ok","host_id":"linux-b","key_id":"` + testEd25519KeyID + `","public_key":"` + testEd25519Key + `","private_key_path":"/wrong/key"}`)},
	}}
	driver := RegularSSHProvisionDriver{
		Runner:                runner,
		RegularKnownHostsPath: "/Users/jesse/.config/clipfan/ssh/regular_known_hosts",
		ConfirmedHostKeyLines: map[string]string{
			"mac-a":   "mac-a.tailnet ssh-ed25519 " + testEd25519Key,
			"linux-b": "linux-b.tailnet ssh-ed25519 " + testEd25519Key,
		},
		WriteConfigFunc: func(context.Context, DirectPairConfigMutation) error { return nil },
	}
	provisioner := DirectPairProvisioner{Driver: driver, configV2WriteGate: func() bool { return true }}

	_, err := provisioner.Provision(context.Background(), validDirectPairProvisionInput())
	if !errors.Is(err, ErrRemoteProvisionOutput) {
		t.Fatalf("Provision() error = %v, want ErrRemoteProvisionOutput", err)
	}
	if len(runner.commands) != 3 {
		t.Fatalf("runner command count = %d, want 3", len(runner.commands))
	}
}

func TestRegularSSHProvisionDriverRejectsSyncKeyIDMismatch(t *testing.T) {
	t.Parallel()

	runner := &fakeRegularSSHRunner{override: map[string]CommandOutput{
		"ssh-ensure-sync-key": {Stdout: []byte(`{"status":"ok","host_id":"linux-b","key_id":"key-123456","public_key":"` + testEd25519Key + `","private_key_path":"/home/jesse/.config/clipfan/ssh/sync_ed25519"}`)},
	}}
	driver := RegularSSHProvisionDriver{
		Runner:                runner,
		RegularKnownHostsPath: "/Users/jesse/.config/clipfan/ssh/regular_known_hosts",
		ConfirmedHostKeyLines: map[string]string{
			"mac-a":   "mac-a.tailnet ssh-ed25519 " + testEd25519Key,
			"linux-b": "linux-b.tailnet ssh-ed25519 " + testEd25519Key,
		},
		WriteConfigFunc: func(context.Context, DirectPairConfigMutation) error { return nil },
	}
	provisioner := DirectPairProvisioner{Driver: driver, configV2WriteGate: func() bool { return true }}

	_, err := provisioner.Provision(context.Background(), validDirectPairProvisionInput())
	if !errors.Is(err, ErrInvalidAuthorizedKey) {
		t.Fatalf("Provision() error = %v, want ErrInvalidAuthorizedKey", err)
	}
	if len(runner.commands) != 3 {
		t.Fatalf("runner command count = %d, want 3", len(runner.commands))
	}
}

type fakeRegularSSHRunner struct {
	commands []SSHCommand
	override map[string]CommandOutput
}

func (r *fakeRegularSSHRunner) Run(_ context.Context, command SSHCommand) (CommandOutput, error) {
	r.commands = append(r.commands, command)
	remote := command.Args[len(command.Args)-1]
	if r.override != nil {
		for key, output := range r.override {
			if strings.Contains(remote, key) {
				return output, nil
			}
		}
	}
	switch {
	case strings.Contains(remote, "ssh-install-known-host"):
		return CommandOutput{Stdout: []byte(`{"status":"ok","pattern":"mac-a.tailnet","key_type":"ssh-ed25519"}`)}, nil
	case strings.Contains(remote, "ssh-ensure-sync-key"):
		if strings.Contains(remote, "\"--host-id\" \"mac-a\"") {
			return CommandOutput{Stdout: []byte(`{"status":"ok","changed":true,"host_id":"mac-a","key_id":"` + testMacEd25519KeyID + `","public_key":"` + testOtherEd25519Key + `","private_key_path":"/Users/jesse/.config/clipfan/ssh/sync_ed25519"}`)}, nil
		}
		return CommandOutput{Stdout: []byte(`{"status":"ok","changed":true,"host_id":"linux-b","key_id":"` + testEd25519KeyID + `","public_key":"` + testEd25519Key + `","private_key_path":"/home/jesse/.config/clipfan/ssh/sync_ed25519"}`)}, nil
	case strings.Contains(remote, "ssh-install-authorized-key"):
		if strings.Contains(remote, "\"--peer\" \"mac-a\"") {
			return CommandOutput{Stdout: []byte(`{"status":"ok","changed":true,"peer_id":"mac-a","key_id":"` + testMacEd25519KeyID + `"}`)}, nil
		}
		return CommandOutput{Stdout: []byte(`{"status":"ok","changed":true,"peer_id":"linux-b","key_id":"` + testEd25519KeyID + `"}`)}, nil
	case strings.Contains(remote, "ssh-run-probe"):
		if strings.Contains(remote, "\"--expect-peer\" \"mac-a\"") {
			return CommandOutput{Stdout: []byte(`{"status":"ok","peer_id":"mac-a","key_id":"` + testMacEd25519KeyID + `"}`)}, nil
		}
		return CommandOutput{Stdout: []byte(`{"status":"ok","peer_id":"linux-b","key_id":"` + testEd25519KeyID + `"}`)}, nil
	default:
		return CommandOutput{Stdout: []byte(`{"status":"ok"}`)}, nil
	}
}

const testEd25519KeyID = "626e58c17d770373"
const testMacEd25519KeyID = "1892e27b582e5293"

func assertRemoteCommand(t *testing.T, command SSHCommand, target string, subcommand string, flag string, value string) {
	t.Helper()
	if len(command.Args) < 2 {
		t.Fatalf("command args too short: %#v", command.Args)
	}
	if command.Args[len(command.Args)-2] != target {
		t.Fatalf("target = %q, want %q; args=%#v", command.Args[len(command.Args)-2], target, command.Args)
	}
	remote := command.Args[len(command.Args)-1]
	for _, want := range []string{"\"" + subcommand + "\"", "\"" + flag + "\"", "\"" + value + "\""} {
		if !strings.Contains(remote, want) {
			t.Fatalf("remote command = %q, want %q", remote, want)
		}
	}
	if command.Args[0] != "ssh" {
		t.Fatalf("command does not start with ssh: %#v", command.Args)
	}
	assertRegularSSHCommandSafety(t, command.Args)
	if _, err := config.CanonicalSSHHost(strings.TrimPrefix(strings.Split(target, "@")[1], "[")); err != nil {
		t.Fatalf("target host is not canonical: %q", target)
	}
}
