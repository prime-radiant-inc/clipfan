//go:build darwin || linux

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

func TestRunWritesDaemonLockDiagnosticsAfterAcquire(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateRoot)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d := newLockTestDaemon(t)
	d.serve = func(context.Context) error {
		diag := readDaemonLockDiagnostics(t, stateRoot)
		if diag["pid"].(float64) != float64(os.Getpid()) {
			t.Fatalf("pid diagnostic = %#v, want %d", diag["pid"], os.Getpid())
		}
		if diag["listen"] != d.listenerPlan.BindListen {
			t.Fatalf("listen diagnostic = %#v, want %q", diag["listen"], d.listenerPlan.BindListen)
		}
		for _, key := range []string{"started_at", "config_path", "state_dir", "daemon_version", "hostname"} {
			if diag[key] == "" {
				t.Fatalf("missing diagnostic key %s in %#v", key, diag)
			}
		}
		return nil
	}

	if err := d.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertDaemonLockMode(t, stateRoot)
}

func TestRunRejectsConcurrentDaemonAndKeepsExistingDiagnostics(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateRoot)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	first := newLockTestDaemon(t)
	started := make(chan struct{})
	release := make(chan struct{})
	first.serve = func(context.Context) error {
		close(started)
		<-release
		return nil
	}
	errCh := make(chan error, 1)
	go func() { errCh <- first.Run(context.Background()) }()
	<-started

	lockPath := filepath.Join(stateRoot, "clipfan", "daemon.lock")
	before, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	second := newLockTestDaemon(t)
	servedSecond := false
	second.serve = func(context.Context) error {
		servedSecond = true
		return nil
	}
	err = second.Run(context.Background())
	if !errors.Is(err, ErrDaemonAlreadyRunning) || !strings.Contains(err.Error(), "daemon_already_running") {
		t.Fatalf("second Run error = %v, want daemon_already_running", err)
	}
	if servedSecond {
		t.Fatal("second daemon served despite held daemon lock")
	}
	after, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("failed lock acquisition rewrote diagnostics\nbefore=%s\nafter=%s", before, after)
	}

	close(release)
	if err := <-errCh; err != nil {
		t.Fatalf("first Run: %v", err)
	}
}

func TestWaitForDaemonLockReleaseTimesOut(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "clipfan")
	lock, err := acquireDaemonLock(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err = WaitForDaemonLockRelease(ctx, stateRoot)
	if !errors.Is(err, ErrDaemonLockTimeout) || !strings.Contains(err.Error(), "daemon_lock_timeout") {
		t.Fatalf("WaitForDaemonLockRelease error = %v, want daemon_lock_timeout", err)
	}
}

func TestAcquireDaemonLockRejectsSymlinkStateDir(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	stateRoot := filepath.Join(root, "clipfan")
	if err := os.Symlink(target, stateRoot); err != nil {
		t.Fatal(err)
	}

	lock, err := acquireDaemonLock(stateRoot)
	if lock != nil {
		lock.release()
	}
	if !errors.Is(err, ErrDaemonLockUnsafe) {
		t.Fatalf("acquireDaemonLock error = %v, want daemon_lock_unsafe", err)
	}
	if _, err := os.Lstat(filepath.Join(target, "daemon.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("symlink target lock path exists after rejected lock: %v", err)
	}
}

func TestAcquireDaemonLockRejectsSymlinkLockFileWithoutWritingTarget(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "clipfan")
	if err := os.Mkdir(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target-lock")
	original := []byte("do not replace")
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(stateRoot, "daemon.lock")); err != nil {
		t.Fatal(err)
	}

	lock, err := acquireDaemonLock(stateRoot)
	if lock != nil {
		lock.release()
	}
	if !errors.Is(err, ErrDaemonLockUnsafe) {
		t.Fatalf("acquireDaemonLock error = %v, want daemon_lock_unsafe", err)
	}
	after, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(original) {
		t.Fatalf("symlink target was modified: %q", after)
	}
}

func TestAcquireDaemonLockRejectsHardLinkedLockFileWithoutWritingTarget(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "clipfan")
	if err := os.Mkdir(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target-lock")
	original := []byte("do not replace")
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, filepath.Join(stateRoot, "daemon.lock")); err != nil {
		t.Fatal(err)
	}

	lock, err := acquireDaemonLock(stateRoot)
	if lock != nil {
		lock.release()
	}
	if !errors.Is(err, ErrDaemonLockUnsafe) {
		t.Fatalf("acquireDaemonLock error = %v, want daemon_lock_unsafe", err)
	}
	after, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(original) {
		t.Fatalf("hard link target was modified: %q", after)
	}
}

func TestRunLoopbackPortConflictReturnsStableErrorBeforeClipboardRead(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	d, err := NewWithOptions(&config.Config{
		Listen:    net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		SharedKey: config.NewSharedKey(),
		Discovery: "static",
	}, Options{ListenerBoundaryEnabled: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}
	if d.listenerPlan.SafeMode {
		t.Fatal("test daemon unexpectedly entered safe mode")
	}
	clipboardProbe := &countingClipboardBackend{}
	d.cb = clipboardProbe

	err = d.Run(context.Background())
	if !errors.Is(err, ErrDaemonPortConflict) || !strings.Contains(err.Error(), "daemon_port_conflict") {
		t.Fatalf("Run error = %v, want daemon_port_conflict", err)
	}
	if got := clipboardProbe.count(); got != 0 {
		t.Fatalf("clipboard reads before bind conflict = %d, want 0", got)
	}
}

func newLockTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	d, err := NewWithOptions(&config.Config{
		Listen:    "0.0.0.0:9000",
		SharedKey: config.NewSharedKey(),
		Discovery: "static",
	}, Options{ListenerBoundaryEnabled: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}
	if !d.listenerPlan.SafeMode {
		t.Fatal("test daemon did not enter safe mode")
	}
	return d
}

func readDaemonLockDiagnostics(t *testing.T, stateRoot string) map[string]any {
	t.Helper()
	lockPath := filepath.Join(stateRoot, "clipfan", "daemon.lock")
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	var diag map[string]any
	if err := json.Unmarshal(data, &diag); err != nil {
		t.Fatalf("decode diagnostics %q: %v", data, err)
	}
	return diag
}

func assertDaemonLockMode(t *testing.T, stateRoot string) {
	t.Helper()
	lockPath := filepath.Join(stateRoot, "clipfan", "daemon.lock")
	info, err := os.Lstat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("daemon lock mode = %o, want 600", got)
	}
	dirInfo, err := os.Lstat(filepath.Dir(lockPath))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("state dir mode = %o, want 700", got)
	}
}
