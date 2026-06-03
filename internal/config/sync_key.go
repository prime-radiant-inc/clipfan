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
	"sync/atomic"
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
	ErrMissingSyncKey          = errors.New("missing_sync_key")
	ErrSyncKeyIdentityMismatch = errors.New("sync_key_identity_mismatch")
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

type SyncKeyLoadOptions struct {
	KeyPath string
	HostID  string
	Checker storagecheck.Checker
}

type SyncKeyResetOptions struct {
	KeyPath   string
	HostID    string
	Checker   storagecheck.Checker
	Now       func() time.Time
	Generator SyncKeyGenerator
}

var syncKeyResetTempCounter uint64

type stagedSyncKeyReset struct {
	result  SyncKeyCreateResult
	keyPath string
	backups []syncKeyResetBackup
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

	return createLocalSyncKeyLocked(opts)
}

// ResetLocalSyncKey regenerates the sync key at KeyPath after explicit local
// fleet-reset confirmation. Existing private/public/sidecar material is moved
// aside only after replacement material has been generated successfully.
func ResetLocalSyncKey(opts SyncKeyResetOptions) (SyncKeyCreateResult, error) {
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

	staged, err := stageLocalSyncKeyResetLocked(opts)
	if err != nil {
		return result, err
	}
	staged.commit()
	return staged.result, nil
}

func createLocalSyncKeyLocked(opts SyncKeyCreateOptions) (SyncKeyCreateResult, error) {
	var result SyncKeyCreateResult
	publicKeyPath := opts.KeyPath + ".pub"
	sidecarPath := opts.KeyPath + ".clipfan.json"
	for _, path := range syncKeyMaterialPaths(opts.KeyPath) {
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
	keyID, digest := syncKeyIdentityFromPublicBlob(publicKeyBlob)
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

func stageLocalSyncKeyReset(opts SyncKeyResetOptions) (stagedSyncKeyReset, error) {
	if err := ValidateSyncKeyPath(opts.KeyPath); err != nil {
		return stagedSyncKeyReset{}, fmt.Errorf("invalid_sync_key: %w", err)
	}
	if err := ValidateHostID(opts.HostID); err != nil {
		return stagedSyncKeyReset{}, err
	}
	if err := validateSyncKeyParentDirectory(opts.KeyPath); err != nil {
		return stagedSyncKeyReset{}, err
	}
	if err := syncKeyStoragePreflight(opts.Checker, opts.KeyPath); err != nil {
		return stagedSyncKeyReset{}, err
	}
	releaseLock, err := acquireSyncKeyCreateLock(opts.KeyPath)
	if err != nil {
		return stagedSyncKeyReset{}, err
	}
	defer func() { _ = releaseLock() }()
	return stageLocalSyncKeyResetLocked(opts)
}

func stageLocalSyncKeyResetLocked(opts SyncKeyResetOptions) (stagedSyncKeyReset, error) {
	if err := removeStaleSyncKeyResetScratch(opts.KeyPath); err != nil {
		return stagedSyncKeyReset{}, err
	}
	tempPath := syncKeyResetTempPath(opts.KeyPath)
	tempOpts := SyncKeyCreateOptions{
		KeyPath:   tempPath,
		HostID:    opts.HostID,
		Checker:   opts.Checker,
		Now:       opts.Now,
		Generator: opts.Generator,
	}
	tempResult, err := createLocalSyncKeyLocked(tempOpts)
	if err != nil {
		removeSyncKeyMaterialBestEffort(tempPath)
		return stagedSyncKeyReset{}, err
	}
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			removeSyncKeyMaterialBestEffort(tempPath)
		}
	}()

	backups, err := moveExistingSyncKeyMaterialForReset(opts.KeyPath)
	if err != nil {
		return stagedSyncKeyReset{}, err
	}
	restoreBackups := true
	defer func() {
		if restoreBackups {
			rollbackInstalledSyncKeyReset(opts.KeyPath, backups)
		}
	}()

	if err := installResetSyncKeyMaterial(tempPath, opts.KeyPath); err != nil {
		return stagedSyncKeyReset{}, err
	}
	cleanupTemp = false
	restoreBackups = false

	tempResult.PrivateKeyPath = opts.KeyPath
	tempResult.PublicKeyPath = opts.KeyPath + ".pub"
	tempResult.SidecarPath = opts.KeyPath + ".clipfan.json"
	return stagedSyncKeyReset{result: tempResult, keyPath: opts.KeyPath, backups: backups}, nil
}

