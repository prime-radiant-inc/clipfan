package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
	"github.com/prime-radiant-inc/clipfan/internal/version"
)

func TestRunSSHGatewayAllowsProbeCommand(t *testing.T) {
	configRoot := t.TempDir()
	stateRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_STATE_HOME", stateRoot)
	oldVersion := version.Version
	version.Version = "test-version"
	t.Cleanup(func() { version.Version = oldVersion })

	var stdout, stderr bytes.Buffer
	err := runSSHGateway(
		[]string{"--authorized-peer", "linux-a", "--authorized-key-id", "key-123456"},
		&stdout,
		&stderr,
		func(key string) string {
			if key == "SSH_ORIGINAL_COMMAND" {
				return sshprovision.SSHGatewayProbeCommand
			}
			return ""
		},
	)
	if err != nil {
		t.Fatalf("runSSHGateway() error = %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var payload struct {
		Status  string `json:"status"`
		PeerID  string `json:"peer_id"`
		KeyID   string `json:"key_id"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if payload.Status != "ok" || payload.PeerID != "linux-a" || payload.KeyID != "key-123456" || payload.Version != "test-version" {
		t.Fatalf("payload = %#v", payload)
	}
	assertPathMissing(t, filepath.Join(configRoot, "clipfan"))
	assertPathMissing(t, filepath.Join(stateRoot, "clipfan"))
}

func TestRunSSHGatewayAllowsInjectedPrivateSyncStreamCommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	var got SSHGatewayIdentity
	err := runSSHGatewayWithHandlers(
		[]string{"--authorized-peer", "linux-a", "--authorized-key-id", "key-123456"},
		&stdout,
		&stderr,
		func(key string) string {
			if key == "SSH_ORIGINAL_COMMAND" {
				return sshprovision.SSHGatewaySyncStreamCommand
			}
			return ""
		},
		SSHGatewayHandlers{
			SyncStream: func(identity SSHGatewayIdentity, stdout io.Writer) error {
				got = identity
				_, err := stdout.Write([]byte("stream-owned\n"))
				return err
			},
		},
	)
	if err != nil {
		t.Fatalf("runSSHGatewayWithHandlers() error = %v", err)
	}
	if got.PeerID != "linux-a" || got.KeyID != "key-123456" {
		t.Fatalf("identity = %#v", got)
	}
	if stdout.String() != "stream-owned\n" {
		t.Fatalf("stdout = %q, want handler-owned stream output", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunSSHGatewayRejectsShellAndUnknownCommands(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		command string
	}{
		{name: "empty command", command: ""},
		{name: "leading whitespace", command: " " + sshprovision.SSHGatewayProbeCommand},
		{name: "trailing whitespace", command: sshprovision.SSHGatewayProbeCommand + " "},
		{name: "newline", command: sshprovision.SSHGatewayProbeCommand + "\n"},
		{name: "shell command", command: "sh"},
		{name: "probe plus extra token", command: sshprovision.SSHGatewayProbeCommand + " --shell"},
		{name: "probe shell separator", command: sshprovision.SSHGatewayProbeCommand + ";sh"},
		{name: "scp", command: "scp -t file"},
		{name: "sftp", command: "sftp"},
		{name: "version", command: "version"},
		{name: "receive", command: "receive"},
		{name: "sync stream gated off", command: sshprovision.SSHGatewaySyncStreamCommand},
		{name: "peer metadata in command", command: sshprovision.SSHGatewayProbeCommand + " --authorized-peer linux-a"},
		{name: "key metadata in command", command: sshprovision.SSHGatewayProbeCommand + " --authorized-key-id key-123456"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			err := runSSHGateway(
				[]string{"--authorized-peer", "linux-a", "--authorized-key-id", "key-123456"},
				&stdout,
				&stderr,
				func(key string) string {
					if key == "SSH_ORIGINAL_COMMAND" {
						return tc.command
					}
					return ""
				},
			)
			if !errors.Is(err, ErrSSHGatewayCommandRejected) {
				t.Fatalf("runSSHGateway() error = %v, want ErrSSHGatewayCommandRejected", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestRunSSHGatewayRejectsInvalidIdentity(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "missing peer", args: []string{"--authorized-key-id", "key-123456"}},
		{name: "invalid peer", args: []string{"--authorized-peer", "bad peer", "--authorized-key-id", "key-123456"}},
		{name: "missing key id", args: []string{"--authorized-peer", "linux-a"}},
		{name: "invalid key id", args: []string{"--authorized-peer", "linux-a", "--authorized-key-id", "short"}},
		{name: "extra arg", args: []string{"--authorized-peer", "linux-a", "--authorized-key-id", "key-123456", "extra"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			err := runSSHGateway(tc.args, &stdout, &stderr, func(string) string { return sshprovision.SSHGatewayProbeCommand })
			if err == nil {
				t.Fatal("runSSHGateway() error = nil, want error")
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestSSHGatewayForcedCommandMatchesManagedAuthorizedKey(t *testing.T) {
	t.Parallel()

	entry := sshprovision.ManagedAuthorizedKey{
		PeerID:      "linux-a",
		KeyID:       "key-123456",
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   "AAAAC3NzaC1lZDI1NTE5AAAAIC6JxQKUfHw2JMc2+5ZUTc5xI8QX1sGm8c5C7h4eY7p1",
	}
	managed, err := sshprovision.NewManagedAuthorizedKey(entry)
	if err != nil {
		t.Fatalf("NewManagedAuthorizedKey() error = %v", err)
	}

	want := "/home/jesse/.local/bin/clipfan ssh-gateway --authorized-peer linux-a --authorized-key-id key-123456"
	if got := managed.ForcedCommand(); got != want {
		t.Fatalf("ForcedCommand() = %q, want %q", got, want)
	}
	if sshprovision.SSHGatewayProbeCommand != "probe-authorized-key" {
		t.Fatalf("SSHGatewayProbeCommand = %q, want probe-authorized-key", sshprovision.SSHGatewayProbeCommand)
	}
	if sshprovision.SSHGatewaySyncStreamCommand != "sync-stream" {
		t.Fatalf("SSHGatewaySyncStreamCommand = %q, want sync-stream", sshprovision.SSHGatewaySyncStreamCommand)
	}
}

func TestRunSSHGatewayDoesNotEchoRejectedOriginalCommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	err := runSSHGateway(
		[]string{"--authorized-peer", "linux-a", "--authorized-key-id", "key-123456"},
		&stdout,
		&stderr,
		func(key string) string {
			if key == "SSH_ORIGINAL_COMMAND" {
				return "secret-command with tokens"
			}
			return ""
		},
	)
	if !errors.Is(err, ErrSSHGatewayCommandRejected) {
		t.Fatalf("runSSHGateway() error = %v, want ErrSSHGatewayCommandRejected", err)
	}
	if strings.Contains(stderr.String(), "secret-command") || strings.Contains(err.Error(), "secret-command") {
		t.Fatalf("rejected command leaked: stderr=%q err=%v", stderr.String(), err)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s exists or stat failed unexpectedly: %v", path, err)
	}
}
