//go:build darwin || linux

package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

func TestOfflineListenerRepairStopsServiceWaitsForLockThenRepairs(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "clipfan-state")
	lock, err := acquireDaemonLock(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()
	configPath := filepath.Join(t.TempDir(), "config.json")
	events := make(chan string, 4)
	release := make(chan struct{})
	wantBackup := filepath.Join(filepath.Dir(configPath), "config.json.listener-repair.20260602T203000Z.bak")
	gotBackup := ""
	gotReturnedBackup := ""
	go func() {
		<-release
		lock.release()
	}()

	done := make(chan error, 1)
	go func() {
		_, backupPath, err := OfflineListenerRepair(context.Background(), OfflineListenerRepairOptions{
			ConfigPath:  configPath,
			StateDir:    stateRoot,
			Request:     listenerRepairTestRequest(),
			WaitTimeout: time.Second,
			Now:         func() time.Time { return time.Date(2026, 6, 2, 20, 30, 0, 0, time.UTC) },
			StopService: func(context.Context) error { events <- "stop"; return nil },
			repair: func(path string, req config.ListenerRepairRequest, backup string) (config.ListenerRepairStatus, error) {
				gotBackup = backup
				events <- "repair"
				racedLock, lockErr := acquireDaemonLock(stateRoot)
				if racedLock != nil {
					racedLock.release()
				}
				if !errors.Is(lockErr, ErrDaemonAlreadyRunning) {
					t.Errorf("daemon lock during repair = %v, want ErrDaemonAlreadyRunning", lockErr)
				}
				if path != configPath {
					t.Errorf("repair path = %q, want %q", path, configPath)
				}
				if backup != wantBackup {
					t.Errorf("backup path = %q, want %q", backup, wantBackup)
				}
				return config.ListenerRepairStatus{Listen: "127.0.0.1:9000"}, nil
			},
		})
		gotReturnedBackup = backupPath
		done <- err
	}()

	if got := <-events; got != "stop" {
		t.Fatalf("first event = %q, want stop", got)
	}
	select {
	case got := <-events:
		t.Fatalf("repair ran before daemon lock release: %q", got)
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("OfflineListenerRepair: %v", err)
	}
	if got := <-events; got != "repair" {
		t.Fatalf("second event = %q, want repair", got)
	}
	if gotBackup != wantBackup || gotReturnedBackup != wantBackup {
		t.Fatalf("backup path = %q/%q, want %q", gotBackup, gotReturnedBackup, wantBackup)
	}
	afterRepairLock, err := acquireDaemonLock(stateRoot)
	if err != nil {
		t.Fatalf("daemon lock after repair = %v, want released", err)
	}
	afterRepairLock.release()
}

func TestOfflineListenerRepairLockTimeoutSkipsRepair(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "clipfan-state")
	lock, err := acquireDaemonLock(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()
	repairCalled := false

	_, _, err = OfflineListenerRepair(context.Background(), OfflineListenerRepairOptions{
		ConfigPath:  filepath.Join(t.TempDir(), "config.json"),
		StateDir:    stateRoot,
		Request:     listenerRepairTestRequest(),
		WaitTimeout: 10 * time.Millisecond,
		StopService: func(context.Context) error { return nil },
		repair: func(string, config.ListenerRepairRequest, string) (config.ListenerRepairStatus, error) {
			repairCalled = true
			return config.ListenerRepairStatus{}, nil
		},
	})
	if !errors.Is(err, ErrDaemonLockTimeout) {
		t.Fatalf("OfflineListenerRepair error = %v, want daemon_lock_timeout", err)
	}
	if repairCalled {
		t.Fatal("repair ran despite daemon lock timeout")
	}
}

func TestOfflineListenerRepairStopFailureSkipsRepair(t *testing.T) {
	stopErr := errors.New("stop failed")
	repairCalled := false

	_, _, err := OfflineListenerRepair(context.Background(), OfflineListenerRepairOptions{
		ConfigPath:  filepath.Join(t.TempDir(), "config.json"),
		StateDir:    filepath.Join(t.TempDir(), "clipfan-state"),
		Request:     listenerRepairTestRequest(),
		StopService: func(context.Context) error { return stopErr },
		repair: func(string, config.ListenerRepairRequest, string) (config.ListenerRepairStatus, error) {
			repairCalled = true
			return config.ListenerRepairStatus{}, nil
		},
	})
	if !errors.Is(err, stopErr) {
		t.Fatalf("OfflineListenerRepair error = %v, want stop failure", err)
	}
	if repairCalled {
		t.Fatal("repair ran despite stop failure")
	}
}

func listenerRepairTestRequest() config.ListenerRepairRequest {
	previous := "0.0.0.0:9000"
	return config.ListenerRepairRequest{
		ExpectedRevisionState: config.RevisionStatePreV2,
		Listen:                "127.0.0.1:9000",
		Port:                  9000,
		PreviousListen:        &previous,
	}
}
