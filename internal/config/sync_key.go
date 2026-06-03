package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/storagecheck"
)

const syncKeyMetadataSchema = "clipfan-sync-key-v1"

var (
	ErrSyncKeyExists           = errors.New("sync_key_exists")
	ErrSyncKeyGenerationFailed = errors.New("sync_key_generation_failed")
	ErrInvalidSyncKeyPublicKey = errors.New("invalid_sync_key_public_key")
	ErrSyncKeyDirectoryMissing = errors.New("missing_sync_key_directory")
	ErrSyncKeyDirectoryUnsafe  = errors.New("sync_key_directory_unsafe")
	ErrSyncKeyParentNotDir     = errors.New("sync_key_parent_not_directory")
	ErrSyncKeyLockUnavailable  = errors.New("sync_key_lock_unavailable")
	ErrSyncKeyLockUnsafe       = errors.New("sync_key_lock_unsafe")
	ErrSyncKeyPathUnavailable  = errors.New("sync_key_path_unavailable")
	ErrSyncKeyChmodFailed      = errors.New("sync_key_chmod_failed")
	ErrSyncKeyReadFailed       = errors.New("sync_key_read_failed")
	ErrSyncKeyWriteFailed      = errors.New("sync_key_write_failed")
)

type SyncKeyMetadata struct {
	Schema          string `json:"schema"`
	HostID          string `json:"host_id"`
	KeyID           string `json:"key_id"`
	PublicKeySHA256 string `json:"public_key_sha256"`
	PublicKey       string `json:"public_key"`
	CreatedAt       string `json:"created_at"`
}

type SyncKeyCreateResult struct {
	PrivateKeyPath  string
	PublicKeyPath   string
	SidecarPath     string
	KeyID           string
	PublicKeySHA256 string
	PublicKey       string
	Metadata        SyncKeyMetadata
}

type SyncKeyGenerateRequest struct {
	KeyPath       string
	PublicKeyPath string
	HostID        string
}

type SyncKeyGenerator func(SyncKeyGenerateRequest) error

type SyncKeyCreateOptions struct {
	KeyPath   string
	HostID    string
	Checker   storagecheck.Checker
	Now       func() time.Time
	Generator SyncKeyGenerator
}

// CreateLocalSyncKey creates a new local sync key at KeyPath and writes its
// public metadata sidecar. KeyPath's parent directory must already exist and be
// pre-resolved by the caller; symlinked parent ancestry is rejected.
func CreateLocalSyncKey(opts SyncKeyCreateOptions) (SyncKeyCreateResult, error) {
	var result SyncKeyCreateResult
	if err := ValidateSyncKeyPath(opts.KeyPath); err != nil {
		return result, fmt.Errorf("invalid_sync_key: %w", err)
	}
	if err := ValidateHostID(opts.HostID); err != nil {
		return result, err
	}

	if err := validateSyncKeyParentDirectory(opts.KeyPath); err != nil {
		return result, err
	}
	if err := syncKeyStoragePreflight(opts.Checker, opts.KeyPath); err != nil {
		return result, err
	}

	releaseLock, err := acquireSyncKeyCreateLock(opts.KeyPath)
	if err != nil {
		return result, err
	}
	defer func() { _ = releaseLock() }()

	publicKeyPath := opts.KeyPath + ".pub"
	sidecarPath := opts.KeyPath + ".clipfan.json"
	for _, path := range []string{opts.KeyPath, publicKeyPath, sidecarPath} {
		if exists, err := pathExists(path); err != nil {
			return result, err
		} else if exists {
			return result, ErrSyncKeyExists
		}
	}

	cleanupGenerated := true
	defer func() {
		if cleanupGenerated {
			_ = os.Remove(opts.KeyPath)
			_ = os.Remove(publicKeyPath)
			_ = os.Remove(sidecarPath)
		}
	}()
	req := SyncKeyGenerateRequest{
		KeyPath:       opts.KeyPath,
		PublicKeyPath: publicKeyPath,
		HostID:        opts.HostID,
	}
	if opts.Generator == nil {
		if err := generateSyncKeyWithSSHKeygen(req); err != nil {
			return result, err
		}
	} else if err := opts.Generator(req); err != nil {
		return result, ErrSyncKeyGenerationFailed
	}
	if err := chmodSyncKeyPrivateFile(opts.KeyPath); err != nil {
		return result, ErrSyncKeyChmodFailed
	}

	publicKey, publicKeyBlob, err := readOpenSSHPublicKey(publicKeyPath)
	if err != nil {
		return result, err
	}
	sum := sha256.Sum256(publicKeyBlob)
	keyID := hex.EncodeToString(sum[:8])
	digest := "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}

	metadata := SyncKeyMetadata{
		Schema:          syncKeyMetadataSchema,
		HostID:          opts.HostID,
		KeyID:           keyID,
		PublicKeySHA256: digest,
		PublicKey:       publicKey,
		CreatedAt:       now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return result, err
	}
	data = append(data, '\n')
	if err := writeConfigAtomic(sidecarPath, data, 0o600); err != nil {
		return result, ErrSyncKeyWriteFailed
	}
	cleanupGenerated = false

	return SyncKeyCreateResult{
		PrivateKeyPath:  opts.KeyPath,
		PublicKeyPath:   publicKeyPath,
		SidecarPath:     sidecarPath,
		KeyID:           keyID,
		PublicKeySHA256: digest,
		PublicKey:       publicKey,
		Metadata:        metadata,
	}, nil
}

