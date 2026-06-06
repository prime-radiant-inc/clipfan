package sshprovision

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

const (
	authorizedKeyMarkerPrefix   = "clipfan-sync:"
	managedKeyType              = "ssh-ed25519"
	SSHGatewayProbeCommand      = "probe-authorized-key"
	SSHGatewaySyncStreamCommand = "sync-stream"
)

var (
	ErrInvalidAuthorizedKey  = errors.New("invalid_authorized_key")
	ErrAuthorizedKeyConflict = errors.New("authorized_key_conflict")
	ErrAuthorizedKeyNotFound = errors.New("authorized_key_not_found")
	ErrAuthorizedKeysUnsafe  = errors.New("authorized_keys_unsafe")
)

var authorizedKeyIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

var authorizedKeysTempCounter uint64

type ManagedAuthorizedKey struct {
	PeerID      string
	KeyID       string
	GatewayPath string
	PublicKey   string
}

type ManagedAuthorizedKeyMetadata struct {
	PeerID string
	KeyID  string
}

func NewManagedAuthorizedKey(entry ManagedAuthorizedKey) (ManagedAuthorizedKey, error) {
	if err := config.ValidateHostID(entry.PeerID); err != nil {
		return ManagedAuthorizedKey{}, fmt.Errorf("%w: invalid peer id: %v", ErrInvalidAuthorizedKey, err)
	}
	if err := ValidateManagedAuthorizedKeyID(entry.KeyID); err != nil {
		return ManagedAuthorizedKey{}, fmt.Errorf("%w: invalid key id", ErrInvalidAuthorizedKey)
	}
	if err := config.ValidateSSHExecutablePath(entry.GatewayPath); err != nil {
		return ManagedAuthorizedKey{}, fmt.Errorf("%w: invalid gateway path: %v", ErrInvalidAuthorizedKey, err)
	}
	keyType, err := publicKeyType(entry.PublicKey)
	if err != nil {
		return ManagedAuthorizedKey{}, err
	}
	if keyType != managedKeyType {
		return ManagedAuthorizedKey{}, fmt.Errorf("%w: unsupported public key type %q", ErrInvalidAuthorizedKey, keyType)
	}
	return entry, nil
}

func ValidateManagedAuthorizedKeyID(keyID string) error {
	if !authorizedKeyIDPattern.MatchString(keyID) {
		return fmt.Errorf("invalid_authorized_key_id")
	}
	return nil
}

func (entry ManagedAuthorizedKey) ForcedCommand() string {
	return entry.GatewayPath + " ssh-gateway --authorized-peer " + entry.PeerID + " --authorized-key-id " + entry.KeyID
}

func (entry ManagedAuthorizedKey) Line() string {
	return `no-agent-forwarding,no-X11-forwarding,no-port-forwarding,no-pty,no-user-rc,command="` + escapeAuthorizedKeyOption(entry.ForcedCommand()) + `" ` +
		managedKeyType + " " + entry.PublicKey + " " + authorizedKeyMarkerPrefix + entry.PeerID + ":" + entry.KeyID
}

func UpsertManagedAuthorizedKeyLine(data []byte, entry ManagedAuthorizedKey) ([]byte, error) {
	if _, err := NewManagedAuthorizedKey(entry); err != nil {
		return nil, err
	}

	lines, trailingNewline := splitAuthorizedKeyLines(string(data))
	out := make([]string, 0, len(lines)+1)
	replaced := false
	for _, line := range lines {
		metadata, managed, err := ParseManagedAuthorizedKeyMetadata(line)
		if err != nil {
			return nil, err
		}
		if !managed {
			if authorizedKeyLineUsesPublicKey(line, managedKeyType, entry.PublicKey) {
				continue
			}
			out = append(out, line)
			continue
		}
		if metadata.PeerID == entry.PeerID {
			if !replaced {
				out = append(out, entry.Line())
				replaced = true
			}
			continue
		}
		if metadata.KeyID == entry.KeyID {
			return nil, fmt.Errorf("%w: key id %s already belongs to peer %s", ErrAuthorizedKeyConflict, entry.KeyID, metadata.PeerID)
		}
		if authorizedKeyLineUsesPublicKey(line, managedKeyType, entry.PublicKey) {
			return nil, fmt.Errorf("%w: public key already belongs to peer %s", ErrAuthorizedKeyConflict, metadata.PeerID)
		}
		out = append(out, line)
	}
	if !replaced {
		out = append(out, entry.Line())
		trailingNewline = true
	}
	return joinAuthorizedKeyLines(out, trailingNewline), nil
}

