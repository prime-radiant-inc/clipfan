package config

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/storagecheck"
)

func TestCreateLocalSyncKeyCreatesKeyPairAndSidecar(t *testing.T) {
	keyPath := filepath.Join(syncKeyTempDir(t), "sync_ed25519")
	publicBlob := []byte("clipfan public sync key blob")
	publicKey := "ssh-ed25519 " + base64.StdEncoding.EncodeToString(publicBlob) + " clipfan:m4"
	now := time.Date(2026, 6, 1, 12, 34, 56, 0, time.UTC)

	result, err := CreateLocalSyncKey(SyncKeyCreateOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
		Now: func() time.Time {
			return now
		},
		Generator: fakeSyncKeyGenerator("PRIVATE-MATERIAL", publicKey),
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, keyPath); got != "PRIVATE-MATERIAL" {
		t.Fatalf("private key = %q, want fake key material", got)
	}
	if got := fileMode(t, keyPath); got != 0o600 {
		t.Fatalf("private key mode = %#o, want 0600", got)
	}
	if got := readFile(t, keyPath+".pub"); got != publicKey+"\n" {
		t.Fatalf("public key = %q, want generated public key", got)
	}
	if got := fileMode(t, keyPath+".clipfan.json"); got != 0o600 {
		t.Fatalf("sidecar mode = %#o, want 0600", got)
	}

	sum := sha256.Sum256(publicBlob)
	wantDigest := "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
	wantKeyID := hex.EncodeToString(sum[:8])

	if result.PrivateKeyPath != keyPath {
		t.Fatalf("PrivateKeyPath = %q, want %q", result.PrivateKeyPath, keyPath)
	}
	if result.PublicKeyPath != keyPath+".pub" {
		t.Fatalf("PublicKeyPath = %q, want %q", result.PublicKeyPath, keyPath+".pub")
	}
	if result.SidecarPath != keyPath+".clipfan.json" {
		t.Fatalf("SidecarPath = %q, want sidecar path", result.SidecarPath)
	}
	if result.PublicKey != publicKey {
		t.Fatalf("PublicKey = %q, want %q", result.PublicKey, publicKey)
	}
	if result.KeyID != wantKeyID {
		t.Fatalf("KeyID = %q, want %q", result.KeyID, wantKeyID)
	}
	if result.PublicKeySHA256 != wantDigest {
		t.Fatalf("PublicKeySHA256 = %q, want %q", result.PublicKeySHA256, wantDigest)
	}

	var meta SyncKeyMetadata
	if err := json.Unmarshal(readBytes(t, keyPath+".clipfan.json"), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Schema != "clipfan-sync-key-v1" {
		t.Fatalf("Schema = %q", meta.Schema)
	}
	if meta.HostID != "m4" {
		t.Fatalf("HostID = %q, want m4", meta.HostID)
	}
	if meta.KeyID != wantKeyID {
		t.Fatalf("metadata KeyID = %q, want %q", meta.KeyID, wantKeyID)
	}
	if meta.PublicKeySHA256 != wantDigest {
		t.Fatalf("metadata PublicKeySHA256 = %q, want %q", meta.PublicKeySHA256, wantDigest)
	}
	if meta.PublicKey != publicKey {
		t.Fatalf("metadata PublicKey = %q, want %q", meta.PublicKey, publicKey)
	}
	if meta.CreatedAt != "2026-06-01T12:34:56Z" {
		t.Fatalf("CreatedAt = %q, want RFC3339 UTC", meta.CreatedAt)
	}
}

func TestCreateLocalSyncKeyRefusesExistingMaterialBeforeOverwrite(t *testing.T) {
	for _, tc := range []struct {
		name         string
		existingPath func(string) string
		material     string
	}{
		{
			name:         "private key",
			existingPath: func(keyPath string) string { return keyPath },
			material:     "EXISTING-PRIVATE",
		},
		{
			name:         "public key",
			existingPath: func(keyPath string) string { return keyPath + ".pub" },
			material:     "EXISTING-PUBLIC",
		},
		{
			name:         "sidecar",
			existingPath: func(keyPath string) string { return keyPath + ".clipfan.json" },
			material:     "EXISTING-SIDECAR",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyPath := filepath.Join(syncKeyTempDir(t), "sync_ed25519")
			path := tc.existingPath(keyPath)
			if err := os.WriteFile(path, []byte(tc.material), 0o600); err != nil {
				t.Fatal(err)
			}
			calledGenerator := false

			_, err := CreateLocalSyncKey(SyncKeyCreateOptions{
				KeyPath: keyPath,
				HostID:  "m4",
				Checker: localSyncKeyChecker(),
				Generator: func(SyncKeyGenerateRequest) error {
					calledGenerator = true
					return nil
				},
			})
			if !errors.Is(err, ErrSyncKeyExists) {
				t.Fatalf("error = %v, want ErrSyncKeyExists", err)
			}
			if calledGenerator {
				t.Fatal("generator ran despite existing sync-key material")
			}
			if got := readFile(t, path); got != tc.material {
				t.Fatalf("existing material overwritten: %q", got)
			}
			if strings.Contains(fmt.Sprint(err), tc.material) {
				t.Fatalf("error disclosed existing material: %v", err)
			}
		})
	}
}

func TestCreateLocalSyncKeyRefusesExistingSymlinkMaterialBeforeOverwrite(t *testing.T) {
	keyPath := filepath.Join(syncKeyTempDir(t), "sync_ed25519")
	target := filepath.Join(syncKeyTempDir(t), "target")
	if err := os.WriteFile(target, []byte("EXISTING-TARGET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, keyPath); err != nil {
		t.Fatal(err)
	}
	calledGenerator := false

	_, err := CreateLocalSyncKey(SyncKeyCreateOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
		Generator: func(SyncKeyGenerateRequest) error {
			calledGenerator = true
			return nil
		},
	})
	if !errors.Is(err, ErrSyncKeyExists) {
		t.Fatalf("error = %v, want ErrSyncKeyExists", err)
	}
	if calledGenerator {
		t.Fatal("generator ran despite existing symlink at private key path")
	}
	if got := readFile(t, target); got != "EXISTING-TARGET" {
		t.Fatalf("symlink target overwritten: %q", got)
	}
}