func (s stagedSyncKeyReset) commit() {
	removeSyncKeyResetBackups(s.backups)
}

func (s stagedSyncKeyReset) rollback() {
	rollbackInstalledSyncKeyReset(s.keyPath, s.backups)
}

func syncKeyMaterialPaths(keyPath string) []string {
	return []string{keyPath, keyPath + ".pub", keyPath + ".clipfan.json"}
}

func syncKeyResetTempPath(keyPath string) string {
	return filepath.Join(filepath.Dir(keyPath), fmt.Sprintf(".%s.reset.%d.%d", filepath.Base(keyPath), os.Getpid(), atomic.AddUint64(&syncKeyResetTempCounter, 1)))
}

func removeStaleSyncKeyResetScratch(keyPath string) error {
	entries, err := os.ReadDir(filepath.Dir(keyPath))
	if err != nil {
		return ErrSyncKeyDirectoryUnsafe
	}
	base := filepath.Base(keyPath)
	for _, entry := range entries {
		if entry.IsDir() || !isSyncKeyResetScratchName(base, entry.Name()) {
			continue
		}
		if err := os.Remove(filepath.Join(filepath.Dir(keyPath), entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return ErrSyncKeyWriteFailed
		}
	}
	return nil
}

func isSyncKeyResetScratchName(base, name string) bool {
	if suffix, ok := strings.CutPrefix(name, "."+base+".reset."); ok {
		for _, materialSuffix := range []string{"", ".pub", ".clipfan.json"} {
			if counterSuffix, ok := strings.CutSuffix(suffix, materialSuffix); ok && hasResetCounterSuffix(counterSuffix) {
				return true
			}
		}
		return false
	}
	for _, materialSuffix := range []string{"", ".pub", ".clipfan.json"} {
		if suffix, ok := strings.CutPrefix(name, base+materialSuffix+".reset-old."); ok {
			return hasResetCounterSuffix(suffix)
		}
	}
	return false
}

func hasResetCounterSuffix(value string) bool {
	pid, counter, ok := strings.Cut(value, ".")
	return ok && isDecimalString(pid) && isDecimalString(counter)
}

func isDecimalString(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

type syncKeyResetBackup struct {
	final  string
	backup string
}

func moveExistingSyncKeyMaterialForReset(keyPath string) ([]syncKeyResetBackup, error) {
	unique := fmt.Sprintf(".reset-old.%d.%d", os.Getpid(), atomic.AddUint64(&syncKeyResetTempCounter, 1))
	backups := make([]syncKeyResetBackup, 0, 3)
	for _, finalPath := range syncKeyMaterialPaths(keyPath) {
		info, err := os.Lstat(finalPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			restoreSyncKeyResetBackups(backups)
			return nil, ErrSyncKeyPathUnavailable
		}
		if info.IsDir() {
			restoreSyncKeyResetBackups(backups)
			return nil, ErrSyncKeyIdentityMismatch
		}
		backupPath := finalPath + unique
		if exists, err := pathExists(backupPath); err != nil || exists {
			restoreSyncKeyResetBackups(backups)
			if err != nil {
				return nil, err
			}
			return nil, ErrSyncKeyPathUnavailable
		}
		if err := os.Rename(finalPath, backupPath); err != nil {
			restoreSyncKeyResetBackups(backups)
			return nil, ErrSyncKeyWriteFailed
		}
		backups = append(backups, syncKeyResetBackup{final: finalPath, backup: backupPath})
	}
	return backups, nil
}

func installResetSyncKeyMaterial(tempPath, finalPath string) error {
	tempPaths := syncKeyMaterialPaths(tempPath)
	finalPaths := syncKeyMaterialPaths(finalPath)
	installed := make([]string, 0, len(finalPaths))
	for i := range tempPaths {
		if err := os.Rename(tempPaths[i], finalPaths[i]); err != nil {
			removePathsBestEffort(installed)
			return ErrSyncKeyWriteFailed
		}
		installed = append(installed, finalPaths[i])
	}
	return nil
}

func rollbackInstalledSyncKeyReset(keyPath string, backups []syncKeyResetBackup) {
	removePathsBestEffort(syncKeyMaterialPaths(keyPath))
	restoreSyncKeyResetBackups(backups)
}

func restoreSyncKeyResetBackups(backups []syncKeyResetBackup) {
	for i := len(backups) - 1; i >= 0; i-- {
		_ = os.Remove(backups[i].final)
		_ = os.Rename(backups[i].backup, backups[i].final)
	}
}

func removeSyncKeyResetBackups(backups []syncKeyResetBackup) {
	for _, backup := range backups {
		_ = os.Remove(backup.backup)
	}
}

func removeSyncKeyMaterialBestEffort(keyPath string) {
	removePathsBestEffort(syncKeyMaterialPaths(keyPath))
}

func removePathsBestEffort(paths []string) {
	for _, path := range paths {
		_ = os.Remove(path)
	}
}

// LoadLocalSyncKey validates local sync-key file hygiene and sidecar/public-key
// identity. After identity validation succeeds it may chmod the private key and
// sidecar to 0600. It deliberately does not read private key material into
// memory or prove the private half cryptographically; command-locked SSH use
// proves that.
func LoadLocalSyncKey(opts SyncKeyLoadOptions) (SyncKeyCreateResult, error) {
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

	publicKeyPath := opts.KeyPath + ".pub"
	sidecarPath := opts.KeyPath + ".clipfan.json"
	if err := validateSyncKeyMaterialFile(opts.KeyPath); err != nil {
		return result, err
	}
	publicData, err := readSyncKeyMaterialFile(publicKeyPath)
	if err != nil {
		return result, err
	}
	sidecarData, err := readSyncKeyMaterialFile(sidecarPath)
	if err != nil {
		return result, err
	}

	publicKey, publicKeyBlob, err := parseOpenSSHPublicKey(publicData)
	if err != nil {
		return result, ErrSyncKeyIdentityMismatch
	}
	keyID, digest := syncKeyIdentityFromPublicBlob(publicKeyBlob)
	metadata, err := parseSyncKeyMetadata(sidecarData)
	if err != nil {
		return result, ErrSyncKeyIdentityMismatch
	}
	if err := validateSyncKeyMetadata(metadata, opts.HostID, publicKey, digest, keyID); err != nil {
		return result, err
	}
	if err := repairLoadedSyncKeyPrivateModes(opts.KeyPath, sidecarPath); err != nil {
		return result, err
	}

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

func readSyncKeyMaterialFile(path string) ([]byte, error) {
	file, err := openSyncKeyMaterialFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if err := validateOpenedSyncKeyMaterialFile(path, file); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, ErrSyncKeyIdentityMismatch
	}
	return data, nil
}

func validateSyncKeyMaterialFile(path string) error {
	file, err := openSyncKeyMaterialFile(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return validateOpenedSyncKeyMaterialFile(path, file)
}

func openSyncKeyMaterialFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrMissingSyncKey
	}
	if err != nil {
		return nil, ErrSyncKeyIdentityMismatch
	}
	return file, nil
}

func validateOpenedSyncKeyMaterialFile(path string, file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return ErrSyncKeyIdentityMismatch
	}
	if err := validateSyncKeyMaterialFileInfo(info); err != nil {
		return err
	}
	openedIdentity, err := configFileIdentity(info)
	if err != nil {
		return ErrSyncKeyIdentityMismatch
	}
	pathInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrMissingSyncKey
	}
	if err != nil {
		return ErrSyncKeyIdentityMismatch
	}
	if err := validateSyncKeyMaterialFileInfo(pathInfo); err != nil {
		return err
	}
	pathIdentity, err := configFileIdentity(pathInfo)
	if err != nil {
		return ErrSyncKeyIdentityMismatch
	}
	if openedIdentity.device != pathIdentity.device || openedIdentity.inode != pathIdentity.inode {
		return ErrSyncKeyIdentityMismatch
	}
	return nil
}