func authorizedKeyLineUsesPublicKey(line string, keyType string, publicKey string) bool {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return false
	}
	first, rest, ok := readAuthorizedKeyField(line)
	if !ok {
		return false
	}
	if first == keyType {
		candidateKey, _, ok := readAuthorizedKeyField(rest)
		return ok && candidateKey == publicKey
	}
	candidateType, rest, ok := readAuthorizedKeyField(rest)
	if !ok || candidateType != keyType {
		return false
	}
	candidateKey, _, ok := readAuthorizedKeyField(rest)
	return ok && candidateKey == publicKey
}

func readAuthorizedKeyField(line string) (string, string, bool) {
	line = strings.TrimLeftFunc(line, isAuthorizedKeySpace)
	if line == "" {
		return "", "", false
	}
	inQuote := false
	escaped := false
	for i, ch := range line {
		switch {
		case escaped:
			escaped = false
		case inQuote && ch == '\\':
			escaped = true
		case ch == '"':
			inQuote = !inQuote
		case !inQuote && isAuthorizedKeySpace(ch):
			return line[:i], strings.TrimLeftFunc(line[i:], isAuthorizedKeySpace), true
		}
	}
	if inQuote {
		return "", "", false
	}
	return line, "", true
}

func isAuthorizedKeySpace(ch rune) bool {
	return ch == ' ' || ch == '\t'
}

func ManagedAuthorizedKeysPath(homeDir string) (string, error) {
	if err := validateAuthorizedKeysHomeDir(homeDir); err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".ssh", "authorized_keys"), nil
}

