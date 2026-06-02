//go:build !darwin && !linux

package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

var (
	ErrDaemonAlreadyRunning = errors.New("daemon_already_running")
	ErrDaemonLockTimeout    = errors.New("daemon_lock_timeout")
	ErrDaemonLockUnsafe     = errors.New("daemon_lock_unsafe")
	ErrDaemonPortConflict   = errors.New("daemon_port_conflict")
)

type daemonLock struct{}

type daemonLockDiagnostics struct {
	PID           int    `json:"pid"`
	StartedAt     string `json:"started_at"`
	ConfigPath    string `json:"config_path"`
	StateDir      string `json:"state_dir"`
	Listen        string `json:"listen"`
	DaemonVersion string `json:"daemon_version"`
	Hostname      string `json:"hostname"`
}

func acquireDaemonLock(string) (*daemonLock, error) {
	return &daemonLock{}, nil
}

func (l *daemonLock) writeDiagnostics(daemonLockDiagnostics) error { return nil }

func (l *daemonLock) release() {}

func WaitForDaemonLockRelease(context.Context, string) error {
	return nil
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
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "address already in use") || strings.Contains(text, "only one usage")
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
