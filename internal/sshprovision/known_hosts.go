package sshprovision

import (
	"encoding/base64"
	"encoding/binary"
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

var (
	ErrInvalidKnownHostPin = errors.New("invalid_known_host_pin")
	ErrKnownHostMismatch   = errors.New("known_host_mismatch")
	ErrKnownHostNotFound   = errors.New("known_host_not_found")
	ErrKnownHostsUnsafe    = errors.New("known_hosts_unsafe")
)

var knownHostKeyTypePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@._+-]{0,127}$`)

var knownHostsTempCounter uint64

type KnownHostPin struct {
	Pattern   string
	KeyType   string
	PublicKey string
}

func KnownHostsPattern(host string, port int) (string, error) {
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("%w: invalid port %d", ErrInvalidKnownHostPin, port)
	}
	canonicalHost, err := config.CanonicalSSHHost(host)
	if err != nil {
		return "", fmt.Errorf("%w: invalid host: %v", ErrInvalidKnownHostPin, err)
	}
	if port == 22 {
		return canonicalHost, nil
	}
	return fmt.Sprintf("[%s]:%d", canonicalHost, port), nil
}

func NewKnownHostPin(host string, port int, keyType, publicKey string) (KnownHostPin, error) {
	pattern, err := KnownHostsPattern(host, port)
	if err != nil {
		return KnownHostPin{}, err
	}
	pin := KnownHostPin{
		Pattern:   pattern,
		KeyType:   keyType,
		PublicKey: publicKey,
	}
	if err := validateKnownHostPin(pin); err != nil {
		return KnownHostPin{}, err
	}
	return pin, nil
}

func ParseKnownHostScanLine(host string, port int, line string) (KnownHostPin, error) {
	pattern, err := KnownHostsPattern(host, port)
	if err != nil {
		return KnownHostPin{}, err
	}
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 3 || strings.HasPrefix(fields[0], "@") {
		return KnownHostPin{}, fmt.Errorf("%w: malformed known_hosts scan line", ErrInvalidKnownHostPin)
	}
	if !knownHostPatternListContains(fields[0], pattern) {
		return KnownHostPin{}, fmt.Errorf("%w: scan line does not contain %s", ErrKnownHostMismatch, pattern)
	}
	return NewKnownHostPin(host, port, fields[1], fields[2])
}

func (pin KnownHostPin) Line() string {
	return pin.Pattern + " " + pin.KeyType + " " + pin.PublicKey
}

func UpsertKnownHostPin(path string, pin KnownHostPin) error {
	if err := validateKnownHostsPath(path); err != nil {
		return err
	}
	if err := validateKnownHostPin(pin); err != nil {
		return err
	}
	return withKnownHostsLock(path, func() error {
		if err := removeStaleKnownHostsTemps(path); err != nil {
			return err
		}
		data, _, err := readKnownHostsFileForUpdate(path)
		if err != nil {
			return err
		}
		found, err := verifyKnownHostPinData(data, pin)
		if err == nil && found {
			return nil
		}
		if err != nil && !errors.Is(err, ErrKnownHostNotFound) {
			return err
		}
		return writeKnownHostsAtomic(path, appendKnownHostLine(data, pin.Line()), 0o600)
	})
}

func VerifyKnownHostPin(path string, pin KnownHostPin) error {
	if err := validateKnownHostsPath(path); err != nil {
		return err
	}
	if err := validateKnownHostPin(pin); err != nil {
		return err
	}
	data, exists, err := readKnownHostsFileForUpdate(path)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: %s", ErrKnownHostNotFound, path)
	}
	_, err = verifyKnownHostPinData(data, pin)
	return err
}

func validateKnownHostPin(pin KnownHostPin) error {
	if err := validateKnownHostPattern(pin.Pattern); err != nil {
		return err
	}
	if !knownHostKeyTypePattern.MatchString(pin.KeyType) || strings.TrimSpace(pin.KeyType) != pin.KeyType {
		return fmt.Errorf("%w: invalid key type", ErrInvalidKnownHostPin)
	}
	if strings.TrimSpace(pin.PublicKey) != pin.PublicKey || strings.ContainsAny(pin.PublicKey, "\x00\r\n\t ") {
		return fmt.Errorf("%w: invalid public key", ErrInvalidKnownHostPin)
	}
	if pin.PublicKey == "" {
		return fmt.Errorf("%w: empty public key", ErrInvalidKnownHostPin)
	}
	blob, err := decodeKnownHostPublicKeyBlob(pin.PublicKey)
	if err != nil {
		return err
	}
	blobKeyType, err := readSSHString(blob)
	if err != nil {
		return fmt.Errorf("%w: malformed public key blob", ErrInvalidKnownHostPin)
	}
	if blobKeyType != pin.KeyType {
		return fmt.Errorf("%w: public key blob type %q does not match %q", ErrInvalidKnownHostPin, blobKeyType, pin.KeyType)
	}
	return nil
}

func decodeKnownHostPublicKeyBlob(publicKey string) ([]byte, error) {
	blob, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil {
		blob, err = base64.RawStdEncoding.DecodeString(publicKey)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid public key base64", ErrInvalidKnownHostPin)
		}
	}
	if len(blob) < 4 {
		return nil, fmt.Errorf("%w: public key blob too short", ErrInvalidKnownHostPin)
	}
	return blob, nil
}

func readSSHString(data []byte) (string, error) {
	if len(data) < 4 {
		return "", io.ErrUnexpectedEOF
	}
	size := binary.BigEndian.Uint32(data[:4])
	if size == 0 || uint64(size) > uint64(len(data)-4) {
		return "", io.ErrUnexpectedEOF
	}
	value := string(data[4 : 4+size])
	if strings.ContainsAny(value, "\x00\r\n\t ") {
		return "", fmt.Errorf("invalid ssh string")
	}
	return value, nil
}

func validateKnownHostPattern(pattern string) error {
	if pattern == "" || strings.TrimSpace(pattern) != pattern {
		return fmt.Errorf("%w: invalid pattern", ErrInvalidKnownHostPin)
	}
	if strings.HasPrefix(pattern, "@") || strings.ContainsAny(pattern, "\x00\r\n\t ,") {
		return fmt.Errorf("%w: invalid pattern", ErrInvalidKnownHostPin)
	}
	return nil
}

func validateKnownHostsPath(path string) error {
	if err := config.ValidateSSHExecutablePath(path); err != nil {
		return fmt.Errorf("%w: invalid path: %v", ErrKnownHostsUnsafe, err)
	}
	if err := rejectSymlinkedKnownHostsAncestry(filepath.Dir(path)); err != nil {
		return err
	}
	return nil
}

func verifyKnownHostPinData(data []byte, pin KnownHostPin) (bool, error) {
	found := false
	for _, rawLine := range strings.Split(string(data), "\n") {
		entry, ok := parseKnownHostsEntry(rawLine)
		if !ok {
			continue
		}
		if entry.marker != "" && (knownHostPatternListContains(entry.patterns, pin.Pattern) || knownHostPatternListMayMatch(entry.patterns, pin.Pattern)) {
			return false, fmt.Errorf("%w: marked entry for %s", ErrKnownHostMismatch, pin.Pattern)
		}
		if knownHostPatternListMayMatch(entry.patterns, pin.Pattern) {
			return false, fmt.Errorf("%w: broad entry for %s", ErrKnownHostMismatch, pin.Pattern)
		}
		if !knownHostPatternListContains(entry.patterns, pin.Pattern) {
			continue
		}
		if entry.keyType != pin.KeyType {
			continue
		}
		if entry.publicKey != pin.PublicKey {
			return false, fmt.Errorf("%w: %s %s", ErrKnownHostMismatch, pin.Pattern, pin.KeyType)
		}
		found = true
	}
	if found {
		return true, nil
	}
	return false, fmt.Errorf("%w: %s %s", ErrKnownHostNotFound, pin.Pattern, pin.KeyType)
}

type knownHostsEntry struct {
	marker    string
	patterns  string
	keyType   string
	publicKey string
}

func parseKnownHostsEntry(line string) (knownHostsEntry, bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
		return knownHostsEntry{}, false
	}
	if strings.HasPrefix(fields[0], "@") {
		if len(fields) < 4 {
			return knownHostsEntry{}, false
		}
		return knownHostsEntry{
			marker:    fields[0],
			patterns:  fields[1],
			keyType:   fields[2],
			publicKey: fields[3],
		}, true
	}
	if len(fields) < 3 {
		return knownHostsEntry{}, false
	}
	return knownHostsEntry{
		patterns:  fields[0],
		keyType:   fields[1],
		publicKey: fields[2],
	}, true
}

func knownHostPatternListContains(list, pattern string) bool {
	for _, candidate := range strings.Split(list, ",") {
		if candidate == pattern {
			return true
		}
	}
	return false
}

func knownHostPatternListMayMatch(list, pattern string) bool {
	for _, candidate := range strings.Split(list, ",") {
		if candidate == "" {
			continue
		}
		if strings.HasPrefix(candidate, "|") {
			return true
		}
		candidate = strings.TrimPrefix(candidate, "!")
		if strings.ContainsAny(candidate, "*?") {
			if openSSHKnownHostGlobMatches(candidate, pattern) {
				return true
			}
		}
	}
	return false
}

func openSSHKnownHostGlobMatches(glob, value string) bool {
	matches := make([][]bool, len(glob)+1)
	for i := range matches {
		matches[i] = make([]bool, len(value)+1)
	}
	matches[len(glob)][len(value)] = true
	for i := len(glob) - 1; i >= 0; i-- {
		for j := len(value); j >= 0; j-- {
			switch glob[i] {
			case '*':
				matches[i][j] = matches[i+1][j] || (j < len(value) && matches[i][j+1])
			case '?':
				matches[i][j] = j < len(value) && matches[i+1][j+1]
			default:
				matches[i][j] = j < len(value) && glob[i] == value[j] && matches[i+1][j+1]
			}
		}
	}
	return matches[0][0]
}

func appendKnownHostLine(data []byte, line string) []byte {
	out := append([]byte(nil), data...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	out = append(out, line...)
	out = append(out, '\n')
	return out
}

func withKnownHostsLock(path string, fn func() error) error {
	if err := ensureKnownHostsDir(filepath.Dir(path)); err != nil {
		return err
	}
	lockPath := path + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return fmt.Errorf("%w: lock is symlink: %s", ErrKnownHostsUnsafe, lockPath)
		}
		return err
	}
	defer lockFile.Close()
	if err := validateKnownHostsLockFile(lockPath, lockFile); err != nil {
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

func ensureKnownHostsDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := rejectSymlinkedKnownHostsAncestry(dir); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsDir() {
		return fmt.Errorf("%w: parent is not a directory: %s", ErrKnownHostsUnsafe, dir)
	}
	return os.Chmod(dir, 0o700)
}

func rejectSymlinkedKnownHostsAncestry(path string) error {
	cleaned := filepath.Clean(path)
	info, err := os.Lstat(cleaned)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: parent path uses symlink: %s", ErrKnownHostsUnsafe, path)
	}
	if !info.Mode().IsDir() {
		return fmt.Errorf("%w: parent path is not a directory: %s", ErrKnownHostsUnsafe, path)
	}
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return fmt.Errorf("%w: parent path is unsafe: %s", ErrKnownHostsUnsafe, path)
	}
	if filepath.Clean(resolved) != cleaned {
		return fmt.Errorf("%w: parent ancestry uses symlink: %s", ErrKnownHostsUnsafe, path)
	}
	return nil
}

func readKnownHostsFileForUpdate(path string) ([]byte, bool, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		if errors.Is(err, syscall.ELOOP) {
			return nil, false, fmt.Errorf("%w: file is symlink: %s", ErrKnownHostsUnsafe, path)
		}
		return nil, false, err
	}
	defer file.Close()
	if err := validateKnownHostsOpenedFile(path, file); err != nil {
		return nil, false, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func validateKnownHostsOpenedFile(path string, file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if err := validateKnownHostsFileInfo(path, info); err != nil {
		return err
	}
	openedIdentity, err := knownHostsFileIdentity(info)
	if err != nil {
		return err
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if err := validateKnownHostsFileInfo(path, pathInfo); err != nil {
		return err
	}
	pathIdentity, err := knownHostsFileIdentity(pathInfo)
	if err != nil {
		return err
	}
	if openedIdentity.device != pathIdentity.device || openedIdentity.inode != pathIdentity.inode {
		return fmt.Errorf("%w: file changed during open: %s", ErrKnownHostsUnsafe, path)
	}
	return nil
}

func validateKnownHostsFileInfo(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: file is symlink: %s", ErrKnownHostsUnsafe, path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: file is not regular: %s", ErrKnownHostsUnsafe, path)
	}
	identity, err := knownHostsFileIdentity(info)
	if err != nil {
		return err
	}
	if identity.uid != uint32(os.Getuid()) {
		return fmt.Errorf("%w: owner uid %d does not match current uid %d: %s", ErrKnownHostsUnsafe, identity.uid, os.Getuid(), path)
	}
	if identity.linkCount > 1 {
		return fmt.Errorf("%w: file has multiple links: %s", ErrKnownHostsUnsafe, path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: permissions are too open: %s", ErrKnownHostsUnsafe, path)
	}
	return nil
}

func validateKnownHostsLockFile(lockPath string, file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: lock is not regular: %s", ErrKnownHostsUnsafe, lockPath)
	}
	openedIdentity, err := knownHostsFileIdentity(info)
	if err != nil {
		return err
	}
	if openedIdentity.uid != uint32(os.Getuid()) {
		return fmt.Errorf("%w: lock owner uid %d does not match current uid %d: %s", ErrKnownHostsUnsafe, openedIdentity.uid, os.Getuid(), lockPath)
	}
	if openedIdentity.linkCount > 1 {
		return fmt.Errorf("%w: lock has multiple links: %s", ErrKnownHostsUnsafe, lockPath)
	}
	pathInfo, err := os.Lstat(lockPath)
	if err != nil {
		return err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: lock is symlink: %s", ErrKnownHostsUnsafe, lockPath)
	}
	if !pathInfo.Mode().IsRegular() {
		return fmt.Errorf("%w: lock is not regular: %s", ErrKnownHostsUnsafe, lockPath)
	}
	pathIdentity, err := knownHostsFileIdentity(pathInfo)
	if err != nil {
		return err
	}
	if openedIdentity.device != pathIdentity.device || openedIdentity.inode != pathIdentity.inode {
		return fmt.Errorf("%w: lock changed during open: %s", ErrKnownHostsUnsafe, lockPath)
	}
	if pathInfo.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: lock permissions are too open: %s", ErrKnownHostsUnsafe, lockPath)
	}
	return nil
}

type knownHostsFileIdentityInfo struct {
	device    uint64
	inode     uint64
	linkCount uint64
	uid       uint32
}

func knownHostsFileIdentity(info os.FileInfo) (knownHostsFileIdentityInfo, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return knownHostsFileIdentityInfo{}, fmt.Errorf("%w: could not inspect file identity", ErrKnownHostsUnsafe)
	}
	return knownHostsFileIdentityInfo{
		device:    uint64(stat.Dev),
		inode:     uint64(stat.Ino),
		linkCount: uint64(stat.Nlink),
		uid:       uint32(stat.Uid),
	}, nil
}

func removeStaleKnownHostsTemps(path string) error {
	dir := filepath.Dir(path)
	prefix := knownHostsTempPrefix(path)
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

func knownHostsTempPrefix(path string) string {
	return ".known-hosts-" + filepath.Base(path) + ".tmp."
}

func writeKnownHostsAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, fmt.Sprintf("%s%d.%d", knownHostsTempPrefix(path), os.Getpid(), atomic.AddUint64(&knownHostsTempCounter, 1)))
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
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	cleanup = false
	return syncDirBestEffort(dir)
}

func syncBestEffort(file *os.File) error {
	if err := file.Sync(); err != nil && !isUnsupportedSync(err) {
		return err
	}
	return nil
}

func syncDirBestEffort(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Sync(); err != nil && !isUnsupportedSync(err) {
		return err
	}
	return nil
}

func isUnsupportedSync(err error) bool {
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.ENOSYS)
}
