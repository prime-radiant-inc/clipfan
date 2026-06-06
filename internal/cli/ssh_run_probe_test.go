package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

func TestRunSSHRunProbeBuildsAndRunsPinnedProbe(t *testing.T) {
	t.Parallel()

	runner := &fakeProbeRunner{}
	var stdout, stderr bytes.Buffer
	err := runSSHRunProbe(
		context.Background(),
		[]string{
			"--user", "jesse",
			"--host", "Example.COM.",
			"--port", "2200",
			"--private-key", "/home/jesse/.config/clipfan/ssh/sync_ed25519",
			"--known-hosts", "/home/jesse/.config/clipfan/ssh/known_hosts",
			"--expect-peer", "linux-b",
			"--expect-key-id", "key-123456",
		},
		&stdout,
		&stderr,
		runner,
	)
	if err != nil {
		t.Fatalf("runSSHRunProbe() error = %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
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
		sshprovision.SSHGatewayProbeCommand,
	}
	assertStringSlicesEqual(t, runner.command.Args, want)
	var payload struct {
		Status string `json:"status"`
		PeerID string `json:"peer_id"`
		KeyID  string `json:"key_id"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if payload.Status != "ok" || payload.PeerID != "linux-b" || payload.KeyID != "key-123456" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestRunSSHRunProbeRejectsInvalidInputBeforeRunning(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "missing user", args: validRunProbeArgs("--user")},
		{name: "invalid host", args: replaceRunProbeArg("--host", "example.com;sh")},
		{name: "host with port suffix", args: replaceRunProbeArg("--host", "example.com:22")},
		{name: "invalid port zero", args: replaceRunProbeArg("--port", "0")},
		{name: "invalid port too large", args: replaceRunProbeArg("--port", "65536")},
		{name: "relative private key", args: replaceRunProbeArg("--private-key", "sync_ed25519")},
		{name: "unsafe known hosts", args: replaceRunProbeArg("--known-hosts", "/home/jesse/.config/clipfan/ssh/../known_hosts")},
		{name: "missing expected peer", args: validRunProbeArgs("--expect-peer")},
		{name: "invalid expected key", args: replaceRunProbeArg("--expect-key-id", "short")},
		{name: "extra arg", args: append(validRunProbeArgs(), "extra")},
		{name: "unknown flag", args: append(validRunProbeArgs(), "--remote-command", "sh")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runner := &fakeProbeRunner{}
			var stdout, stderr bytes.Buffer
			err := runSSHRunProbe(context.Background(), tc.args, &stdout, &stderr, runner)
			if err == nil {
				t.Fatal("runSSHRunProbe() error = nil, want error")
			}
			if len(runner.command.Args) != 0 {
				t.Fatalf("runner command = %#v, want empty", runner.command.Args)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestRunSSHRunProbeReturnsRunnerFailure(t *testing.T) {
	t.Parallel()

	privateKey := "/home/jesse/.config/clipfan/ssh/sync_ed25519"
	knownHosts := "/home/jesse/.config/clipfan/ssh/known_hosts"
	runner := &fakeProbeRunner{err: sshprovision.SSHCommandError{}}
	var stdout, stderr bytes.Buffer
	err := runSSHRunProbe(context.Background(), validRunProbeArgs(), &stdout, &stderr, runner)
	if err == nil {
		t.Fatal("runSSHRunProbe() error = nil, want error")
	}
	if strings.Contains(err.Error(), privateKey) || strings.Contains(err.Error(), knownHosts) {
		t.Fatalf("runner failure leaked sensitive paths: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunSSHRunProbeReturnsExplicitContextTimeout(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 0)
	cancel()
	runner := &fakeProbeRunner{err: context.DeadlineExceeded}
	var stdout, stderr bytes.Buffer
	err := runSSHRunProbe(ctx, validRunProbeArgs(), &stdout, &stderr, runner)
	if err == nil {
		t.Fatal("runSSHRunProbe() error = nil, want error")
	}
	if got := err.Error(); got != "ssh_probe_timeout: context deadline exceeded" {
		t.Fatalf("runSSHRunProbe() error = %q, want explicit timeout", got)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunSSHRunProbeRejectsBadProbeOutput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		output sshprovision.CommandOutput
	}{
		{name: "non json", output: sshprovision.CommandOutput{Stdout: []byte("not-json")}},
		{name: "trailing json", output: sshprovision.CommandOutput{Stdout: []byte(`{"status":"ok","peer_id":"linux-b","key_id":"key-123456"} {}`)}},
		{name: "bad status", output: sshprovision.CommandOutput{Stdout: []byte(`{"status":"no","peer_id":"linux-b","key_id":"key-123456"}`)}},
		{name: "wrong peer", output: sshprovision.CommandOutput{Stdout: []byte(`{"status":"ok","peer_id":"linux-c","key_id":"key-123456"}`)}},
		{name: "wrong key", output: sshprovision.CommandOutput{Stdout: []byte(`{"status":"ok","peer_id":"linux-b","key_id":"key-654321"}`)}},
		{name: "invalid peer", output: sshprovision.CommandOutput{Stdout: []byte(`{"status":"ok","peer_id":"bad peer","key_id":"key-123456"}`)}},
		{name: "stderr", output: sshprovision.CommandOutput{Stdout: []byte(`{"status":"ok","peer_id":"linux-b","key_id":"key-123456"}`), Stderr: []byte("warning")}},
		{name: "stdout truncated", output: sshprovision.CommandOutput{Stdout: []byte(`{"status":"ok"`), StdoutTruncated: true}},
		{name: "stderr truncated", output: sshprovision.CommandOutput{Stdout: []byte(`{"status":"ok","peer_id":"linux-b","key_id":"key-123456"}`), StderrTruncated: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runner := &fakeProbeRunner{output: tc.output}
			var stdout, stderr bytes.Buffer
			err := runSSHRunProbe(context.Background(), validRunProbeArgs(), &stdout, &stderr, runner)
			if err == nil {
				t.Fatal("runSSHRunProbe() error = nil, want error")
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

type fakeProbeRunner struct {
	command sshprovision.SSHCommand
	output  sshprovision.CommandOutput
	err     error
}

func (r *fakeProbeRunner) Run(_ context.Context, command sshprovision.SSHCommand) (sshprovision.CommandOutput, error) {
	r.command = command
	if r.err != nil {
		return sshprovision.CommandOutput{}, r.err
	}
	if r.output.Stdout != nil || r.output.Stderr != nil || r.output.StdoutTruncated || r.output.StderrTruncated {
		return r.output, nil
	}
	return sshprovision.CommandOutput{Stdout: []byte(`{"status":"ok","peer_id":"linux-b","key_id":"key-123456"}`)}, nil
}

func validRunProbeArgs(removeFlags ...string) []string {
	args := []string{
		"--user", "jesse",
		"--host", "example.com",
		"--port", "22",
		"--private-key", "/home/jesse/.config/clipfan/ssh/sync_ed25519",
		"--known-hosts", "/home/jesse/.config/clipfan/ssh/known_hosts",
		"--expect-peer", "linux-b",
		"--expect-key-id", "key-123456",
	}
	remove := map[string]bool{}
	for _, flag := range removeFlags {
		remove[flag] = true
	}
	if len(remove) == 0 {
		return args
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i += 2 {
		if remove[args[i]] {
			continue
		}
		out = append(out, args[i], args[i+1])
	}
	return out
}

func replaceRunProbeArg(flag string, value string) []string {
	args := validRunProbeArgs()
	for i := 0; i < len(args)-1; i += 2 {
		if args[i] == flag {
			args[i+1] = value
			return args
		}
	}
	return args
}

func assertStringSlicesEqual(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q; got=%#v", i, got[i], want[i], got)
		}
	}
}
