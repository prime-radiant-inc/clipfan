//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var (
	ErrDaemonAlreadyRunning = errors.New("daemon_already_running")
	ErrDaemonLockTimeout    = errors.New("daemon_lock_timeout")
	ErrDaemonLockUnsafe     = errors.New("daemon_lock_unsafe")
	ErrDaemonPortConflict   = errors.New("daemon_port_conflict")
)

type daemonLock struct {
	file *os.File
}

type daemonLockDiagnostics struct {
	PID           int    `json:"pid"`
	StartedAt     string `json:"started_at"`
	ConfigPath    string `json:"config_path"`
	StateDir      string `json:"state_dir"`
	Listen        string `json:"listen"`
	DaemonVersion string `json:"daemon_version"`
	Hostname      string `json:"hostname"`
}

func acquireDaemonLock(stateDir string) (*daemonLock, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	if err := validateDaemonLockStateDir(stateDir); err != nil {
		return nil, err
	}
	if err := os.Chmod(stateDir, 0o700); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(stateDir, "daemon.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("%w: lock path is symlink: %s", ErrDaemonLockUnsafe, lockPath)
		}
		return nil, err
	}
	if err := validateDaemonLockFile(lockPath, f); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("%w: %s", ErrDaemonAlreadyRunning, lockPath)
		}
		return nil, err
	}
	return &daemonLock{file: f}, nil
}

func validateDaemonLockStateDir(stateDir string) error {
	info, err := os.Lstat(stateDir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: state directory is symlink: %s", ErrDaemonLockUnsafe, stateDir)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: state path is not a directory: %s", ErrDaemonLockUnsafe, stateDir)
	}
	return nil
}

func validateDaemonLockFile(lockPath string, f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: lock path is not regular file: %s", ErrDaemonLockUnsafe, lockPath)
	}
	openedIdentity, err := daemonLockFileIdentity(info)
	if err != nil {
		return err
	}
	if openedIdentity.linkCount > 1 {
		return fmt.Errorf("%w: lock path has multiple links: %s", ErrDaemonLockUnsafe, lockPath)
	}
	pathInfo, err := os.Lstat(lockPath)
	if err != nil {
		return err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: lock path is symlink: %s", ErrDaemonLockUnsafe, lockPath)
	}
	if !pathInfo.Mode().IsRegular() {
		return fmt.Errorf("%w: lock path is not regular file: %s", ErrDaemonLockUnsafe, lockPath)
	}
	pathIdentity, err := daemonLockFileIdentity(pathInfo)
	if err != nil {
		return err
	}
	if openedIdentity.device != pathIdentity.device || openedIdentity.inode != pathIdentity.inode {
		return fmt.Errorf("%w: lock path changed during open: %s", ErrDaemonLockUnsafe, lockPath)
	}
	return nil
}

type daemonLockIdentity struct {
	device    uint64
	inode     uint64
	linkCount uint64
}

func daemonLockFileIdentity(info os.FileInfo) (daemonLockIdentity, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return daemonLockIdentity{}, fmt.Errorf("%w: could not inspect lock path identity", ErrDaemonLockUnsafe)
	}
	return daemonLockIdentity{
		device:    uint64(stat.Dev),
		inode:     uint64(stat.Ino),
		linkCount: uint64(stat.Nlink),
	}, nil
}

func (l *daemonLock) writeDiagnostics(diag daemonLockDiagnostics) error {
	if l == nil || l.file == nil {
		return errors.New("daemon lock not held")
	}
	body, err := json.MarshalIndent(diag, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if err := l.file.Truncate(0); err != nil {
		return err
	}
	if _, err := l.file.Seek(0, 0); err != nil {
		return err
	}
	if _, err := l.file.Write(body); err != nil {
		return err
	}
	return l.file.Sync()
}

func (l *daemonLock) release() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}

func WaitForDaemonLockRelease(ctx context.Context, stateDir string) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		lock, err := acquireDaemonLock(stateDir)
		if err == nil {
			lock.release()
			return nil
		}
		if !errors.Is(err, ErrDaemonAlreadyRunning) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %s", ErrDaemonLockTimeout, filepath.Join(stateDir, "daemon.lock"))
		case <-ticker.C:
		}
	}
}

func (d *Daemon) normalizeRunError(err error) error {
	if err == nil {
		return nil
	}
	if isLoopbackListen(d.listenerPlan.BindListen) && isAddressInUse(err) {
		return fmt.Errorf("%w: %v", ErrDaemonPortConflict, err)
	}
	return err
}

func isAddressInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE) || strings.Contains(err.Error(), "address already in use")
}

func isLoopbackListen(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