func UpsertManagedAuthorizedKeyFile(homeDir string, entry ManagedAuthorizedKey) (bool, error) {
	path, err := ManagedAuthorizedKeysPath(homeDir)
	if err != nil {
		return false, err
	}
	if _, err := NewManagedAuthorizedKey(entry); err != nil {
		return false, err
	}

	changed := false
	err = withAuthorizedKeysLock(path, func() error {
		if err := removeStaleAuthorizedKeysTemps(path); err != nil {
			return err
		}
		data, _, err := readAuthorizedKeysFile(path)
		if err != nil {
			return err
		}
		updated, err := UpsertManagedAuthorizedKeyLine(data, entry)
		if err != nil {
			return err
		}
		if bytes.Equal(updated, data) {
			return nil
		}
		if err := writeAuthorizedKeysAtomic(path, updated, 0o600); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return changed, err
}

func VerifyManagedAuthorizedKeyFile(homeDir string, entry ManagedAuthorizedKey) error {
	path, err := ManagedAuthorizedKeysPath(homeDir)
	if err != nil {
		return err
	}
	if _, err := NewManagedAuthorizedKey(entry); err != nil {
		return err
	}
	if err := validateAuthorizedKeysReadPath(path); err != nil {
		return err
	}
	data, exists, err := readAuthorizedKeysFile(path)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: %s", ErrAuthorizedKeyNotFound, path)
	}
	updated, err := UpsertManagedAuthorizedKeyLine(data, entry)
	if err != nil {
		return err
	}
	if !bytes.Equal(updated, data) {
		return fmt.Errorf("%w: %s", ErrAuthorizedKeyNotFound, entry.PeerID)
	}
	return nil
}

func ParseManagedAuthorizedKeyMetadata(line string) (ManagedAuthorizedKeyMetadata, bool, error) {
	fields := strings.Fields(line)
	markerIndex := -1
	marker := ""
	for i, field := range fields {
		if strings.HasPrefix(field, authorizedKeyMarkerPrefix) {
			markerIndex = i
			marker = field
			break
		}
	}
	if markerIndex == -1 {
		return ManagedAuthorizedKeyMetadata{}, false, nil
	}

	metadata, err := parseManagedAuthorizedKeyMarker(marker)
	if err != nil {
		return ManagedAuthorizedKeyMetadata{}, true, err
	}
	if err := validateManagedAuthorizedKeyLine(line, fields, markerIndex, metadata); err != nil {
		return ManagedAuthorizedKeyMetadata{}, true, err
	}
	return metadata, true, nil
}

func parseManagedAuthorizedKeyMarker(marker string) (ManagedAuthorizedKeyMetadata, error) {
	value := strings.TrimPrefix(marker, authorizedKeyMarkerPrefix)
	peerID, keyID, ok := strings.Cut(value, ":")
	if !ok {
		return ManagedAuthorizedKeyMetadata{}, fmt.Errorf("%w: malformed managed marker", ErrAuthorizedKeyConflict)
	}
	metadata := ManagedAuthorizedKeyMetadata{PeerID: peerID, KeyID: keyID}
	if err := config.ValidateHostID(metadata.PeerID); err != nil {
		return ManagedAuthorizedKeyMetadata{}, fmt.Errorf("%w: invalid managed peer id", ErrAuthorizedKeyConflict)
	}
	if !authorizedKeyIDPattern.MatchString(metadata.KeyID) {
		return ManagedAuthorizedKeyMetadata{}, fmt.Errorf("%w: invalid managed key id", ErrAuthorizedKeyConflict)
	}
	return metadata, nil
}

func validateManagedAuthorizedKeyLine(line string, fields []string, markerIndex int, metadata ManagedAuthorizedKeyMetadata) error {
	if markerIndex < 2 || fields[markerIndex-2] != managedKeyType {
		return fmt.Errorf("%w: malformed managed key line", ErrAuthorizedKeyConflict)
	}
	keyType, err := publicKeyType(fields[markerIndex-1])
	if err != nil || keyType != managedKeyType {
		return fmt.Errorf("%w: invalid managed public key", ErrAuthorizedKeyConflict)
	}
	command, ok := parseAuthorizedKeyCommandOption(line)
	if !ok {
		return fmt.Errorf("%w: missing managed forced command", ErrAuthorizedKeyConflict)
	}
	expectedSuffix := " ssh-gateway --authorized-peer " + metadata.PeerID + " --authorized-key-id " + metadata.KeyID
	if !strings.HasSuffix(command, expectedSuffix) {
		return fmt.Errorf("%w: managed forced command metadata mismatch", ErrAuthorizedKeyConflict)
	}
	gatewayPath := strings.TrimSuffix(command, expectedSuffix)
	if err := config.ValidateSSHExecutablePath(gatewayPath); err != nil {
		return fmt.Errorf("%w: invalid managed gateway path", ErrAuthorizedKeyConflict)
	}
	return nil
}

func parseAuthorizedKeyCommandOption(line string) (string, bool) {
	for i := 0; i < len(line); i++ {
		if !strings.HasPrefix(line[i:], `command="`) {
			continue
		}
		value, ok := readAuthorizedKeyQuotedOption(line[i+len(`command="`):])
		if ok {
			return value, true
		}
	}
	return "", false
}

func readAuthorizedKeyQuotedOption(value string) (string, bool) {
	var out strings.Builder
	escaped := false
	for _, ch := range value {
		if escaped {
			out.WriteRune(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			return out.String(), true
		}
		out.WriteRune(ch)
	}
	return "", false
}

func publicKeyType(publicKey string) (string, error) {
	if strings.TrimSpace(publicKey) != publicKey || strings.ContainsAny(publicKey, "\x00\r\n\t ") || publicKey == "" {
		return "", fmt.Errorf("%w: invalid public key", ErrInvalidAuthorizedKey)
	}
	blob, err := decodeKnownHostPublicKeyBlob(publicKey)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidAuthorizedKey, err)
	}
	keyType, err := readSSHString(blob)
	if err != nil {
		return "", fmt.Errorf("%w: malformed public key blob", ErrInvalidAuthorizedKey)
	}
	return keyType, nil
}

func escapeAuthorizedKeyOption(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}

func splitAuthorizedKeyLines(data string) ([]string, bool) {
	if data == "" {
		return nil, true
	}
	trailingNewline := strings.HasSuffix(data, "\n")
	trimmed := strings.TrimSuffix(data, "\n")
	if trimmed == "" {
		return nil, trailingNewline
	}
	return strings.Split(trimmed, "\n"), trailingNewline
}