func TestCreateLocalSyncKeyRefusesUnsupportedStorageBeforeKeygen(t *testing.T) {
	keyPath := filepath.Join(syncKeyTempDir(t), "sync_ed25519")
	calledGenerator := false

	_, err := CreateLocalSyncKey(SyncKeyCreateOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: storagecheck.Checker{
			Probe: fakeSyncKeyProbe(storagecheck.Fact{
				FilesystemType: "nfs",
				Local:          boolPtr(false),
				MountPoint:     filepath.Dir(keyPath),
			}),
			Smoke: func(string) error {
				t.Fatal("smoke should not run for unsupported storage")
				return nil
			},
		},
		Generator: func(SyncKeyGenerateRequest) error {
			calledGenerator = true
			return nil
		},
	})
	if !errors.Is(err, storagecheck.ErrUnsupportedRuntimeStorage) {
		t.Fatalf("error = %v, want ErrUnsupportedRuntimeStorage", err)
	}
	if strings.Contains(err.Error(), keyPath) {
		t.Fatalf("error disclosed sync key path: %v", err)
	}
	if calledGenerator {
		t.Fatal("generator ran despite unsupported storage")
	}
	assertNoPath(t, keyPath)
	assertNoPath(t, keyPath+".pub")
	assertNoPath(t, keyPath+".clipfan.json")
}

func TestCreateLocalSyncKeyRefusesInconclusiveStorageBeforeKeygen(t *testing.T) {
	keyPath := filepath.Join(syncKeyTempDir(t), "sync_ed25519")
	calledGenerator := false

	_, err := CreateLocalSyncKey(SyncKeyCreateOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: storagecheck.Checker{
			Probe: fakeSyncKeyProbe(storagecheck.Fact{
				FilesystemType: "",
				MountPoint:     filepath.Dir(keyPath),
			}),
			Smoke: func(string) error {
				t.Fatal("smoke should not run for inconclusive storage")
				return nil
			},
		},
		Generator: func(SyncKeyGenerateRequest) error {
			calledGenerator = true
			return nil
		},
	})
	if !errors.Is(err, storagecheck.ErrStorageCheckInconclusive) {
		t.Fatalf("error = %v, want ErrStorageCheckInconclusive", err)
	}
	if strings.Contains(err.Error(), keyPath) {
		t.Fatalf("error disclosed sync key path: %v", err)
	}
	if calledGenerator {
		t.Fatal("generator ran despite inconclusive storage")
	}
	assertNoPath(t, keyPath)
	assertNoPath(t, keyPath+".pub")
	assertNoPath(t, keyPath+".clipfan.json")
}

func TestCreateLocalSyncKeyRejectsMissingParentDirectory(t *testing.T) {
	keyPath := filepath.Join(syncKeyTempDir(t), "missing", "sync_ed25519")
	calledGenerator := false

	_, err := CreateLocalSyncKey(SyncKeyCreateOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
		Generator: func(SyncKeyGenerateRequest) error {
			calledGenerator = true
			return nil
		},
	})
	if !errors.Is(err, ErrSyncKeyDirectoryMissing) {
		t.Fatalf("error = %v, want ErrSyncKeyDirectoryMissing", err)
	}
	if calledGenerator {
		t.Fatal("generator ran despite missing parent directory")
	}
	if strings.Contains(err.Error(), keyPath) {
		t.Fatalf("error disclosed sync key path: %v", err)
	}
}

func TestCreateLocalSyncKeyRejectsParentPathThatIsFile(t *testing.T) {
	parent := filepath.Join(syncKeyTempDir(t), "not-a-directory")
	if err := os.WriteFile(parent, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(parent, "sync_ed25519")

	_, err := CreateLocalSyncKey(SyncKeyCreateOptions{
		KeyPath:   keyPath,
		HostID:    "m4",
		Checker:   localSyncKeyChecker(),
		Generator: fakeSyncKeyGenerator("PRIVATE-MATERIAL", "ssh-ed25519 "+base64.StdEncoding.EncodeToString([]byte("public"))+" clipfan:m4"),
	})
	if !errors.Is(err, ErrSyncKeyParentNotDir) {
		t.Fatalf("error = %v, want ErrSyncKeyParentNotDir", err)
	}
	if strings.Contains(err.Error(), keyPath) {
		t.Fatalf("error disclosed sync key path: %v", err)
	}
}

func TestCreateLocalSyncKeyRejectsUnsafeParentStat(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can traverse chmod-000 parent")
	}
	blocked := filepath.Join(syncKeyTempDir(t), "blocked")
	child := filepath.Join(blocked, "child")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(blocked, 0o700)
	keyPath := filepath.Join(child, "sync_ed25519")

	_, err := CreateLocalSyncKey(SyncKeyCreateOptions{
		KeyPath:   keyPath,
		HostID:    "m4",
		Checker:   localSyncKeyChecker(),
		Generator: fakeSyncKeyGenerator("PRIVATE-MATERIAL", "ssh-ed25519 "+base64.StdEncoding.EncodeToString([]byte("public"))+" clipfan:m4"),
	})
	if !errors.Is(err, ErrSyncKeyDirectoryUnsafe) {
		t.Fatalf("error = %v, want ErrSyncKeyDirectoryUnsafe", err)
	}
	if strings.Contains(err.Error(), keyPath) {
		t.Fatalf("error disclosed sync key path: %v", err)
	}
}

func TestCreateLocalSyncKeyRejectsSymlinkParentDirectory(t *testing.T) {
	targetDir := syncKeyTempDir(t)
	linkDir := filepath.Join(syncKeyTempDir(t), "sync-parent")
	if err := os.Symlink(targetDir, linkDir); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(linkDir, "sync_ed25519")
	calledGenerator := false

	_, err := CreateLocalSyncKey(SyncKeyCreateOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
		Generator: func(SyncKeyGenerateRequest) error {
			calledGenerator = true
			return nil
		},
	})
	if !errors.Is(err, ErrSyncKeyDirectoryUnsafe) {
		t.Fatalf("error = %v, want ErrSyncKeyDirectoryUnsafe", err)
	}
	if calledGenerator {
		t.Fatal("generator ran despite symlink parent directory")
	}
}