// ValidateSyncKeyPath checks the lexical path shape for a sync key. It does not
// resolve symlinks; CreateLocalSyncKey rejects unresolved parent ancestry.
func ValidateSyncKeyPath(value string) error {
	return ValidateSafeAbsolutePath(value)
}

func syncKeyStoragePreflight(checker storagecheck.Checker, keyPath string) error {
	if _, err := checker.CheckRoots(storagecheck.SyncKeyRoots(keyPath)); err != nil {
		if errors.Is(err, storagecheck.ErrUnsupportedRuntimeStorage) {
			return storagecheck.ErrUnsupportedRuntimeStorage
		}
		return storagecheck.ErrStorageCheckInconclusive
	}
	return nil
}

func validateSyncKeyParentDirectory(keyPath string) error {
	parent := filepath.Dir(keyPath)
	info, err := os.Lstat(parent)
	if errors.Is(err, os.ErrNotExist) {
		return ErrSyncKeyDirectoryMissing
	}
	if err != nil {
		return ErrSyncKeyDirectoryUnsafe
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrSyncKeyDirectoryUnsafe
	}
	if !info.Mode().IsDir() {
		return ErrSyncKeyParentNotDir
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return ErrSyncKeyDirectoryUnsafe
	}
	if filepath.Clean(resolved) != filepath.Clean(parent) {
		return ErrSyncKeyDirectoryUnsafe
	}
	return nil
}

func chmodSyncKeyPrivateFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return ErrSyncKeyChmodFailed
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ErrSyncKeyChmodFailed
	}
	if !info.Mode().IsRegular() {
		return ErrSyncKeyChmodFailed
	}
	if err := file.Chmod(0o600); err != nil {
		return ErrSyncKeyChmodFailed
	}
	return nil
}

func acquireSyncKeyCreateLock(keyPath string) (func() error, error) {
	lockPath := keyPath + ".create.lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, ErrSyncKeyLockUnsafe
		}
		return nil, ErrSyncKeyLockUnavailable
	}
	if err := lockFile.Chmod(0o600); err != nil {
		_ = lockFile.Close()
		return nil, ErrSyncKeyLockUnavailable
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		_ = lockFile.Close()
		return nil, ErrSyncKeyLockUnavailable
	}
	return func() error {
		unlockErr := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		closeErr := lockFile.Close()
		return errors.Join(unlockErr, closeErr)
	}, nil
}

func pathExists(path string) (bool, error) {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, ErrSyncKeyPathUnavailable
	}
	return true, nil
}

func generateSyncKeyWithSSHKeygen(req SyncKeyGenerateRequest) error {
	cmd := exec.Command("ssh-keygen", syncKeygenArgs(req)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return syncKeyCommandError{err: err, stderr: stderr.String(), keyPath: req.KeyPath}
	}
	return nil
}

func syncKeygenArgs(req SyncKeyGenerateRequest) []string {
	return []string{"-t", "ed25519", "-N", "", "-f", req.KeyPath, "-C", "clipfan:" + req.HostID}
}

type syncKeyCommandError struct {
	err     error
	stderr  string
	keyPath string
}

func (e syncKeyCommandError) Error() string {
	stderr := sanitizeSyncKeyCommandDiagnostic(e.stderr, e.keyPath)
	if stderr != "" {
		return ErrSyncKeyGenerationFailed.Error() + ": " + stderr
	}
	cause := sanitizeSyncKeyCommandDiagnostic(errorString(e.err), e.keyPath)
	if cause != "" {
		return ErrSyncKeyGenerationFailed.Error() + ": " + cause
	}
	return ErrSyncKeyGenerationFailed.Error()
}

func (e syncKeyCommandError) Is(target error) bool {
	return target == ErrSyncKeyGenerationFailed
}

func sanitizeSyncKeyCommandDiagnostic(value, keyPath string) string {
	out := strings.TrimSpace(value)
	if keyPath != "" {
		out = strings.ReplaceAll(out, keyPath, "<sync_key_path>")
		dir := filepath.Dir(keyPath)
		if dir != "." && dir != "/" {
			out = strings.ReplaceAll(out, dir, "<sync_key_dir>")
		}
	}
	return out
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func readOpenSSHPublicKey(path string) (string, []byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", nil, ErrSyncKeyReadFailed
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", nil, ErrSyncKeyReadFailed
	}
	if !info.Mode().IsRegular() {
		return "", nil, ErrSyncKeyReadFailed
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return "", nil, ErrSyncKeyReadFailed
	}
	line := strings.TrimSpace(string(data))
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "ssh-ed25519" {
		return "", nil, ErrInvalidSyncKeyPublicKey
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return "", nil, ErrInvalidSyncKeyPublicKey
	}
	return line, blob, nil
}
