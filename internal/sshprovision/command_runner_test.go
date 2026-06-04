package sshprovision

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecCommandRunnerRunsArgvAndBoundsOutput(t *testing.T) {
	t.Parallel()

	script := writeRunnerScript(t, "printf 'abcdef'; printf 'ghijkl' >&2\n")
	output, err := (ExecCommandRunner{MaxOutputBytes: 4}).Run(context.Background(), SSHCommand{Args: []string{script}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if string(output.Stdout) != "abcd" {
		t.Fatalf("Stdout = %q, want abcd", string(output.Stdout))
	}
	if string(output.Stderr) != "ghij" {
		t.Fatalf("Stderr = %q, want ghij", string(output.Stderr))
	}
}

func TestExecCommandRunnerRejectsEmptyCommand(t *testing.T) {
	t.Parallel()

	_, err := (ExecCommandRunner{}).Run(context.Background(), SSHCommand{})
	if !errors.Is(err, ErrSSHCommandFailed) {
		t.Fatalf("Run() error = %v, want ErrSSHCommandFailed", err)
	}
}

func TestExecCommandRunnerRedactsFailureDiagnostics(t *testing.T) {
	t.Parallel()

	script := writeRunnerScript(t, "printf '%s\\n' \"$@\" >&2\nexit 7\n")
	privateKey := "/home/jesse/.config/clipfan/ssh/sync_ed25519"
	knownHosts := "/home/jesse/.config/clipfan/ssh/known_hosts"
	remoteCommand := "'/home/jesse/.local/bin/clipfan' 'ssh-install-authorized-key' '--gateway-path' '/home/jesse/.local/bin/clipfan' '--public-key' '" + testEd25519Key + "'"
	_, err := (ExecCommandRunner{}).Run(context.Background(), SSHCommand{Args: []string{
		script,
		"-i", privateKey,
		"-o", "UserKnownHostsFile=" + knownHosts,
		remoteCommand,
	}})
	if err == nil {
		t.Fatal("Run() error = nil, want error")
	}
	var commandErr SSHCommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("Run() error = %T, want SSHCommandError", err)
	}
	message := err.Error()
	for _, leaked := range []string{privateKey, knownHosts, testEd25519Key, "/home/jesse/.local/bin/clipfan"} {
		if strings.Contains(message, leaked) {
			t.Fatalf("error leaked %q: %s", leaked, message)
		}
	}
	for _, want := range []string{"<private_key>", "UserKnownHostsFile=<known_hosts>", "'--public-key' '<public_key>'"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error = %q, want marker %q", message, want)
		}
	}
}

func TestExecCommandRunnerPropagatesCanceledContext(t *testing.T) {
	t.Parallel()

	script := writeRunnerScript(t, "sleep 1\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (ExecCommandRunner{}).Run(ctx, SSHCommand{Args: []string{script}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}

func writeRunnerScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runner-script")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}
