// Package safefile provides cross-platform helpers for the secure-file and
// advisory-locking patterns clipfan uses: opening a path without following a
// trailing symlink, taking an exclusive lock on an open file, and reading the
// device/inode/links/owner identity of a file.
//
// On Unix these map to O_NOFOLLOW, flock(2), and stat(2). On Windows there is
// no direct O_NOFOLLOW and ownership is SID-based; the helpers provide a
// best-effort equivalent (LockFileEx; symlink/owner hardening is reduced) so
// clipfan compiles and runs while remaining safe on Unix.
package safefile

import "errors"

// Errors returned across platforms.
var (
	// ErrSymlink is returned by OpenNoFollow when the path is a symlink.
	ErrSymlink = errors.New("path is a symlink")
	// ErrWouldBlock is returned by Flock when LOCK_NB was requested and the
	// lock is held by another process.
	ErrWouldBlock = errors.New("resource temporarily unavailable")
)

// LockFlag is a bit-set of the LOCK_* constants below.
type LockFlag uint8

// Advisory lock operation flags (mirror POSIX flock(2) semantics).
const (
	LOCK_SH LockFlag = 1 << 0
	LOCK_EX LockFlag = 1 << 1
	LOCK_NB LockFlag = 1 << 2
	LOCK_UN LockFlag = 1 << 3
)