func TestCreateLocalSyncKeyRejectsSymlinkAncestorDirectory(t *testing.T) {
	targetRoot := syncKeyTempDir(t)
	if err := os.MkdirAll(filepath.Join(targetRoot, "clipfan", "ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	linkRoot := filepath.Join(syncKeyTempDir(t), "config-link")
	if err := os.Symlink(targetRoot, linkRoot); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(linkRoot, "clipfan", "ssh", "sync_ed25519")
	calledGenerator := false

	_, err := CreateLocalSyncKey(SyncKeyCreateOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
		Generator: func(SyncKeyGenerateRequest) error {
			calledGenerator = true
			return nil
		},
	})
	if !errors.Is(err, ErrSyncKeyDirectoryUnsafe) {
		t.Fatalf("error = %v, want ErrSyncKeyDirectoryUnsafe", err)
	}
	if calledGenerator {
		t.Fatal("generator ran despite symlink ancestor directory")
	}
}

func TestCreateLocalSyncKeyRejectsInvalidInputs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		keyPath string
		hostID  string
		code    string
	}{
		{name: "relative key path", keyPath: "sync_ed25519", hostID: "m4", code: "invalid_sync_key"},
		{name: "uncanonical key path", keyPath: syncKeyTempDir(t) + "/ssh/../sync_ed25519", hostID: "m4", code: "invalid_sync_key"},
		{name: "missing host id", keyPath: filepath.Join(syncKeyTempDir(t), "sync_ed25519"), hostID: "", code: "invalid_host_id"},
		{name: "invalid host id", keyPath: filepath.Join(syncKeyTempDir(t), "sync_ed25519"), hostID: "bad host", code: "invalid_host_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calledGenerator := false
			_, err := CreateLocalSyncKey(SyncKeyCreateOptions{
				KeyPath: tc.keyPath,
				HostID:  tc.hostID,
				Checker: localSyncKeyChecker(),
				Generator: func(SyncKeyGenerateRequest) error {
					calledGenerator = true
					return nil
				},
			})
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
			if calledGenerator {
				t.Fatal("generator ran for invalid input")
			}
			if tc.code == "invalid_sync_key" && (strings.Contains(err.Error(), "invalid_ssh_path") || strings.Contains(err.Error(), "invalid_path")) {
				t.Fatalf("error = %v, should not include nested path code", err)
			}
			if tc.code == "invalid_host_id" && strings.Count(err.Error(), "invalid_host_id") != 1 {
				t.Fatalf("error = %v, want single invalid_host_id code", err)
			}
		})
	}
}

func TestValidateSyncKeyPathAllowsSpaces(t *testing.T) {
	path := filepath.Join(syncKeyTempDir(t), "Application Support", "clipfan", "ssh", "sync_ed25519")
	if err := ValidateSyncKeyPath(path); err != nil {
		t.Fatalf("ValidateSyncKeyPath(%q) = %v, want nil", path, err)
	}
}

func TestCreateLocalSyncKeyDoesNotExposePrivateMaterialInResult(t *testing.T) {
	keyPath := filepath.Join(syncKeyTempDir(t), "sync_ed25519")
	result, err := CreateLocalSyncKey(SyncKeyCreateOptions{
		KeyPath:   keyPath,
		HostID:    "m4",
		Checker:   localSyncKeyChecker(),
		Generator: fakeSyncKeyGenerator("PRIVATE-MATERIAL", "ssh-ed25519 "+base64.StdEncoding.EncodeToString([]byte("public"))+" clipfan:m4"),
	})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(fmt.Sprintf("%+v", result), "PRIVATE-MATERIAL") {
		t.Fatalf("result disclosed private key material: %+v", result)
	}
}

func TestCreateLocalSyncKeyCleansGeneratedMaterialOnPostGenerationFailure(t *testing.T) {
	keyPath := filepath.Join(syncKeyTempDir(t), "sync_ed25519")
	_, err := CreateLocalSyncKey(SyncKeyCreateOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
		Generator: func(req SyncKeyGenerateRequest) error {
			if err := os.WriteFile(req.KeyPath, []byte("PRIVATE-MATERIAL"), 0o600); err != nil {
				return err
			}
			return os.WriteFile(req.PublicKeyPath, []byte("not-an-ed25519-key\n"), 0o644)
		},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid_sync_key_public_key") {
		t.Fatalf("error = %v, want invalid public key", err)
	}
	assertNoPath(t, keyPath)
	assertNoPath(t, keyPath+".pub")
	assertNoPath(t, keyPath+".clipfan.json")
	if strings.Contains(fmt.Sprint(err), "PRIVATE-MATERIAL") {
		t.Fatalf("error disclosed private key material: %v", err)
	}
}

func TestCreateLocalSyncKeyCleansGeneratedMaterialOnGenerationFailure(t *testing.T) {
	keyPath := filepath.Join(syncKeyTempDir(t), "sync_ed25519")
	_, err := CreateLocalSyncKey(SyncKeyCreateOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
		Generator: func(req SyncKeyGenerateRequest) error {
			if err := os.WriteFile(req.KeyPath, []byte("PRIVATE-MATERIAL"), 0o600); err != nil {
				return err
			}
			return fmt.Errorf("PRIVATE-MATERIAL")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "sync_key_generation_failed") {
		t.Fatalf("error = %v, want generation failure", err)
	}
	assertNoPath(t, keyPath)
	assertNoPath(t, keyPath+".pub")
	assertNoPath(t, keyPath+".clipfan.json")
	if strings.Contains(fmt.Sprint(err), "PRIVATE-MATERIAL") {
		t.Fatalf("error disclosed private key material: %v", err)
	}
}

func TestCreateLocalSyncKeyFailsPathFreeWhenGeneratedPublicKeyMissing(t *testing.T) {
	keyPath := filepath.Join(syncKeyTempDir(t), "sync_ed25519")
	_, err := CreateLocalSyncKey(SyncKeyCreateOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
		Generator: func(req SyncKeyGenerateRequest) error {
			return os.WriteFile(req.KeyPath, []byte("PRIVATE-MATERIAL"), 0o600)
		},
	})
	if !errors.Is(err, ErrSyncKeyReadFailed) {
		t.Fatalf("error = %v, want ErrSyncKeyReadFailed", err)
	}
	assertNoPath(t, keyPath)
	assertNoPath(t, keyPath+".pub")
	assertNoPath(t, keyPath+".clipfan.json")
	if strings.Contains(err.Error(), keyPath) || strings.Contains(fmt.Sprint(err), "PRIVATE-MATERIAL") {
		t.Fatalf("error disclosed sync key path or private material: %v", err)
	}
}

