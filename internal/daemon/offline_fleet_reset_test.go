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

func TestOfflineLocalFleetResetStopsServiceWaitsForLockThenResets(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "clipfan-state")
	lock, err := acquireDaemonLock(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()
	configPath := filepath.Join(t.TempDir(), "config.json")
	events := make(chan string, 4)
	release := make(chan struct{})
	wantBackup := filepath.Join(filepath.Dir(configPath), "config.json.fleet-reset.20260602T203000Z.bak")
	gotBackup := ""
	gotReturnedBackup := ""
	req := config.LocalFleetResetRequest{Confirmation: config.LocalFleetResetConfirmation, ExpectedRevisionState: config.RevisionStatePreV2}
	go func() {
		<-release
		lock.release()
	}()

	done := make(chan error, 1)
	go func() {
		_, backupPath, err := OfflineLocalFleetReset(context.Background(), OfflineLocalFleetResetOptions{
			ConfigPath:  configPath,
			StateDir:    stateRoot,
			Request:     req,
			WaitTimeout: time.Second,
			Now:         func() time.Time { return time.Date(2026, 6, 2, 20, 30, 0, 0, time.UTC) },
			StopService: func(context.Context) error { events <- "stop"; return nil },
			reset: func(path, stateDir string, gotReq config.LocalFleetResetRequest, backup string) (config.LocalFleetResetResult, error) {
				gotBackup = backup
				events <- "reset"
				racedLock, lockErr := acquireDaemonLock(stateRoot)
				if racedLock != nil {
					racedLock.release()
				}
				if !errors.Is(lockErr, ErrDaemonAlreadyRunning) {
					t.Errorf("daemon lock during reset = %v, want ErrDaemonAlreadyRunning", lockErr)
				}
				if path != configPath || stateDir != stateRoot || gotReq != req {
					t.Errorf("reset args = %q %q %#v, want %q %q %#v", path, stateDir, gotReq, configPath, stateRoot, req)
				}
				if backup != wantBackup {
					t.Errorf("backup path = %q, want %q", backup, wantBackup)
				}
				return config.LocalFleetResetResult{HostID: "m4"}, nil
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
		t.Fatalf("reset ran before daemon lock release: %q", got)
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("OfflineLocalFleetReset: %v", err)
	}
	if got := <-events; got != "reset" {
		t.Fatalf("second event = %q, want reset", got)
	}
	if gotBackup != wantBackup || gotReturnedBackup != wantBackup {
		t.Fatalf("backup path = %q/%q, want %q", gotBackup, gotReturnedBackup, wantBackup)
	}
	afterResetLock, err := acquireDaemonLock(stateRoot)
	if err != nil {
		t.Fatalf("daemon lock after reset = %v, want released", err)
	}
	afterResetLock.release()
}

func TestOfflineLocalFleetResetLockTimeoutSkipsReset(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "clipfan-state")
	lock, err := acquireDaemonLock(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()
	resetCalled := false

	_, _, err = OfflineLocalFleetReset(context.Background(), OfflineLocalFleetResetOptions{
		ConfigPath:  filepath.Join(t.TempDir(), "config.json"),
		StateDir:    stateRoot,
		Request:     config.LocalFleetResetRequest{Confirmation: config.LocalFleetResetConfirmation, ExpectedRevisionState: config.RevisionStatePreV2},
		WaitTimeout: 10 * time.Millisecond,
		StopService: func(context.Context) error { return nil },
		reset: func(string, string, config.LocalFleetResetRequest, string) (config.LocalFleetResetResult, error) {
			resetCalled = true
			return config.LocalFleetResetResult{}, nil
		},
	})
	if !errors.Is(err, ErrDaemonLockTimeout) {
		t.Fatalf("OfflineLocalFleetReset error = %v, want daemon_lock_timeout", err)
	}
	if resetCalled {
		t.Fatal("reset ran despite daemon lock timeout")
	}
}

func TestOfflineLocalFleetResetStopFailureSkipsReset(t *testing.T) {
	stopErr := errors.New("stop failed")
	resetCalled := false

	_, _, err := OfflineLocalFleetReset(context.Background(), OfflineLocalFleetResetOptions{
		ConfigPath:  filepath.Join(t.TempDir(), "config.json"),
		StateDir:    filepath.Join(t.TempDir(), "clipfan-state"),
		Request:     config.LocalFleetResetRequest{Confirmation: config.LocalFleetResetConfirmation, ExpectedRevisionState: config.RevisionStatePreV2},
		StopService: func(context.Context) error { return stopErr },
		reset: func(string, string, config.LocalFleetResetRequest, string) (config.LocalFleetResetResult, error) {
			resetCalled = true
			return config.LocalFleetResetResult{}, nil
		},
	})
	if !errors.Is(err, stopErr) {
		t.Fatalf("OfflineLocalFleetReset error = %v, want stop failure", err)
	}
	if resetCalled {
		t.Fatal("reset ran despite stop failure")
	}
}
