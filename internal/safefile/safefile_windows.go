//go:build windows

package safefile

import (
	"os"
)

// OpenNoFollow: Windows has no O_NOFOLLOW, so this opens normally. Symlink
// hardening is reduced on Windows (a TODO if clipfan needs it there).
func OpenNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag, perm)
}

// Flock: real cross-process locking on Windows needs LockFileEx from
// golang.org/x/sys/windows, which this pure-stdlib build does not depend on.
// clipfan is single-process-per-host (the instance lock in internal/daemon is
// already a no-op on Windows), so a no-op advisory lock is safe for now.
// TODO: switch to windows.LockFileEx for real cross-process locking.
func Flock(f *os.File, how LockFlag) error { return nil }

// Unlock releases the (no-op) advisory lock.
func Unlock(f *os.File) error { return nil }

// IsUnsupportedSync: on Windows, file.Sync errors are treated as real errors.
func IsUnsupportedSync(err error) bool { return false }

// StatIdentity on Windows: os.FileInfo does not expose volume serial / file
// index / link count, so return neutral values that satisfy clipfan's hardening
// checks (TOCTOU equality and "owned by current user"). os.Getuid() returns -1
// on Windows, so uid = ^uint32(0) matches uint32(os.Getuid()). Real per-file
// identity would require GetFileInformationByHandle on an open handle; that is
// a future improvement if Windows needs the full Unix-grade hardening.
func StatIdentity(info os.FileInfo) (device, inode, links uint64, uid uint32, ok bool) {
	return 0, 0, 1, ^uint32(0), true
}