func TestCreateLocalSyncKeyRefusesSymlinkPrivateKeyAfterGeneration(t *testing.T) {
	keyPath := filepath.Join(syncKeyTempDir(t), "sync_ed25519")
	target := filepath.Join(syncKeyTempDir(t), "target-private")
	if err := os.WriteFile(target, []byte("TARGET"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := CreateLocalSyncKey(SyncKeyCreateOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
		Generator: func(req SyncKeyGenerateRequest) error {
			return os.Symlink(target, req.KeyPath)
		},
	})
	if !errors.Is(err, ErrSyncKeyChmodFailed) {
		t.Fatalf("error = %v, want ErrSyncKeyChmodFailed", err)
	}
	assertNoPath(t, keyPath)
	if got := readFile(t, target); got != "TARGET" {
		t.Fatalf("symlink target changed: %q", got)
	}
	if strings.Contains(err.Error(), keyPath) || strings.Contains(err.Error(), target) {
		t.Fatalf("error disclosed sync key path: %v", err)
	}
}

func TestCreateLocalSyncKeyRefusesSymlinkPublicKeyAfterGeneration(t *testing.T) {
	keyPath := filepath.Join(syncKeyTempDir(t), "sync_ed25519")
	publicTarget := filepath.Join(syncKeyTempDir(t), "target.pub")
	publicKey := "ssh-ed25519 " + base64.StdEncoding.EncodeToString([]byte("public")) + " clipfan:m4"
	if err := os.WriteFile(publicTarget, []byte(publicKey+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := CreateLocalSyncKey(SyncKeyCreateOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
		Generator: func(req SyncKeyGenerateRequest) error {
			if err := os.WriteFile(req.KeyPath, []byte("PRIVATE-MATERIAL"), 0o600); err != nil {
				return err
			}
			return os.Symlink(publicTarget, req.PublicKeyPath)
		},
	})
	if !errors.Is(err, ErrSyncKeyReadFailed) {
		t.Fatalf("error = %v, want ErrSyncKeyReadFailed", err)
	}
	assertNoPath(t, keyPath)
	assertNoPath(t, keyPath+".pub")
	assertNoPath(t, keyPath+".clipfan.json")
	if got := readFile(t, publicTarget); got != publicKey+"\n" {
		t.Fatalf("symlink target changed: %q", got)
	}
	if strings.Contains(err.Error(), keyPath) || strings.Contains(err.Error(), publicTarget) {
		t.Fatalf("error disclosed sync key path: %v", err)
	}
}

func TestCreateLocalSyncKeyFailsPathFreeWhenSidecarWriteFails(t *testing.T) {
	keyPath := filepath.Join(syncKeyTempDir(t), "sync_ed25519")
	publicKey := "ssh-ed25519 " + base64.StdEncoding.EncodeToString([]byte("public")) + " clipfan:m4"
	_, err := CreateLocalSyncKey(SyncKeyCreateOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
		Generator: func(req SyncKeyGenerateRequest) error {
			if err := os.WriteFile(req.KeyPath, []byte("PRIVATE-MATERIAL"), 0o600); err != nil {
				return err
			}
			if err := os.WriteFile(req.PublicKeyPath, []byte(publicKey+"\n"), 0o644); err != nil {
				return err
			}
			return os.Mkdir(req.KeyPath+".clipfan.json", 0o700)
		},
	})
	if !errors.Is(err, ErrSyncKeyWriteFailed) {
		t.Fatalf("error = %v, want ErrSyncKeyWriteFailed", err)
	}
	assertNoPath(t, keyPath)
	assertNoPath(t, keyPath+".pub")
	assertNoPath(t, keyPath+".clipfan.json")
	if strings.Contains(err.Error(), keyPath) || strings.Contains(fmt.Sprint(err), "PRIVATE-MATERIAL") {
		t.Fatalf("error disclosed sync key path or private material: %v", err)
	}
}

func TestCreateLocalSyncKeyDefaultGeneratorFailureIsTyped(t *testing.T) {
	keyPath := filepath.Join(syncKeyTempDir(t), "sync_ed25519")
	t.Setenv("PATH", filepath.Join(syncKeyTempDir(t), "empty-path"))

	_, err := CreateLocalSyncKey(SyncKeyCreateOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
	})
	if !errors.Is(err, ErrSyncKeyGenerationFailed) {
		t.Fatalf("error = %v, want ErrSyncKeyGenerationFailed", err)
	}
	if strings.Contains(err.Error(), keyPath) {
		t.Fatalf("error disclosed sync key path: %v", err)
	}
}

func TestCreateLocalSyncKeyRejectsSymlinkCreateLock(t *testing.T) {
	keyPath := filepath.Join(syncKeyTempDir(t), "sync_ed25519")
	lockTarget := filepath.Join(syncKeyTempDir(t), "lock-target")
	if err := os.Symlink(lockTarget, keyPath+".create.lock"); err != nil {
		t.Fatal(err)
	}
	calledGenerator := false

	_, err := CreateLocalSyncKey(SyncKeyCreateOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
		Generator: func(SyncKeyGenerateRequest) error {
			calledGenerator = true
			return nil
		},
	})
	if !errors.Is(err, ErrSyncKeyLockUnsafe) {
		t.Fatalf("error = %v, want ErrSyncKeyLockUnsafe", err)
	}
	if calledGenerator {
		t.Fatal("generator ran despite symlink create lock")
	}
	if strings.Contains(err.Error(), keyPath) || strings.Contains(err.Error(), lockTarget) {
		t.Fatalf("error disclosed sync key path: %v", err)
	}
}

