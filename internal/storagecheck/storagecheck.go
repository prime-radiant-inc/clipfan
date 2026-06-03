package storagecheck

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	ErrUnsupportedRuntimeStorage = errors.New("unsupported_runtime_storage")
	ErrStorageCheckInconclusive  = errors.New("storage_check_inconclusive")
)

type Code string

const (
	CodeOK                        Code = "ok"
	CodeUnsupportedRuntimeStorage Code = "unsupported_runtime_storage"
	CodeStorageCheckInconclusive  Code = "storage_check_inconclusive"
)

type RootRole string

const (
	RootConfig  RootRole = "config"
	RootState   RootRole = "state"
	RootSyncKey RootRole = "sync_key"
)

type StorageClass string

const (
	ClassLocal        StorageClass = "local"
	ClassNetwork      StorageClass = "network"
	ClassCloudSync    StorageClass = "cloud_sync"
	ClassInconclusive StorageClass = "inconclusive"
)

type Root struct {
	Role RootRole
	Path string
}

type Fact struct {
	FilesystemType  string
	FilesystemMagic int64
	MountPoint      string
	Local           *bool
}

type Result struct {
	Role                    RootRole
	Path                    string
	NormalizedPath          string
	Code                    Code
	StorageClass            StorageClass
	FilesystemType          string
	FilesystemMagic         int64
	MountPoint              string
	Reason                  string
	CrossHostLockingClaimed bool
}

type ProbeFunc func(path string) (Fact, error)
type SmokeFunc func(path string) error

type Checker struct {
	HomeDir string
	Probe   ProbeFunc
	Smoke   SmokeFunc
}

func RuntimeRoots(configRoot, stateRoot string) []Root {
	return []Root{
		{Role: RootConfig, Path: configRoot},
		{Role: RootState, Path: stateRoot},
	}
}

func SyncKeyRoots(syncKeyPath string) []Root {
	return []Root{{Role: RootSyncKey, Path: filepath.Dir(syncKeyPath)}}
}

func CheckRuntimeRoots(configRoot, stateRoot string) ([]Result, error) {
	return Checker{}.CheckRoots(RuntimeRoots(configRoot, stateRoot))
}