func validateSyncKeyMaterialFileInfo(info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return ErrSyncKeyIdentityMismatch
	}
	identity, err := configFileIdentity(info)
	if err != nil {
		return ErrSyncKeyIdentityMismatch
	}
	if identity.uid != uint32(os.Getuid()) || identity.linkCount > 1 {
		return ErrSyncKeyIdentityMismatch
	}
	mode := info.Mode().Perm()
	if mode&0o022 != 0 {
		return ErrSyncKeyIdentityMismatch
	}
	return nil
}

func repairLoadedSyncKeyPrivateModes(privateKeyPath, sidecarPath string) error {
	for _, path := range []string{privateKeyPath, sidecarPath} {
		if err := repairLoadedSyncKeyPrivateMode(path); err != nil {
			return err
		}
	}
	return nil
}

func repairLoadedSyncKeyPrivateMode(path string) error {
	file, err := openSyncKeyMaterialFile(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := validateOpenedSyncKeyMaterialFile(path, file); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return ErrSyncKeyIdentityMismatch
	}
	if info.Mode().Perm() == 0o600 {
		return nil
	}
	if err := file.Chmod(0o600); err != nil {
		return ErrSyncKeyIdentityMismatch
	}
	return nil
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

func syncKeyIdentityFromPublicBlob(publicKeyBlob []byte) (string, string) {
	sum := sha256.Sum256(publicKeyBlob)
	return hex.EncodeToString(sum[:8]), "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

func parseSyncKeyMetadata(data []byte) (SyncKeyMetadata, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return SyncKeyMetadata{}, err
	}
	required := map[string]bool{
		"schema":            false,
		"host_id":           false,
		"key_id":            false,
		"public_key_sha256": false,
		"public_key":        false,
		"created_at":        false,
	}
	for key := range raw {
		if _, ok := required[key]; !ok {
			return SyncKeyMetadata{}, ErrSyncKeyIdentityMismatch
		}
		required[key] = true
	}
	for _, present := range required {
		if !present {
			return SyncKeyMetadata{}, ErrSyncKeyIdentityMismatch
		}
	}
	var metadata SyncKeyMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return SyncKeyMetadata{}, err
	}
	return metadata, nil
}

func validateSyncKeyMetadata(meta SyncKeyMetadata, expectedHostID, publicKey, digest, keyID string) error {
	if meta.Schema != syncKeyMetadataSchema {
		return ErrSyncKeyIdentityMismatch
	}
	if err := ValidateHostID(meta.HostID); err != nil {
		return ErrSyncKeyIdentityMismatch
	}
	if meta.HostID != expectedHostID ||
		meta.KeyID != keyID ||
		meta.PublicKeySHA256 != digest ||
		meta.PublicKey != publicKey {
		return ErrSyncKeyIdentityMismatch
	}
	if _, err := time.Parse(time.RFC3339, meta.CreatedAt); err != nil {
		return ErrSyncKeyIdentityMismatch
	}
	return nil
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
	return parseOpenSSHPublicKey(data)
}

func parseOpenSSHPublicKey(data []byte) (string, []byte, error) {
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
