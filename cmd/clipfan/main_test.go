package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/version"
)

func TestRunVersionCommandPrintsBuildVersion(t *testing.T) {
	oldVersion := version.Version
	version.Version = "test-version"
	defer func() { version.Version = oldVersion }()

	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"version"}, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	if got := stdout.String(); got != "test-version\n" {
		t.Fatalf("stdout = %q, want test-version newline", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunVersionJSONCommandPrintsConfigV2CapabilityWithoutConfigLoad(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	oldVersion := version.Version
	version.Version = "test-version"
	defer func() { version.Version = oldVersion }()

	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"version", "--json"}, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var payload struct {
		Version      string `json:"version"`
		Capabilities struct {
			ConfigV2          bool   `json:"config_v2"`
			ConfigV2LocalAuth string `json:"config_v2_local_auth"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("version --json payload is not JSON: %v\n%s", err, stdout.String())
	}
	if payload.Version != "test-version" {
		t.Fatalf("version = %q, want test-version", payload.Version)
	}
	if !payload.Capabilities.ConfigV2 {
		t.Fatal("version --json did not advertise config_v2 capability")
	}
	if payload.Capabilities.ConfigV2LocalAuth != "clipfan-v1/request-hmac" {
		t.Fatalf("config_v2_local_auth = %q, want clipfan-v1/request-hmac", payload.Capabilities.ConfigV2LocalAuth)
	}
	_, err := os.Stat(filepath.Join(configRoot, "clipfan", "config.json"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("version --json touched config file: stat err = %v", err)
	}
}

func TestRunSSHGatewayCommandDispatchesForcedCommandProbe(t *testing.T) {
	oldVersion := version.Version
	version.Version = "test-version"
	defer func() { version.Version = oldVersion }()
	t.Setenv("SSH_ORIGINAL_COMMAND", "probe-authorized-key")

	var stdout, stderr bytes.Buffer
	exitCode := run([]string{"ssh-gateway", "--authorized-peer", "linux-a", "--authorized-key-id", "key-123456"}, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var payload struct {
		Status string `json:"status"`
		PeerID string `json:"peer_id"`
		KeyID  string `json:"key_id"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if payload.Status != "ok" || payload.PeerID != "linux-a" || payload.KeyID != "key-123456" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestRunSSHInstallAuthorizedKeyDispatchesWithoutDaemonFallback(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	var stdout, stderr bytes.Buffer
	exitCode := run([]string{
		"ssh-install-authorized-key",
		"--peer", "linux-b",
		"--key-id", "key-123456",
		"--gateway-path", "/home/jesse/.local/bin/clipfan",
		"--public-key", "AAAAC3NzaC1lZDI1NTE5AAAAIC6JxQKUfHw2JMc2+5ZUTc5xI8QX1sGm8c5C7h4eY7p1",
	}, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var payload struct {
		Status  string `json:"status"`
		Changed bool   `json:"changed"`
		PeerID  string `json:"peer_id"`
		KeyID   string `json:"key_id"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if payload.Status != "ok" || !payload.Changed || payload.PeerID != "linux-b" || payload.KeyID != "key-123456" {
		t.Fatalf("payload = %#v", payload)
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh", "authorized_keys")); err != nil {
		t.Fatalf("authorized_keys not written: %v", err)
	}
}

func TestRunSSHRunProbeDispatchesWithoutDaemonFallbackAndStaysHiddenFromHelp(t *testing.T) {
	var helpOut, helpErr bytes.Buffer
	helpExit := run([]string{"help"}, &helpOut, &helpErr)
	if helpExit != 0 {
		t.Fatalf("help exit = %d, want 0", helpExit)
	}
	if bytes.Contains(helpErr.Bytes(), []byte("ssh-run-probe")) || bytes.Contains(helpOut.Bytes(), []byte("ssh-run-probe")) {
		t.Fatalf("ssh-run-probe appeared in help: stdout=%q stderr=%q", helpOut.String(), helpErr.String())
	}

	var stdout, stderr bytes.Buffer
	exitCode := run([]string{
		"ssh-run-probe",
		"--user", "jesse",
		"--host", "example.com",
		"--port", "22",
		"--private-key", "/home/jesse/.config/clipfan/ssh/sync_ed25519",
		"--known-hosts", "/home/jesse/.config/clipfan/ssh/known_hosts",
		"--expect-peer", "bad peer",
		"--expect-key-id", "key-123456",
	}, &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("clipfan ssh-run-probe:")) {
		t.Fatalf("stderr = %q, want ssh-run-probe dispatch error", stderr.String())
	}
}
