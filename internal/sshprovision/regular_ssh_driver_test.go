package sshprovision

import (
	"context"
	"errors"
	"reflect"
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
		ConfirmedHostKeyLines: map[string]string{"mac-a": "mac-a.tailnet ssh-ed25519 " + testEd25519Key},
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
	if result.ConnectorSyncKey.KeyID != testEd25519KeyID {
		t.Fatalf("ConnectorSyncKey = %#v", result.ConnectorSyncKey)
	}
	if configMutation.ConnectorKnownHostsPath != "/home/jesse/.config/clipfan/ssh/known_hosts" {
		t.Fatalf("config mutation = %#v", configMutation)
	}

	if len(runner.commands) != 4 {
		t.Fatalf("runner command count = %d, want 4: %#v", len(runner.commands), runner.commands)
	}
	assertRemoteCommand(t, runner.commands[0], "jesse@linux-b.tailnet", "ssh-install-known-host", "--host", "mac-a.tailnet")
	assertRemoteCommand(t, runner.commands[1], "jesse@linux-b.tailnet", "ssh-ensure-sync-key", "--key-path", "/home/jesse/.config/clipfan/ssh/sync_ed25519")
	assertRemoteCommand(t, runner.commands[2], "jesse@mac-a.tailnet", "ssh-install-authorized-key", "--peer", "linux-b")
	assertRemoteCommand(t, runner.commands[3], "jesse@linux-b.tailnet", "ssh-run-probe", "--host", "mac-a.tailnet")
	assertRemoteCommand(t, runner.commands[3], "jesse@linux-b.tailnet", "ssh-run-probe", "--expect-peer", "linux-b")
	assertRemoteCommand(t, runner.commands[3], "jesse@linux-b.tailnet", "ssh-run-probe", "--expect-key-id", testEd25519KeyID)
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
		ConfirmedHostKeyLines: map[string]string{"mac-a": "mac-a.tailnet ssh-ed25519 " + testEd25519Key},
		WriteConfigFunc:       func(context.Context, DirectPairConfigMutation) error { return nil },
	}
	provisioner := DirectPairProvisioner{Driver: driver, configV2WriteGate: func() bool { return true }}

	_, err := provisioner.Provision(context.Background(), validDirectPairProvisionInput())
	if !errors.Is(err, ErrRemoteProvisionOutput) {
		t.Fatalf("Provision() error = %v, want ErrRemoteProvisionOutput", err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("runner command count = %d, want 2", len(runner.commands))
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
		ConfirmedHostKeyLines: map[string]string{"mac-a": "mac-a.tailnet ssh-ed25519 " + testEd25519Key},
		WriteConfigFunc:       func(context.Context, DirectPairConfigMutation) error { return nil },
	}
	provisioner := DirectPairProvisioner{Driver: driver, configV2WriteGate: func() bool { return true }}

	_, err := provisioner.Provision(context.Background(), validDirectPairProvisionInput())
	if !errors.Is(err, ErrInvalidAuthorizedKey) {
		t.Fatalf("Provision() error = %v, want ErrInvalidAuthorizedKey", err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("runner command count = %d, want 2", len(runner.commands))
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
		return CommandOutput{Stdout: []byte(`{"status":"ok","changed":true,"host_id":"linux-b","key_id":"` + testEd25519KeyID + `","public_key":"` + testEd25519Key + `","private_key_path":"/home/jesse/.config/clipfan/ssh/sync_ed25519"}`)}, nil
	case strings.Contains(remote, "ssh-install-authorized-key"):
		return CommandOutput{Stdout: []byte(`{"status":"ok","changed":true,"peer_id":"linux-b","key_id":"` + testEd25519KeyID + `"}`)}, nil
	case strings.Contains(remote, "ssh-run-probe"):
		return CommandOutput{Stdout: []byte(`{"status":"ok","peer_id":"linux-b","key_id":"` + testEd25519KeyID + `"}`)}, nil
	default:
		return CommandOutput{Stdout: []byte(`{"status":"ok"}`)}, nil
	}
}

const testEd25519KeyID = "626e58c17d770373"

func assertRemoteCommand(t *testing.T, command SSHCommand, target string, subcommand string, flag string, value string) {
	t.Helper()
	if len(command.Args) < 2 {
		t.Fatalf("command args too short: %#v", command.Args)
	}
	if command.Args[len(command.Args)-2] != target {
		t.Fatalf("target = %q, want %q; args=%#v", command.Args[len(command.Args)-2], target, command.Args)
	}
	remote := command.Args[len(command.Args)-1]
	for _, want := range []string{"'" + subcommand + "'", "'" + flag + "'", "'" + value + "'"} {
		if !strings.Contains(remote, want) {
			t.Fatalf("remote command = %q, want %q", remote, want)
		}
	}
	if !reflect.DeepEqual(command.Args[:2], []string{"ssh", "-F"}) {
		t.Fatalf("command does not start with ssh -F: %#v", command.Args)
	}
	if _, err := config.CanonicalSSHHost(strings.TrimPrefix(strings.Split(target, "@")[1], "[")); err != nil {
		t.Fatalf("target host is not canonical: %q", target)
	}
}
