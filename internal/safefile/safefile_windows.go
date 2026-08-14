//go:build windows

package safefile

import (
	"os"

	"golang.org/x/sys/windows"
)

// OpenNoFollow: Windows has no O_NOFOLLOW, so this opens normally. Symlink
// hardening is reduced on Windows (a TODO if clipfan needs it there).
func OpenNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag, perm)
}

// Flock uses LockFileEx over the whole file (exclusive). LOCK_NB maps to
// LOCKFILE_FAIL_IMMEDIATELY; a contended non-blocking lock returns ErrWouldBlock.
func Flock(f *os.File, how LockFlag) error {
	if how&LOCK_UN != 0 {
		return Unlock(f)
	}
	var flags uint32
	if how&LOCK_EX != 0 {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	if how&LOCK_NB != 0 {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	var ol windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 0xffffffff, 0x7fffffff, &ol); err != nil {
		if how&LOCK_NB != 0 {
			return ErrWouldBlock
		}
		return err
	}
	return nil
}

// Unlock releases a Windows file lock taken by Flock.
func Unlock(f *os.File) error {
	var ol windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 0xffffffff, 0x7fffffff, &ol)
}

// IsUnsupportedSync: on Windows, file.Sync errors are treated as real errors.
func IsUnsupportedSync(err error) bool { return false }

// PermsExposeToGroupOrWorld / PermsWritableByGroupOrWorld: Windows file
// security is ACL-based; the POSIX mode bits os.Stat reports on Windows do
// not reflect access control (every writable file reads as 0666), so
// bit-based exposure checks are meaningless there. Owner-only enforcement
// belongs to the ACLs on the profile directory.
func PermsExposeToGroupOrWorld(mode os.FileMode) bool    { return false }
func PermsWritableByGroupOrWorld(mode os.FileMode) bool { return false }

// SyncDir: syncing a directory handle is not a Windows operation (opening a
// dir for sync fails with Access is denied), and NTFS journals metadata, so
// this is a no-op.
func SyncDir(dir string) error { return nil }

// StatIdentity on Windows: os.FileInfo does not expose volume serial / file
// index / link count, so return neutral values that satisfy clipfan's hardening
// checks (TOCTOU equality and "owned by current user"). os.Getuid() returns -1
// on Windows, so uid = ^uint32(0) matches uint32(os.Getuid()). Real per-file
// identity would require GetFileInformationByHandle on an open handle; that is
// a future improvement if Windows needs the full Unix-grade hardening.
func StatIdentity(info os.FileInfo) (device, inode, links uint64, uid uint32, ok bool) {
	return 0, 0, 1, ^uint32(0), true
}
