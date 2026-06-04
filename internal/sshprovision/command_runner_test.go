package sshprovision

import (
	"context"
	"errors"
	"fmt"
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
	if !output.StdoutTruncated || !output.StderrTruncated {
		t.Fatalf("truncated flags = %v/%v, want true/true", output.StdoutTruncated, output.StderrTruncated)
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
		if strings.Contains(fmt.Sprintf("%#v", commandErr), leaked) {
			t.Fatalf("formatted SSHCommandError leaked %q: %#v", leaked, commandErr)
		}
	}
	for _, want := range []string{"<private_key>", "UserKnownHostsFile=<known_hosts>", "'--public-key' '<public_key>'"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error = %q, want marker %q", message, want)
		}
	}
	if commandErr.RedactedCommand() == "" || commandErr.RedactedStderr() == "" {
		t.Fatalf("redacted accessors returned empty command/stderr: %#v", commandErr)
	}
}

func TestRedactSSHDiagnosticRedactsSecretLikeFields(t *testing.T) {
	t.Parallel()

	value := `{"shared_key":"fleet-secret","token":"token-value","nonce":"nonce-value","password":"password-value-with-escaped-quote-\"-suffix","credential":"credential-value","private_key":"private-key-value"} hmac=hmac-value sync_key_path=/home/jesse/.config/clipfan/ssh/sync_ed25519`
	redacted := redactSSHDiagnostic(value, nil)
	for _, leaked := range []string{"fleet-secret", "token-value", "nonce-value", "password-value-with-escaped-quote", "suffix", "credential-value", "private-key-value", "hmac-value", "/home/jesse/.config/clipfan/ssh/sync_ed25519"} {
		if strings.Contains(redacted, leaked) {
			t.Fatalf("redacted diagnostic leaked %q: %s", leaked, redacted)
		}
	}
	for _, want := range []string{`"shared_key":"<redacted>"`, `"token":"<redacted>"`, "hmac=<redacted>", "sync_key_path=<redacted>"} {
		if !strings.Contains(redacted, want) {
			t.Fatalf("redacted = %q, want %q", redacted, want)
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
