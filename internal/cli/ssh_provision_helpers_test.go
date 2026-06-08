package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
	"github.com/prime-radiant-inc/clipfan/internal/storagecheck"
)

func TestRunSSHEnsureSyncKeyCreatesAndLoadsPublicMetadata(t *testing.T) {
	t.Parallel()

	root := cliProvisionTempDir(t)
	keyPath := filepath.Join(root, "ssh", "sync_ed25519")
	if err := os.Mkdir(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatalf("mkdir key dir: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := runSSHEnsureSyncKey(
		[]string{"--host-id", "linux-b", "--key-path", keyPath},
		&stdout,
		&stderr,
		localProvisionChecker(),
		fakeProvisionSyncKeyGenerator("PRIVATE-MATERIAL", testCLIProvisionPublicKey),
	)
	if err != nil {
		t.Fatalf("runSSHEnsureSyncKey() error = %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	payload := decodeSyncKeyPayload(t, stdout.Bytes())
	if payload.Status != "ok" || !payload.Changed || payload.HostID != "linux-b" || payload.PublicKey != testCLIProvisionPublicKey || payload.PrivateKeyPath != keyPath {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.KeyID != syncKeyIDForTest(t, testCLIProvisionPublicKey) {
		t.Fatalf("KeyID = %q, want derived key id", payload.KeyID)
	}
	if strings.Contains(stdout.String(), "PRIVATE-MATERIAL") || strings.Contains(stderr.String(), "PRIVATE-MATERIAL") {
		t.Fatalf("private key material leaked: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	var secondOut, secondErr bytes.Buffer
	err = runSSHEnsureSyncKey(
		[]string{"--host-id", "linux-b", "--key-path", keyPath},
		&secondOut,
		&secondErr,
		localProvisionChecker(),
		fakeProvisionSyncKeyGenerator("NEW-PRIVATE-MATERIAL", testCLIProvisionPublicKey),
	)
	if err != nil {
		t.Fatalf("second runSSHEnsureSyncKey() error = %v", err)
	}
	second := decodeSyncKeyPayload(t, secondOut.Bytes())
	if second.Changed {
		t.Fatalf("second Changed = true, want false")
	}
	if strings.Contains(secondOut.String(), "NEW-PRIVATE-MATERIAL") {
		t.Fatalf("new private key material leaked or generator ran: %q", secondOut.String())
	}
}

func TestRunSSHEnsureSyncKeyCreatesMissingParentDirectory(t *testing.T) {
	t.Parallel()

	root := cliProvisionTempDir(t)
	keyPath := filepath.Join(root, "missing", "ssh", "sync_ed25519")
	var stdout, stderr bytes.Buffer
	err := runSSHEnsureSyncKey(
		[]string{"--host-id", "linux-b", "--key-path", keyPath},
		&stdout,
		&stderr,
		localProvisionChecker(),
		fakeProvisionSyncKeyGenerator("PRIVATE-MATERIAL", testCLIProvisionPublicKey),
	)
	if err != nil {
		t.Fatalf("runSSHEnsureSyncKey() error = %v", err)
	}
	payload := decodeSyncKeyPayload(t, stdout.Bytes())
	if payload.Status != "ok" || !payload.Changed || payload.PrivateKeyPath != keyPath {
		t.Fatalf("payload = %#v", payload)
	}
	assertModeForTest(t, filepath.Join(root, "missing"), 0o700)
	assertModeForTest(t, filepath.Join(root, "missing", "ssh"), 0o700)
}

func TestRunSSHEnsureSyncKeyRejectsInvalidInputWithoutCreatingMaterial(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		args       func(string) []string
		mustNotDir func(string) string
	}{
		{
			name: "missing host",
			args: func(root string) []string {
				return []string{"--key-path", filepath.Join(root, "missing-host", "ssh", "sync_ed25519")}
			},
			mustNotDir: func(root string) string { return filepath.Join(root, "missing-host") },
		},
		{
			name: "invalid host",
			args: func(root string) []string {
				return []string{"--host-id", "bad host", "--key-path", filepath.Join(root, "invalid-host", "ssh", "sync_ed25519")}
			},
			mustNotDir: func(root string) string { return filepath.Join(root, "invalid-host") },
		},
		{
			name: "missing key path",
			args: func(string) []string { return []string{"--host-id", "linux-b"} },
		},
		{
			name: "relative key path",
			args: func(string) []string { return []string{"--host-id", "linux-b", "--key-path", "sync_ed25519"} },
		},
		{
			name: "extra arg",
			args: func(root string) []string {
				return []string{"--host-id", "linux-b", "--key-path", filepath.Join(root, "extra-arg", "ssh", "sync_ed25519"), "extra"}
			},
			mustNotDir: func(root string) string { return filepath.Join(root, "extra-arg") },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := cliProvisionTempDir(t)
			var stdout, stderr bytes.Buffer
			err := runSSHEnsureSyncKey(tc.args(root), &stdout, &stderr, localProvisionChecker(), fakeProvisionSyncKeyGenerator("PRIVATE", testCLIProvisionPublicKey))
			if err == nil {
				t.Fatal("runSSHEnsureSyncKey() error = nil, want error")
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if tc.mustNotDir != nil {
				if _, statErr := os.Stat(tc.mustNotDir(root)); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("rejected input created %s: %v", tc.mustNotDir(root), statErr)
				}
			}
		})
	}
}

func TestLoadOrCreateSyncKeyReloadsAfterConcurrentCreateWins(t *testing.T) {
	t.Parallel()

	created := config.SyncKeyCreateResult{
		PrivateKeyPath: "/home/jesse/.config/clipfan/ssh/sync_ed25519",
		PublicKey:      "ssh-ed25519 " + testCLIProvisionPublicKey + " clipfan:linux-b",
		KeyID:          syncKeyIDForTest(t, testCLIProvisionPublicKey),
	}
	loadCalls := 0
	load := func(opts config.SyncKeyLoadOptions) (config.SyncKeyCreateResult, error) {
		loadCalls++
		if loadCalls == 1 {
			return config.SyncKeyCreateResult{}, config.ErrMissingSyncKey
		}
		if opts.KeyPath != created.PrivateKeyPath || opts.HostID != "linux-b" {
			t.Fatalf("load opts = %#v", opts)
		}
		return created, nil
	}
	create := func(opts config.SyncKeyCreateOptions) (config.SyncKeyCreateResult, error) {
		if opts.KeyPath != created.PrivateKeyPath || opts.HostID != "linux-b" {
			t.Fatalf("create opts = %#v", opts)
		}
		return config.SyncKeyCreateResult{}, config.ErrSyncKeyExists
	}

	generator := func(config.SyncKeyGenerateRequest) error { return nil }
	result, changed, err := loadOrCreateSyncKeyWithOps(created.PrivateKeyPath, "linux-b", localProvisionChecker(), generator, load, create)
	if err != nil {
		t.Fatalf("loadOrCreateSyncKeyWithOps() error = %v", err)
	}
	if changed {
		t.Fatal("changed = true, want false after reloading concurrent winner")
	}
	if result.KeyID != created.KeyID || result.PublicKey != created.PublicKey {
		t.Fatalf("result = %#v, want %#v", result, created)
	}
	if loadCalls != 2 {
		t.Fatalf("loadCalls = %d, want 2", loadCalls)
	}
}

func TestRunSSHInstallKnownHostWritesConfirmedPin(t *testing.T) {
	t.Parallel()

	root := cliProvisionTempDir(t)
	knownHostsPath := filepath.Join(root, "ssh", "known_hosts")
	var stdout, stderr bytes.Buffer
	err := runSSHInstallKnownHost(
		[]string{
			"--known-hosts", knownHostsPath,
			"--host", "Example.COM.",
			"--port", "2200",
			"--key-type", "ssh-ed25519",
			"--public-key", testCLIProvisionPublicKey,
		},
		&stdout,
		&stderr,
		sshprovision.UpsertKnownHostPin,
	)
	if err != nil {
		t.Fatalf("runSSHInstallKnownHost() error = %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var payload struct {
		Status  string `json:"status"`
		Pattern string `json:"pattern"`
		KeyType string `json:"key_type"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if payload.Status != "ok" || payload.Pattern != "[example.com]:2200" || payload.KeyType != "ssh-ed25519" {
		t.Fatalf("payload = %#v", payload)
	}
	body, err := os.ReadFile(knownHostsPath)
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	if got, want := string(body), "[example.com]:2200 ssh-ed25519 "+testCLIProvisionPublicKey+"\n"; got != want {
		t.Fatalf("known_hosts = %q, want %q", got, want)
	}
}

func TestRunSSHInstallKnownHostRejectsInvalidInputWithoutWriting(t *testing.T) {
	t.Parallel()

	root := cliProvisionTempDir(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "missing known hosts", args: []string{"--host", "example.com", "--port", "22", "--key-type", "ssh-ed25519", "--public-key", testCLIProvisionPublicKey}},
		{name: "invalid host", args: []string{"--known-hosts", filepath.Join(root, "known_hosts"), "--host", "example.com;sh", "--port", "22", "--key-type", "ssh-ed25519", "--public-key", testCLIProvisionPublicKey}},
		{name: "invalid port", args: []string{"--known-hosts", filepath.Join(root, "known_hosts"), "--host", "example.com", "--port", "0", "--key-type", "ssh-ed25519", "--public-key", testCLIProvisionPublicKey}},
		{name: "invalid key type", args: []string{"--known-hosts", filepath.Join(root, "known_hosts"), "--host", "example.com", "--port", "22", "--key-type", "bad type", "--public-key", testCLIProvisionPublicKey}},
		{name: "invalid public key", args: []string{"--known-hosts", filepath.Join(root, "known_hosts"), "--host", "example.com", "--port", "22", "--key-type", "ssh-ed25519", "--public-key", "not-base64"}},
		{name: "extra arg", args: []string{"--known-hosts", filepath.Join(root, "known_hosts"), "--host", "example.com", "--port", "22", "--key-type", "ssh-ed25519", "--public-key", testCLIProvisionPublicKey, "extra"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			err := runSSHInstallKnownHost(tc.args, &stdout, &stderr, sshprovision.UpsertKnownHostPin)
			if err == nil {
				t.Fatal("runSSHInstallKnownHost() error = nil, want error")
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestRunSSHInstallKnownHostDoesNotEchoPublicKeyOnWriterFailure(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	err := runSSHInstallKnownHost(
		[]string{
			"--known-hosts", "/home/jesse/.config/clipfan/ssh/known_hosts",
			"--host", "example.com",
			"--port", "22",
			"--key-type", "ssh-ed25519",
			"--public-key", testCLIProvisionPublicKey,
		},
		&stdout,
		&stderr,
		func(string, sshprovision.KnownHostPin) error {
			return errors.New("write failed")
		},
	)
	if err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("runSSHInstallKnownHost() error = %v, want write failed", err)
	}
	if strings.Contains(err.Error(), testCLIProvisionPublicKey) || strings.Contains(stderr.String(), testCLIProvisionPublicKey) {
		t.Fatalf("public key leaked: err=%v stderr=%q", err, stderr.String())
	}
}

func decodeSyncKeyPayload(t *testing.T, data []byte) syncKeyPayload {
	t.Helper()
	var payload syncKeyPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, string(data))
	}
	return payload
}

type syncKeyPayload struct {
	Status         string `json:"status"`
	Changed        bool   `json:"changed"`
	HostID         string `json:"host_id"`
	KeyID          string `json:"key_id"`
	PublicKey      string `json:"public_key"`
	PrivateKeyPath string `json:"private_key_path"`
}

func localProvisionChecker() storagecheck.Checker {
	local := true
	return storagecheck.Checker{
		Probe: func(string) (storagecheck.Fact, error) {
			return storagecheck.Fact{FilesystemType: "apfs", Local: &local}, nil
		},
		Smoke: func(string) error { return nil },
	}
}

func fakeProvisionSyncKeyGenerator(privateKey string, publicKey string) config.SyncKeyGenerator {
	return func(req config.SyncKeyGenerateRequest) error {
		if err := os.WriteFile(req.KeyPath, []byte(privateKey), 0o600); err != nil {
			return err
		}
		return os.WriteFile(req.PublicKeyPath, []byte("ssh-ed25519 "+publicKey+" clipfan:"+req.HostID+"\n"), 0o644)
	}
}

func cliProvisionTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func syncKeyIDForTest(t *testing.T, publicKey string) string {
	t.Helper()
	blob, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:8])
}

func assertModeForTest(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %#o, want %#o", path, got, want)
	}
}

const testCLIProvisionPublicKey = "AAAAC3NzaC1lZDI1NTE5AAAAIC6JxQKUfHw2JMc2+5ZUTc5xI8QX1sGm8c5C7h4eY7p1"
