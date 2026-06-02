//go:build darwin || linux

package storagecheck

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

func LocalSmokeCheck(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.Mode().IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("root is not a real directory")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("root is group/world writable: %o", info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("owner check unavailable")
	}
	if stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("root owner uid %d does not match current uid %d", stat.Uid, os.Getuid())
	}

	lockPath := filepath.Join(root, ".clipfan-storage-check.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lockFile.Close()
	defer os.Remove(lockPath)
	if err := lockFile.Chmod(0o600); err != nil {
		return err
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	suffix := strconv.Itoa(os.Getpid())
	tmp := filepath.Join(root, ".clipfan-storage-check.tmp."+suffix)
	final := filepath.Join(root, ".clipfan-storage-check.rename."+suffix)
	_ = os.Remove(tmp)
	_ = os.Remove(final)
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmp)
			_ = os.Remove(final)
		}
	}()
	if _, err := file.Write([]byte("clipfan storage check\n")); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if err := syncFileBestEffort(file); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	if err := syncDirBestEffort(root); err != nil {
		return err
	}
	if err := os.Remove(final); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func syncFileBestEffort(file *os.File) error {
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
