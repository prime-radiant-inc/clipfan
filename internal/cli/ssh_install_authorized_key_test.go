package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

func TestRunSSHInstallAuthorizedKeyWritesManagedLineForCurrentUser(t *testing.T) {
	t.Parallel()

	home := cliInstallTempHome(t)
	var stdout, stderr bytes.Buffer
	err := runSSHInstallAuthorizedKey(
		[]string{
			"--peer", "linux-b",
			"--key-id", "key-123456",
			"--gateway-path", "/home/jesse/.local/bin/clipfan",
			"--public-key", testCLIInstallPublicKey,
		},
		&stdout,
		&stderr,
		func() (string, error) { return home, nil },
		sshprovision.UpsertManagedAuthorizedKeyFile,
	)
	if err != nil {
		t.Fatalf("runSSHInstallAuthorizedKey() error = %v", err)
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

	body, err := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if err != nil {
		t.Fatalf("read authorized_keys: %v", err)
	}
	entry := mustCLIInstallManagedAuthorizedKey(t, "linux-b", "key-123456")
	if string(body) != entry.Line()+"\n" {
		t.Fatalf("authorized_keys = %q, want %q", string(body), entry.Line()+"\n")
	}
}

func TestRunSSHInstallAuthorizedKeyIsIdempotent(t *testing.T) {
	t.Parallel()

	home := cliInstallTempHome(t)
	args := []string{
		"--peer", "linux-b",
		"--key-id", "key-123456",
		"--gateway-path", "/home/jesse/.local/bin/clipfan",
		"--public-key", testCLIInstallPublicKey,
	}
	var firstOut, firstErr bytes.Buffer
	if err := runSSHInstallAuthorizedKey(args, &firstOut, &firstErr, func() (string, error) { return home, nil }, sshprovision.UpsertManagedAuthorizedKeyFile); err != nil {
		t.Fatalf("first runSSHInstallAuthorizedKey() error = %v", err)
	}

	var secondOut, secondErr bytes.Buffer
	if err := runSSHInstallAuthorizedKey(args, &secondOut, &secondErr, func() (string, error) { return home, nil }, sshprovision.UpsertManagedAuthorizedKeyFile); err != nil {
		t.Fatalf("second runSSHInstallAuthorizedKey() error = %v", err)
	}
	var payload struct {
		Changed bool `json:"changed"`
	}
	if err := json.Unmarshal(secondOut.Bytes(), &payload); err != nil {
		t.Fatalf("second stdout is not JSON: %v\n%s", err, secondOut.String())
	}
	if payload.Changed {
		t.Fatal("second changed = true, want false")
	}
}

func TestRunSSHInstallAuthorizedKeyRejectsInvalidInputWithoutWriting(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "missing peer", args: []string{"--key-id", "key-123456", "--gateway-path", "/home/jesse/.local/bin/clipfan", "--public-key", testCLIInstallPublicKey}},
		{name: "invalid peer", args: []string{"--peer", "bad peer", "--key-id", "key-123456", "--gateway-path", "/home/jesse/.local/bin/clipfan", "--public-key", testCLIInstallPublicKey}},
		{name: "missing key id", args: []string{"--peer", "linux-b", "--gateway-path", "/home/jesse/.local/bin/clipfan", "--public-key", testCLIInstallPublicKey}},
		{name: "invalid key id", args: []string{"--peer", "linux-b", "--key-id", "short", "--gateway-path", "/home/jesse/.local/bin/clipfan", "--public-key", testCLIInstallPublicKey}},
		{name: "missing gateway path", args: []string{"--peer", "linux-b", "--key-id", "key-123456", "--public-key", testCLIInstallPublicKey}},
		{name: "unsafe gateway path", args: []string{"--peer", "linux-b", "--key-id", "key-123456", "--gateway-path", "/home/jesse/.local/bin/clip fan", "--public-key", testCLIInstallPublicKey}},
		{name: "missing public key", args: []string{"--peer", "linux-b", "--key-id", "key-123456", "--gateway-path", "/home/jesse/.local/bin/clipfan"}},
		{name: "full openssh public key line", args: []string{"--peer", "linux-b", "--key-id", "key-123456", "--gateway-path", "/home/jesse/.local/bin/clipfan", "--public-key", "ssh-ed25519 " + testCLIInstallPublicKey + " clipfan"}},
		{name: "extra arg", args: []string{"--peer", "linux-b", "--key-id", "key-123456", "--gateway-path", "/home/jesse/.local/bin/clipfan", "--public-key", testCLIInstallPublicKey, "extra"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			home := cliInstallTempHome(t)
			var stdout, stderr bytes.Buffer
			err := runSSHInstallAuthorizedKey(tc.args, &stdout, &stderr, func() (string, error) { return home, nil }, sshprovision.UpsertManagedAuthorizedKeyFile)
			if err == nil {
				t.Fatal("runSSHInstallAuthorizedKey() error = nil, want error")
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if _, statErr := os.Stat(filepath.Join(home, ".ssh")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf(".ssh was created after invalid input: %v", statErr)
			}
		})
	}
}

func TestRunSSHInstallAuthorizedKeyDoesNotEchoPublicKeyOnWriterFailure(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	err := runSSHInstallAuthorizedKey(
		[]string{
			"--peer", "linux-b",
			"--key-id", "key-123456",
			"--gateway-path", "/home/jesse/.local/bin/clipfan",
			"--public-key", testCLIInstallPublicKey,
		},
		&stdout,
		&stderr,
		func() (string, error) { return "/home/jesse", nil },
		func(string, sshprovision.ManagedAuthorizedKey) (bool, error) {
			return false, errors.New("write failed")
		},
	)
	if err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("runSSHInstallAuthorizedKey() error = %v, want write failed", err)
	}
	if strings.Contains(err.Error(), testCLIInstallPublicKey) || strings.Contains(stderr.String(), testCLIInstallPublicKey) {
		t.Fatalf("public key leaked: err=%v stderr=%q", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func mustCLIInstallManagedAuthorizedKey(t *testing.T, peerID string, keyID string) sshprovision.ManagedAuthorizedKey {
	t.Helper()
	entry, err := sshprovision.NewManagedAuthorizedKey(sshprovision.ManagedAuthorizedKey{
		PeerID:      peerID,
		KeyID:       keyID,
		GatewayPath: "/home/jesse/.local/bin/clipfan",
		PublicKey:   testCLIInstallPublicKey,
	})
	if err != nil {
		t.Fatalf("NewManagedAuthorizedKey() error = %v", err)
	}
	return entry
}

func cliInstallTempHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return home
}

const testCLIInstallPublicKey = "AAAAC3NzaC1lZDI1NTE5AAAAIC6JxQKUfHw2JMc2+5ZUTc5xI8QX1sGm8c5C7h4eY7p1"