func (c Checker) CheckRoots(roots []Root) ([]Result, error) {
	results := make([]Result, 0, len(roots))
	var firstErr error
	for _, root := range roots {
		result, err := c.checkRoot(root)
		results = append(results, result)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return results, firstErr
}

func (c Checker) checkRoot(root Root) (Result, error) {
	normalized := normalizePath(root.Path)
	result := Result{
		Role:           root.Role,
		Path:           root.Path,
		NormalizedPath: normalized,
		Code:           CodeStorageCheckInconclusive,
		StorageClass:   ClassInconclusive,
	}
	if normalized == "" {
		result.Reason = "empty_root"
		return result, ErrStorageCheckInconclusive
	}

	effectivePath := normalized
	if resolved, err := filepath.EvalSymlinks(normalized); err == nil {
		effectivePath = normalizePath(resolved)
		result.NormalizedPath = effectivePath
	} else if !errors.Is(err, os.ErrNotExist) {
		result.Reason = "symlink_resolution_failed"
		return result, ErrStorageCheckInconclusive
	}

	home := c.homeDir()
	if isCloudSyncPathForHome(normalized, home) || isCloudSyncPathForHome(effectivePath, home) {
		result.Code = CodeUnsupportedRuntimeStorage
		result.StorageClass = ClassCloudSync
		result.Reason = "cloud_sync_root"
		return result, ErrUnsupportedRuntimeStorage
	}

	probe := c.Probe
	if probe == nil {
		probe = DefaultProbe
	}
	fact, err := probe(effectivePath)
	if err != nil {
		result.Reason = "probe_failed"
		return result, ErrStorageCheckInconclusive
	}
	result.FilesystemType = strings.ToLower(fact.FilesystemType)
	result.FilesystemMagic = fact.FilesystemMagic
	result.MountPoint = normalizePath(fact.MountPoint)

	class, code, reason := classifyFact(fact)
	result.StorageClass = class
	result.Code = code
	result.Reason = reason
	switch code {
	case CodeUnsupportedRuntimeStorage:
		return result, ErrUnsupportedRuntimeStorage
	case CodeStorageCheckInconclusive:
		return result, ErrStorageCheckInconclusive
	}

	smoke := c.Smoke
	if smoke == nil {
		smoke = LocalSmokeCheck
	}
	if err := smoke(effectivePath); err != nil {
		result.Code = CodeStorageCheckInconclusive
		result.StorageClass = ClassInconclusive
		result.Reason = "local_smoke_failed"
		return result, fmt.Errorf("%w: %v", ErrStorageCheckInconclusive, err)
	}
	result.Code = CodeOK
	result.StorageClass = ClassLocal
	result.Reason = "local_smoke_passed"
	result.CrossHostLockingClaimed = false
	return result, nil
}

func classifyFact(fact Fact) (StorageClass, Code, string) {
	fsType := strings.ToLower(fact.FilesystemType)
	if isUnsupportedFilesystem(fsType) || isUnsupportedFilesystemMagic(fact.FilesystemMagic) {
		return ClassNetwork, CodeUnsupportedRuntimeStorage, "unsupported_filesystem"
	}
	if fact.Local != nil && !*fact.Local {
		return ClassNetwork, CodeUnsupportedRuntimeStorage, "volume_not_local"
	}
	if isKnownLocalFilesystem(fsType) || isKnownLocalFilesystemMagic(fact.FilesystemMagic) || fact.Local != nil && *fact.Local {
		return ClassLocal, CodeOK, "local_filesystem"
	}
	if fsType == "" {
		return ClassInconclusive, CodeStorageCheckInconclusive, "missing_filesystem_type"
	}
	return ClassInconclusive, CodeStorageCheckInconclusive, "unknown_filesystem"
}

func isUnsupportedFilesystem(fsType string) bool {
	switch strings.ToLower(fsType) {
	case "nfs", "nfs4", "cifs", "smb", "smb2", "smb3", "smbfs", "9p", "afp", "afpfs", "webdav", "davfs", "davfs2", "sshfs", "fuse", "fuseblk", "fuse.sshfs", "fuse.rclone", "fusefs":
		return true
	default:
		return false
	}
}

func isKnownLocalFilesystem(fsType string) bool {
	switch strings.ToLower(fsType) {
	case "apfs", "hfs", "hfsplus", "ext2", "ext3", "ext4", "xfs", "btrfs", "zfs", "tmpfs", "f2fs", "ufs":
		return true
	default:
		return false
	}
}

func isUnsupportedFilesystemMagic(magic int64) bool {
	switch magic {
	case 0x6969, // nfs
		0x517b,     // smb
		0xff534d42, // cifs
		0x01021997, // 9p
		0x65735546: // fuse
		return true
	default:
		return false
	}
}

func isKnownLocalFilesystemMagic(magic int64) bool {
	switch magic {
	case 0xef53, // ext2/ext3/ext4
		0x58465342, // xfs
		0x9123683e, // btrfs
		0x01021994: // tmpfs
		return true
	default:
		return false
	}
}

func isCloudSyncPath(path string, home string) bool {
	if home == "" {
		return false
	}
	home = normalizePath(home)
	path = normalizePath(path)
	if home == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(home, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return false
	}
	parts := splitPath(rel)
	if len(parts) == 0 {
		return false
	}
	if len(parts) >= 2 && parts[0] == "Library" && parts[1] == "Mobile Documents" {
		return true
	}
	if len(parts) >= 3 && parts[0] == "Library" && parts[1] == "CloudStorage" && cloudName(parts[2]) {
		return true
	}
	return cloudName(parts[0])
}

func isCloudSyncPathForHome(path string, home string) bool {
	if isCloudSyncPath(path, home) {
		return true
	}
	if resolvedHome, err := filepath.EvalSymlinks(home); err == nil {
		return isCloudSyncPath(path, resolvedHome)
	}
	return false
}

func cloudName(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "dropbox") ||
		strings.Contains(lower, "google drive") ||
		strings.Contains(lower, "googledrive") ||
		strings.Contains(lower, "onedrive") ||
		strings.Contains(lower, "syncthing") ||
		strings.Contains(lower, "rclone")
}

func splitPath(path string) []string {
	if path == "." || path == "" {
		return nil
	}
	return strings.Split(filepath.Clean(path), string(filepath.Separator))
}

func (c Checker) homeDir() string {
	if c.HomeDir != "" {
		return c.HomeDir
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return ""
}

func normalizePath(path string) string {
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

func currentPlatform() string {
	return runtime.GOOS
}