func joinAuthorizedKeyLines(lines []string, trailingNewline bool) []byte {
	if len(lines) == 0 {
		if trailingNewline {
			return []byte("\n")
		}
		return nil
	}
	out := strings.Join(lines, "\n")
	if trailingNewline {
		out += "\n"
	}
	return []byte(out)
}

func validateAuthorizedKeysHomeDir(path string) error {
	if err := config.ValidateSafeAbsolutePath(path); err != nil {
		return fmt.Errorf("%w: invalid path: %v", ErrAuthorizedKeysUnsafe, err)
	}
	return nil
}

func withAuthorizedKeysLock(path string, fn func() error) error {
	if err := ensureAuthorizedKeysDir(filepath.Dir(path)); err != nil {
		return err
	}
	lockPath := path + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return fmt.Errorf("%w: lock is symlink: %s", ErrAuthorizedKeysUnsafe, lockPath)
		}
		return err
	}
	defer lockFile.Close()
	if err := validateAuthorizedKeysLockFile(lockPath, lockFile); err != nil {
		return err
	}
	if err := lockFile.Chmod(0o600); err != nil {
		return err
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	return fn()
}

func ensureAuthorizedKeysDir(dir string) error {
	if filepath.Base(dir) != ".ssh" {
		return fmt.Errorf("%w: parent must be .ssh: %s", ErrAuthorizedKeysUnsafe, dir)
	}
	if err := validateAuthorizedKeysExistingDir(filepath.Dir(dir)); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(dir, 0o700); err != nil {
			return err
		}
		info, err = os.Lstat(dir)
	}
	if err != nil {
		return err
	}
	if err := validateAuthorizedKeysDirInfo(dir, info); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("%w: parent path is unsafe: %s", ErrAuthorizedKeysUnsafe, dir)
	}
	if filepath.Clean(resolved) != filepath.Clean(dir) {
		return fmt.Errorf("%w: parent ancestry uses symlink: %s", ErrAuthorizedKeysUnsafe, dir)
	}
	return os.Chmod(dir, 0o700)
}

func validateAuthorizedKeysExistingDir(dir string) error {
	info, err := os.Lstat(filepath.Clean(dir))
	if err != nil {
		return err
	}
	if err := validateAuthorizedKeysDirInfo(dir, info); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("%w: directory path is unsafe: %s", ErrAuthorizedKeysUnsafe, dir)
	}
	if filepath.Clean(resolved) != filepath.Clean(dir) {
		return fmt.Errorf("%w: directory ancestry uses symlink: %s", ErrAuthorizedKeysUnsafe, dir)
	}
	return nil
}

func validateAuthorizedKeysReadPath(path string) error {
	homeDir := filepath.Dir(filepath.Dir(path))
	if err := validateAuthorizedKeysExistingDir(homeDir); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return validateAuthorizedKeysExistingDir(dir)
}

func validateAuthorizedKeysDirInfo(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: directory is symlink: %s", ErrAuthorizedKeysUnsafe, path)
	}
	if !info.Mode().IsDir() {
		return fmt.Errorf("%w: directory is not a directory: %s", ErrAuthorizedKeysUnsafe, path)
	}
	identity, err := authorizedKeysFileIdentity(info)
	if err != nil {
		return err
	}
	if identity.uid != uint32(os.Getuid()) {
		return fmt.Errorf("%w: directory owner uid %d does not match current uid %d: %s", ErrAuthorizedKeysUnsafe, identity.uid, os.Getuid(), path)
	}
	return nil
}

