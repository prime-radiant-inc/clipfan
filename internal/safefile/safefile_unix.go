//go:build !windows

package safefile

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// OpenNoFollow opens path with flag/perm, refusing to follow a trailing
// symlink (O_NOFOLLOW). A symlink target is reported as ErrSymlink.
func OpenNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	f, err := os.OpenFile(path, flag|syscall.O_NOFOLLOW, perm)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("%w: %s", ErrSymlink, path)
		}
		return nil, err
	}
	return f, nil
}

// Flock applies an advisory lock. how is a bit-set of LOCK_SH/LOCK_EX with
// optional LOCK_NB (LOCK_UN releases via Unlock). Returns ErrWouldBlock when
// LOCK_NB is set and the lock is contended.
func Flock(f *os.File, how LockFlag) error {
	if how&LOCK_UN != 0 {
		return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}
	mode := 0
	switch {
	case how&LOCK_EX != 0:
		mode = syscall.LOCK_EX
	case how&LOCK_SH != 0:
		mode = syscall.LOCK_SH
	}
	if how&LOCK_NB != 0 {
		mode |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), mode); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return ErrWouldBlock
		}
		return err
	}
	return nil
}

// Unlock releases an advisory lock held on f.
func Unlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// StatIdentity returns the device, inode, hard-link count, and owner uid of a
// file from its FileInfo. ok is false if the underlying stat data is unavailable.
func StatIdentity(info os.FileInfo) (device, inode, links uint64, uid uint32, ok bool) {
	stat, alright := info.Sys().(*syscall.Stat_t)
	if !alright {
		return 0, 0, 0, 0, false
	}
	return uint64(stat.Dev), uint64(stat.Ino), uint64(stat.Nlink), uint32(stat.Uid), true
}

// IsUnsupportedSync reports whether err is an errno indicating the filesystem
// does not support fsync (e.g. EINVAL/ENOTSUP/ENOSYS on tmpfs/special FS).
func IsUnsupportedSync(err error) bool {
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.ENOSYS)
}