func TestCreateLocalSyncKeySerializesConcurrentCreate(t *testing.T) {
	keyPath := filepath.Join(syncKeyTempDir(t), "sync_ed25519")
	publicKey := "ssh-ed25519 " + base64.StdEncoding.EncodeToString([]byte("public")) + " clipfan:m4"
	firstGeneratorEntered := make(chan struct{})
	releaseFirstGenerator := make(chan struct{})
	firstDone := make(chan error, 1)

	go func() {
		_, err := CreateLocalSyncKey(SyncKeyCreateOptions{
			KeyPath: keyPath,
			HostID:  "m4",
			Checker: localSyncKeyChecker(),
			Generator: func(req SyncKeyGenerateRequest) error {
				close(firstGeneratorEntered)
				<-releaseFirstGenerator
				if err := os.WriteFile(req.KeyPath, []byte("FIRST-PRIVATE"), 0o644); err != nil {
					return err
				}
				return os.WriteFile(req.PublicKeyPath, []byte(publicKey+"\n"), 0o644)
			},
		})
		firstDone <- err
	}()
	<-firstGeneratorEntered

	var secondGeneratorCalled atomic.Bool
	secondDone := make(chan error, 1)
	secondPreflightDone := make(chan struct{})
	secondChecker := localSyncKeyChecker()
	secondChecker.Smoke = func(string) error {
		close(secondPreflightDone)
		return nil
	}
	go func() {
		_, err := CreateLocalSyncKey(SyncKeyCreateOptions{
			KeyPath: keyPath,
			HostID:  "m4",
			Checker: secondChecker,
			Generator: func(SyncKeyGenerateRequest) error {
				secondGeneratorCalled.Store(true)
				return nil
			},
		})
		secondDone <- err
	}()

	<-secondPreflightDone
	select {
	case err := <-secondDone:
		t.Fatalf("second CreateLocalSyncKey completed before first released lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseFirstGenerator)
	if err := <-firstDone; err != nil {
		t.Fatalf("first CreateLocalSyncKey error = %v", err)
	}
	err := <-secondDone
	if !errors.Is(err, ErrSyncKeyExists) {
		t.Fatalf("second CreateLocalSyncKey error = %v, want ErrSyncKeyExists", err)
	}
	if secondGeneratorCalled.Load() {
		t.Fatal("second generator ran despite serialized existing key check")
	}
	if got := readFile(t, keyPath); got != "FIRST-PRIVATE" {
		t.Fatalf("first private key was removed or overwritten: %q", got)
	}
}

func TestCreateLocalSyncKeyWithSSHKeygenIntegration(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen unavailable")
	}
	keyPath := filepath.Join(syncKeyTempDir(t), "sync_ed25519")

	result, err := CreateLocalSyncKey(SyncKeyCreateOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
		Now:     func() time.Time { return time.Date(2026, 6, 1, 12, 34, 56, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := fileMode(t, keyPath); got != 0o600 {
		t.Fatalf("private key mode = %#o, want 0600", got)
	}

	publicKey, publicBlob, err := readOpenSSHPublicKey(keyPath + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(publicBlob)
	wantDigest := "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
	wantKeyID := hex.EncodeToString(sum[:8])

	if result.PublicKey != publicKey {
		t.Fatalf("PublicKey = %q, want generated public key", result.PublicKey)
	}
	if result.KeyID != wantKeyID {
		t.Fatalf("KeyID = %q, want %q", result.KeyID, wantKeyID)
	}
	if result.PublicKeySHA256 != wantDigest {
		t.Fatalf("PublicKeySHA256 = %q, want %q", result.PublicKeySHA256, wantDigest)
	}
	fingerprintOutput, err := exec.Command("ssh-keygen", "-l", "-E", "sha256", "-f", keyPath+".pub").Output()
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(fingerprintOutput))
	if len(fields) < 2 {
		t.Fatalf("ssh-keygen fingerprint output = %q", string(fingerprintOutput))
	}
	if fields[1] != result.PublicKeySHA256 {
		t.Fatalf("PublicKeySHA256 = %q, OpenSSH fingerprint = %q", result.PublicKeySHA256, fields[1])
	}

	var meta SyncKeyMetadata
	if err := json.Unmarshal(readBytes(t, keyPath+".clipfan.json"), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.PublicKey != publicKey || meta.KeyID != wantKeyID || meta.PublicKeySHA256 != wantDigest {
		t.Fatalf("metadata = %#v, want generated public key metadata", meta)
	}
}

func TestLoadLocalSyncKeyReturnsExistingIdentity(t *testing.T) {
	keyPath, created := createSyncKeyFixture(t)

	loaded, err := LoadLocalSyncKey(SyncKeyLoadOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PrivateKeyPath != created.PrivateKeyPath ||
		loaded.PublicKeyPath != created.PublicKeyPath ||
		loaded.SidecarPath != created.SidecarPath ||
		loaded.KeyID != created.KeyID ||
		loaded.PublicKeySHA256 != created.PublicKeySHA256 ||
		loaded.PublicKey != created.PublicKey {
		t.Fatalf("loaded = %#v, want created identity %#v", loaded, created)
	}
	if loaded.Metadata != created.Metadata {
		t.Fatalf("metadata = %#v, want %#v", loaded.Metadata, created.Metadata)
	}
	if strings.Contains(fmt.Sprintf("%+v", loaded), "PRIVATE-MATERIAL") {
		t.Fatalf("loaded result disclosed private material: %+v", loaded)
	}
}

func TestLoadLocalSyncKeyAcceptsOpenSSHPublicKeyMode0644(t *testing.T) {
	keyPath, _ := createSyncKeyFixture(t)
	if err := os.Chmod(keyPath+".pub", 0o644); err != nil {
		t.Fatal(err)
	}
	if got := fileMode(t, keyPath+".pub"); got != 0o644 {
		t.Fatalf("public key mode = %#o, fixture should model OpenSSH 0644 public key", got)
	}

	if _, err := LoadLocalSyncKey(SyncKeyLoadOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLoadLocalSyncKeyReturnsMissingSyncKeyForMissingMaterial(t *testing.T) {
	for _, tc := range []struct {
		name string
		path func(string) string
	}{
		{name: "private key", path: func(keyPath string) string { return keyPath }},
		{name: "public key", path: func(keyPath string) string { return keyPath + ".pub" }},
		{name: "sidecar", path: func(keyPath string) string { return keyPath + ".clipfan.json" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyPath, _ := createSyncKeyFixture(t)
			if err := os.Remove(tc.path(keyPath)); err != nil {
				t.Fatal(err)
			}

			_, err := LoadLocalSyncKey(SyncKeyLoadOptions{
				KeyPath: keyPath,
				HostID:  "m4",
				Checker: localSyncKeyChecker(),
			})
			if !errors.Is(err, ErrMissingSyncKey) {
				t.Fatalf("error = %v, want ErrMissingSyncKey", err)
			}
		})
	}
}

func TestLoadLocalSyncKeyRefusesUnsupportedStorageBeforeReadingMaterial(t *testing.T) {
	keyPath, _ := createSyncKeyFixture(t)

	_, err := LoadLocalSyncKey(SyncKeyLoadOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: storagecheck.Checker{
			Probe: fakeSyncKeyProbe(storagecheck.Fact{
				FilesystemType: "nfs",
				Local:          boolPtr(false),
				MountPoint:     filepath.Dir(keyPath),
			}),
			Smoke: func(string) error {
				t.Fatal("smoke should not run for unsupported storage")
				return nil
			},
		},
	})
	if !errors.Is(err, storagecheck.ErrUnsupportedRuntimeStorage) {
		t.Fatalf("error = %v, want ErrUnsupportedRuntimeStorage", err)
	}
	if strings.Contains(err.Error(), keyPath) {
		t.Fatalf("error disclosed sync key path: %v", err)
	}
}

func TestLoadLocalSyncKeyRejectsSidecarIdentityMismatches(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(SyncKeyMetadata) SyncKeyMetadata
	}{
		{name: "wrong host id", mutate: func(meta SyncKeyMetadata) SyncKeyMetadata {
			meta.HostID = "other"
			return meta
		}},
		{name: "digest mismatch", mutate: func(meta SyncKeyMetadata) SyncKeyMetadata {
			meta.PublicKeySHA256 = "SHA256:wrong"
			return meta
		}},
		{name: "key id mismatch", mutate: func(meta SyncKeyMetadata) SyncKeyMetadata {
			meta.KeyID = "0000000000000000"
			return meta
		}},
		{name: "public key mismatch", mutate: func(meta SyncKeyMetadata) SyncKeyMetadata {
			meta.PublicKey = "ssh-ed25519 " + base64.StdEncoding.EncodeToString([]byte("other")) + " clipfan:m4"
			return meta
		}},
		{name: "stale schema", mutate: func(meta SyncKeyMetadata) SyncKeyMetadata {
			meta.Schema = "clipfan-sync-key-v0"
			return meta
		}},
		{name: "missing created_at", mutate: func(meta SyncKeyMetadata) SyncKeyMetadata {
			meta.CreatedAt = ""
			return meta
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyPath, created := createSyncKeyFixture(t)
			writeSyncKeyMetadataForTest(t, keyPath+".clipfan.json", tc.mutate(created.Metadata))

			_, err := LoadLocalSyncKey(SyncKeyLoadOptions{
				KeyPath: keyPath,
				HostID:  "m4",
				Checker: localSyncKeyChecker(),
			})
			if !errors.Is(err, ErrSyncKeyIdentityMismatch) {
				t.Fatalf("error = %v, want ErrSyncKeyIdentityMismatch", err)
			}
		})
	}
}

func TestLoadLocalSyncKeyRejectsInvalidPublicKeyMaterial(t *testing.T) {
	keyPath, _ := createSyncKeyFixture(t)
	if err := os.WriteFile(keyPath+".pub", []byte("not-an-ed25519-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadLocalSyncKey(SyncKeyLoadOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
	})
	if !errors.Is(err, ErrSyncKeyIdentityMismatch) {
		t.Fatalf("error = %v, want ErrSyncKeyIdentityMismatch", err)
	}
	if strings.Contains(err.Error(), keyPath) || strings.Contains(err.Error(), "PRIVATE-MATERIAL") {
		t.Fatalf("error disclosed sync key path or private material: %v", err)
	}
}

func TestLoadLocalSyncKeyRejectsMalformedOrUnknownSidecar(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "malformed json", body: `{`},
		{name: "non object", body: `[]`},
		{name: "missing schema", body: `{"host_id":"m4","key_id":"abc","public_key_sha256":"SHA256:x","public_key":"ssh-ed25519 AAAA","created_at":"2026-06-01T12:34:56Z"}`},
		{name: "unknown field", body: `{"schema":"clipfan-sync-key-v1","host_id":"m4","key_id":"abc","public_key_sha256":"SHA256:x","public_key":"ssh-ed25519 AAAA","created_at":"2026-06-01T12:34:56Z","future":true}`},
		{name: "invalid created_at", body: `{"schema":"clipfan-sync-key-v1","host_id":"m4","key_id":"abc","public_key_sha256":"SHA256:x","public_key":"ssh-ed25519 AAAA","created_at":"not-time"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyPath, _ := createSyncKeyFixture(t)
			if err := os.WriteFile(keyPath+".clipfan.json", []byte(tc.body+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := LoadLocalSyncKey(SyncKeyLoadOptions{
				KeyPath: keyPath,
				HostID:  "m4",
				Checker: localSyncKeyChecker(),
			})
			if !errors.Is(err, ErrSyncKeyIdentityMismatch) {
				t.Fatalf("error = %v, want ErrSyncKeyIdentityMismatch", err)
			}
		})
	}
}

func TestLoadLocalSyncKeyRepairsPrivateSidecarReadModes(t *testing.T) {
	keyPath, _ := createSyncKeyFixture(t)
	if err := os.Chmod(keyPath, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyPath+".clipfan.json", 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadLocalSyncKey(SyncKeyLoadOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
	}); err != nil {
		t.Fatal(err)
	}
	if got := fileMode(t, keyPath); got != 0o600 {
		t.Fatalf("private key mode = %#o, want repaired 0600", got)
	}
	if got := fileMode(t, keyPath+".clipfan.json"); got != 0o600 {
		t.Fatalf("sidecar mode = %#o, want repaired 0600", got)
	}
}

func TestLoadLocalSyncKeyRejectsGroupOrWorldWritableMaterial(t *testing.T) {
	for _, tc := range []struct {
		name string
		path func(string) string
		mode os.FileMode
	}{
		{name: "private group writable", path: func(keyPath string) string { return keyPath }, mode: 0o620},
		{name: "public group writable", path: func(keyPath string) string { return keyPath + ".pub" }, mode: 0o664},
		{name: "sidecar world writable", path: func(keyPath string) string { return keyPath + ".clipfan.json" }, mode: 0o602},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyPath, _ := createSyncKeyFixture(t)
			if err := os.Chmod(tc.path(keyPath), tc.mode); err != nil {
				t.Fatal(err)
			}

			_, err := LoadLocalSyncKey(SyncKeyLoadOptions{
				KeyPath: keyPath,
				HostID:  "m4",
				Checker: localSyncKeyChecker(),
			})
			if !errors.Is(err, ErrSyncKeyIdentityMismatch) {
				t.Fatalf("error = %v, want ErrSyncKeyIdentityMismatch", err)
			}
		})
	}
}

func TestLoadLocalSyncKeyRejectsSymlinkMaterial(t *testing.T) {
	for _, tc := range []struct {
		name string
		path func(string) string
		body string
	}{
		{name: "private", path: func(keyPath string) string { return keyPath }, body: "PRIVATE-MATERIAL"},
		{name: "public", path: func(keyPath string) string { return keyPath + ".pub" }, body: "ssh-ed25519 " + base64.StdEncoding.EncodeToString([]byte("public")) + " clipfan:m4\n"},
		{name: "sidecar", path: func(keyPath string) string { return keyPath + ".clipfan.json" }, body: `{"schema":"clipfan-sync-key-v1"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyPath, _ := createSyncKeyFixture(t)
			path := tc.path(keyPath)
			target := filepath.Join(syncKeyTempDir(t), "target")
			if err := os.WriteFile(target, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}

			_, err := LoadLocalSyncKey(SyncKeyLoadOptions{
				KeyPath: keyPath,
				HostID:  "m4",
				Checker: localSyncKeyChecker(),
			})
			if !errors.Is(err, ErrSyncKeyIdentityMismatch) {
				t.Fatalf("error = %v, want ErrSyncKeyIdentityMismatch", err)
			}
			if strings.Contains(err.Error(), keyPath) || strings.Contains(err.Error(), target) {
				t.Fatalf("error disclosed sync key path: %v", err)
			}
		})
	}
}

func TestLoadLocalSyncKeyRejectsHardLinkedMaterial(t *testing.T) {
	for _, tc := range []struct {
		name string
		path func(string) string
	}{
		{name: "private", path: func(keyPath string) string { return keyPath }},
		{name: "public", path: func(keyPath string) string { return keyPath + ".pub" }},
		{name: "sidecar", path: func(keyPath string) string { return keyPath + ".clipfan.json" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyPath, _ := createSyncKeyFixture(t)
			if err := os.Link(tc.path(keyPath), tc.path(keyPath)+".hardlink"); err != nil {
				t.Skipf("hard links unavailable: %v", err)
			}

			_, err := LoadLocalSyncKey(SyncKeyLoadOptions{
				KeyPath: keyPath,
				HostID:  "m4",
				Checker: localSyncKeyChecker(),
			})
			if !errors.Is(err, ErrSyncKeyIdentityMismatch) {
				t.Fatalf("error = %v, want ErrSyncKeyIdentityMismatch", err)
			}
		})
	}
}

func TestLoadLocalSyncKeyDoesNotCreateOverwriteOrChmodMismatchedMaterial(t *testing.T) {
	keyPath, created := createSyncKeyFixture(t)
	writeSyncKeyMetadataForTest(t, keyPath+".clipfan.json", SyncKeyMetadata{
		Schema:          created.Metadata.Schema,
		HostID:          "other",
		KeyID:           created.Metadata.KeyID,
		PublicKeySHA256: created.Metadata.PublicKeySHA256,
		PublicKey:       created.Metadata.PublicKey,
		CreatedAt:       created.Metadata.CreatedAt,
	})
	if err := os.Chmod(keyPath+".pub", 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyPath+".clipfan.json", 0o644); err != nil {
		t.Fatal(err)
	}
	privateBefore := readFile(t, keyPath)

	_, err := LoadLocalSyncKey(SyncKeyLoadOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
	})
	if !errors.Is(err, ErrSyncKeyIdentityMismatch) {
		t.Fatalf("error = %v, want ErrSyncKeyIdentityMismatch", err)
	}
	if strings.Contains(err.Error(), "PRIVATE-MATERIAL") {
		t.Fatalf("error disclosed private material: %v", err)
	}
	if got := readFile(t, keyPath); got != privateBefore {
		t.Fatalf("private key changed: %q", got)
	}
	if got := fileMode(t, keyPath+".pub"); got != 0o644 {
		t.Fatalf("public key mode = %#o, want unchanged 0644", got)
	}
	if got := fileMode(t, keyPath+".clipfan.json"); got != 0o644 {
		t.Fatalf("sidecar mode = %#o, want unchanged mismatched sidecar mode 0644", got)
	}
}

func TestResetLocalSyncKeyRegeneratesMismatchedMaterial(t *testing.T) {
	keyPath, created := createSyncKeyFixture(t)
	writeSyncKeyMetadataForTest(t, keyPath+".clipfan.json", SyncKeyMetadata{
		Schema:          created.Metadata.Schema,
		HostID:          "old-host",
		KeyID:           created.Metadata.KeyID,
		PublicKeySHA256: created.Metadata.PublicKeySHA256,
		PublicKey:       created.Metadata.PublicKey,
		CreatedAt:       created.Metadata.CreatedAt,
	})
	oldPrivate := readFile(t, keyPath)
	newPublicBlob := []byte("reset public")
	newPublicKey := "ssh-ed25519 " + base64.StdEncoding.EncodeToString(newPublicBlob) + " clipfan:m4"

	result, err := ResetLocalSyncKey(SyncKeyResetOptions{
		KeyPath:   keyPath,
		HostID:    "m4",
		Checker:   localSyncKeyChecker(),
		Now:       func() time.Time { return time.Date(2026, 6, 3, 9, 10, 11, 0, time.UTC) },
		Generator: fakeSyncKeyGenerator("NEW-PRIVATE-MATERIAL", newPublicKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.PrivateKeyPath != keyPath || result.PublicKeyPath != keyPath+".pub" || result.SidecarPath != keyPath+".clipfan.json" {
		t.Fatalf("result paths = %#v, want final key paths", result)
	}
	if got := readFile(t, keyPath); got != "NEW-PRIVATE-MATERIAL" {
		t.Fatalf("private key = %q, want regenerated material", got)
	}
	if strings.Contains(fmt.Sprintf("%+v", result), oldPrivate) || strings.Contains(fmt.Sprintf("%+v", result), "NEW-PRIVATE-MATERIAL") {
		t.Fatalf("reset result disclosed private material: %+v", result)
	}

	loaded, err := LoadLocalSyncKey(SyncKeyLoadOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PublicKey != newPublicKey || loaded.Metadata.HostID != "m4" || loaded.Metadata.CreatedAt != "2026-06-03T09:10:11Z" {
		t.Fatalf("loaded regenerated key = %#v", loaded)
	}
}

func TestResetLocalSyncKeyWithSSHKeygenIntegration(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen unavailable")
	}
	keyPath, created := createSyncKeyFixture(t)
	result, err := ResetLocalSyncKey(SyncKeyResetOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
		Now:     func() time.Time { return time.Date(2026, 6, 3, 9, 10, 11, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.PrivateKeyPath != keyPath || result.PublicKeyPath != keyPath+".pub" || result.SidecarPath != keyPath+".clipfan.json" {
		t.Fatalf("result paths = %#v, want final key paths", result)
	}
	if result.PublicKey == created.PublicKey || result.KeyID == created.KeyID {
		t.Fatalf("reset reused original public identity: reset=%#v created=%#v", result, created)
	}
	if got := fileMode(t, keyPath); got != 0o600 {
		t.Fatalf("private key mode = %#o, want 0600", got)
	}
	loaded, err := LoadLocalSyncKey(SyncKeyLoadOptions{
		KeyPath: keyPath,
		HostID:  "m4",
		Checker: localSyncKeyChecker(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PublicKey != result.PublicKey || loaded.KeyID != result.KeyID || loaded.Metadata.CreatedAt != "2026-06-03T09:10:11Z" {
		t.Fatalf("loaded reset key = %#v, want %#v", loaded, result)
	}
	if strings.Contains(fmt.Sprintf("%+v", result), "PRIVATE-MATERIAL") || strings.Contains(fmt.Sprintf("%+v", loaded), "PRIVATE-MATERIAL") {
		t.Fatalf("reset exposed old private material: result=%+v loaded=%+v", result, loaded)
	}
}

func TestResetLocalSyncKeySweepsStaleScratch(t *testing.T) {
	keyPath, _ := createSyncKeyFixture(t)
	base := filepath.Base(keyPath)
	dir := filepath.Dir(keyPath)
	stalePaths := []string{
		filepath.Join(dir, "."+base+".reset.123.456"),
		filepath.Join(dir, "."+base+".reset.123.456.pub"),
		filepath.Join(dir, "."+base+".reset.123.456.clipfan.json"),
		filepath.Join(dir, base+".reset-old.123.456"),
		filepath.Join(dir, base+".pub.reset-old.123.456"),
		filepath.Join(dir, base+".clipfan.json.reset-old.123.456"),
	}
	for _, path := range stalePaths {
		if err := os.WriteFile(path, []byte("STALE-PRIVATE-MATERIAL"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keptPath := filepath.Join(dir, base+".reset-old.not-a-counter")
	if err := os.WriteFile(keptPath, []byte("not clipfan scratch"), 0o600); err != nil {
		t.Fatal(err)
	}
	newPublicKey := "ssh-ed25519 " + base64.StdEncoding.EncodeToString([]byte("new public")) + " clipfan:m4"

	_, err := ResetLocalSyncKey(SyncKeyResetOptions{
		KeyPath:   keyPath,
		HostID:    "m4",
		Checker:   localSyncKeyChecker(),
		Generator: fakeSyncKeyGenerator("NEW-PRIVATE-MATERIAL", newPublicKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range stalePaths {
		assertNoPath(t, path)
	}
	if got := readFile(t, keptPath); got != "not clipfan scratch" {
		t.Fatalf("non-scratch file changed: %q", got)
	}
	assertNoSyncKeyResetScratch(t, keyPath)
}

func TestLoadLocalSyncKeyRejectsSidecarBoundToPreviousHostIdentity(t *testing.T) {
	keyPath, created := createSyncKeyFixture(t)
	if err := os.Chmod(keyPath+".clipfan.json", 0o644); err != nil {
		t.Fatal(err)
	}
	privateBefore := readFile(t, keyPath)

	_, err := LoadLocalSyncKey(SyncKeyLoadOptions{
		KeyPath: keyPath,
		HostID:  "renamed-host",
		Checker: localSyncKeyChecker(),
	})
	if !errors.Is(err, ErrSyncKeyIdentityMismatch) {
		t.Fatalf("error = %v, want ErrSyncKeyIdentityMismatch", err)
	}
	if strings.Contains(err.Error(), privateBefore) {
		t.Fatalf("error disclosed private material: %v", err)
	}
	if got := readFile(t, keyPath); got != privateBefore {
		t.Fatalf("private key changed from %q to %q", privateBefore, got)
	}
	if got := fileMode(t, keyPath+".clipfan.json"); got != 0o644 {
		t.Fatalf("stale sidecar mode = %#o, want unchanged 0644", got)
	}
	if created.Metadata.HostID != "m4" {
		t.Fatalf("fixture HostID = %q, want old host m4", created.Metadata.HostID)
	}
}

func TestSyncKeygenArgs(t *testing.T) {
	got := syncKeygenArgs(SyncKeyGenerateRequest{
		KeyPath: "/Users/jesse/.config/clipfan/ssh/sync_ed25519",
		HostID:  "m4",
	})
	want := []string{"-t", "ed25519", "-N", "", "-f", "/Users/jesse/.config/clipfan/ssh/sync_ed25519", "-C", "clipfan:m4"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("syncKeygenArgs = %#v, want %#v", got, want)
	}
}

func TestSyncKeyCommandErrorIncludesSafeDiagnostics(t *testing.T) {
	err := syncKeyCommandError{err: errors.New("exit status 1"), stderr: "ssh-keygen: bad path\n"}
	if !errors.Is(err, ErrSyncKeyGenerationFailed) {
		t.Fatalf("errors.Is(err, ErrSyncKeyGenerationFailed) = false")
	}
	if got := err.Error(); !strings.Contains(got, "sync_key_generation_failed") || !strings.Contains(got, "ssh-keygen: bad path") {
		t.Fatalf("error = %q, want code and stderr diagnostic", got)
	}
}

func localSyncKeyChecker() storagecheck.Checker {
	return storagecheck.Checker{
		Probe: fakeSyncKeyProbe(storagecheck.Fact{
			FilesystemType: "apfs",
			Local:          boolPtr(true),
			MountPoint:     "/tmp",
		}),
		Smoke: func(string) error { return nil },
	}
}

func fakeSyncKeyGenerator(privateKey string, publicKey string) SyncKeyGenerator {
	return func(req SyncKeyGenerateRequest) error {
		if err := os.WriteFile(req.KeyPath, []byte(privateKey), 0o644); err != nil {
			return err
		}
		return os.WriteFile(req.PublicKeyPath, []byte(publicKey+"\n"), 0o644)
	}
}

func fakeSyncKeyProbe(fact storagecheck.Fact) storagecheck.ProbeFunc {
	return func(string) (storagecheck.Fact, error) {
		return fact, nil
	}
}

func TestSyncKeyCommandErrorRedactsKeyPath(t *testing.T) {
	keyPath := "/Users/jesse/.config/clipfan/ssh/sync_ed25519"
	keyDir := filepath.Dir(keyPath)
	err := syncKeyCommandError{
		err:     errors.New("Saving key \"" + keyPath + "\" failed"),
		stderr:  "Saving key \"" + keyPath + "\" failed in " + keyDir + ": permission denied\n",
		keyPath: keyPath,
	}
	got := err.Error()
	if strings.Contains(got, keyPath) || strings.Contains(got, keyDir) {
		t.Fatalf("error disclosed sync key path: %q", got)
	}
	if !strings.Contains(got, "<sync_key_path>") {
		t.Fatalf("error = %q, want redacted key path placeholder", got)
	}
	if !strings.Contains(got, "<sync_key_dir>") {
		t.Fatalf("error = %q, want redacted key directory placeholder", got)
	}
}

func createSyncKeyFixture(t *testing.T) (string, SyncKeyCreateResult) {
	t.Helper()
	keyPath := filepath.Join(syncKeyTempDir(t), "sync_ed25519")
	publicKey := "ssh-ed25519 " + base64.StdEncoding.EncodeToString([]byte("public")) + " clipfan:m4"
	result, err := CreateLocalSyncKey(SyncKeyCreateOptions{
		KeyPath:   keyPath,
		HostID:    "m4",
		Checker:   localSyncKeyChecker(),
		Now:       func() time.Time { return time.Date(2026, 6, 1, 12, 34, 56, 0, time.UTC) },
		Generator: fakeSyncKeyGenerator("PRIVATE-MATERIAL", publicKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	return keyPath, result
}

func writeSyncKeyMetadataForTest(t *testing.T, path string, meta SyncKeyMetadata) {
	t.Helper()
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	return string(readBytes(t, path))
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func assertNoPath(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Lstat(%s) err = %v, want not exist", path, err)
	}
}

func assertNoSyncKeyResetScratch(t *testing.T, keyPath string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(keyPath))
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(keyPath)
	for _, entry := range entries {
		if isSyncKeyResetScratchName(base, entry.Name()) {
			t.Fatalf("found stale sync key reset scratch file %s", entry.Name())
		}
	}
}

func syncKeyTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