func readAuthorizedKeysFile(path string) ([]byte, bool, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		if errors.Is(err, syscall.ELOOP) {
			return nil, false, fmt.Errorf("%w: file is symlink: %s", ErrAuthorizedKeysUnsafe, path)
		}
		return nil, false, err
	}
	defer file.Close()
	if err := validateAuthorizedKeysOpenedFile(path, file); err != nil {
		return nil, false, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func validateAuthorizedKeysOpenedFile(path string, file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if err := validateAuthorizedKeysFileInfo(path, info); err != nil {
		return err
	}
	openedIdentity, err := authorizedKeysFileIdentity(info)
	if err != nil {
		return err
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if err := validateAuthorizedKeysFileInfo(path, pathInfo); err != nil {
		return err
	}
	pathIdentity, err := authorizedKeysFileIdentity(pathInfo)
	if err != nil {
		return err
	}
	if openedIdentity.device != pathIdentity.device || openedIdentity.inode != pathIdentity.inode {
		return fmt.Errorf("%w: file changed during open: %s", ErrAuthorizedKeysUnsafe, path)
	}
	return nil
}

func validateAuthorizedKeysFileInfo(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: file is symlink: %s", ErrAuthorizedKeysUnsafe, path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: file is not regular: %s", ErrAuthorizedKeysUnsafe, path)
	}
	identity, err := authorizedKeysFileIdentity(info)
	if err != nil {
		return err
	}
	if identity.uid != uint32(os.Getuid()) {
		return fmt.Errorf("%w: owner uid %d does not match current uid %d: %s", ErrAuthorizedKeysUnsafe, identity.uid, os.Getuid(), path)
	}
	if identity.linkCount > 1 {
		return fmt.Errorf("%w: file has multiple links: %s", ErrAuthorizedKeysUnsafe, path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: permissions are too open: %s", ErrAuthorizedKeysUnsafe, path)
	}
	return nil
}

func validateAuthorizedKeysLockFile(lockPath string, file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: lock is not regular: %s", ErrAuthorizedKeysUnsafe, lockPath)
	}
	openedIdentity, err := authorizedKeysFileIdentity(info)
	if err != nil {
		return err
	}
	if openedIdentity.uid != uint32(os.Getuid()) {
		return fmt.Errorf("%w: lock owner uid %d does not match current uid %d: %s", ErrAuthorizedKeysUnsafe, openedIdentity.uid, os.Getuid(), lockPath)
	}
	if openedIdentity.linkCount > 1 {
		return fmt.Errorf("%w: lock has multiple links: %s", ErrAuthorizedKeysUnsafe, lockPath)
	}
	pathInfo, err := os.Lstat(lockPath)
	if err != nil {
		return err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: lock is symlink: %s", ErrAuthorizedKeysUnsafe, lockPath)
	}
	if !pathInfo.Mode().IsRegular() {
		return fmt.Errorf("%w: lock is not regular: %s", ErrAuthorizedKeysUnsafe, lockPath)
	}
	pathIdentity, err := authorizedKeysFileIdentity(pathInfo)
	if err != nil {
		return err
	}
	if openedIdentity.device != pathIdentity.device || openedIdentity.inode != pathIdentity.inode {
		return fmt.Errorf("%w: lock changed during open: %s", ErrAuthorizedKeysUnsafe, lockPath)
	}
	if pathInfo.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: lock permissions are too open: %s", ErrAuthorizedKeysUnsafe, lockPath)
	}
	return nil
}

type authorizedKeysFileIdentityInfo struct {
	device    uint64
	inode     uint64
	linkCount uint64
	uid       uint32
}

func authorizedKeysFileIdentity(info os.FileInfo) (authorizedKeysFileIdentityInfo, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return authorizedKeysFileIdentityInfo{}, fmt.Errorf("%w: could not inspect file identity", ErrAuthorizedKeysUnsafe)
	}
	return authorizedKeysFileIdentityInfo{
		device:    uint64(stat.Dev),
		inode:     uint64(stat.Ino),
		linkCount: uint64(stat.Nlink),
		uid:       uint32(stat.Uid),
	}, nil
}

func removeStaleAuthorizedKeysTemps(path string) error {
	dir := filepath.Dir(path)
	prefix := authorizedKeysTempPrefix(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func authorizedKeysTempPrefix(path string) string {
	return ".authorized-keys-" + filepath.Base(path) + ".tmp."
}

func writeAuthorizedKeysAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := validateAuthorizedKeysExistingDir(dir); err != nil {
		return err
	}
	tmp := filepath.Join(dir, fmt.Sprintf("%s%d.%d", authorizedKeysTempPrefix(path), os.Getpid(), atomic.AddUint64(&authorizedKeysTempCounter, 1)))
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()
	n, err := file.Write(data)
	if err != nil {
		_ = file.Close()
		return err
	}
	if n != len(data) {
		_ = file.Close()
		return fmt.Errorf("short write to %s", tmp)
	}
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if err := syncBestEffort(file); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := validateAuthorizedKeysExistingDir(dir); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	cleanup = false
	return syncDirBestEffort(dir)
}
