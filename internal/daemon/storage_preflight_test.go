package daemon

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/storagecheck"
)

func TestStoragePreflightDefaultDormantForCurrentPublicGates(t *testing.T) {
	policy := DefaultStoragePreflightPolicy()
	if policy.Required {
		t.Fatal("current generated public gates should not require daemon storage preflight")
	}
}

func TestRunStoragePreflightUnsupportedBindsNoSocket(t *testing.T) {
	d := newStoragePreflightDaemon(t, storagecheck.Fact{
		FilesystemType: "nfs",
		Local:          boolPtr(false),
		MountPoint:     "/mnt/network-home",
	})
	served := false
	d.serve = func(context.Context) error {
		served = true
		return nil
	}

	err := d.Run(context.Background())
	if !errors.Is(err, storagecheck.ErrUnsupportedRuntimeStorage) {
		t.Fatalf("Run error = %v, want ErrUnsupportedRuntimeStorage", err)
	}
	if served {
		t.Fatal("daemon served despite unsupported storage")
	}
}

func TestRunStoragePreflightInconclusiveBindsNoHealthOnlySocket(t *testing.T) {
	d := newStoragePreflightDaemon(t, storagecheck.Fact{
		FilesystemType: "unknownfs",
		MountPoint:     "/mnt/mystery",
	})
	served := false
	d.serve = func(context.Context) error {
		served = true
		return nil
	}

	err := d.Run(context.Background())
	if !errors.Is(err, storagecheck.ErrStorageCheckInconclusive) {
		t.Fatalf("Run error = %v, want ErrStorageCheckInconclusive", err)
	}
	if served {
		t.Fatal("daemon served health-only loopback despite inconclusive storage")
	}
}

func TestRunStoragePreflightUnsupportedLeavesConfiguredAddressClosed(t *testing.T) {
	addr := freeTCPAddress(t)
	d := newStoragePreflightDaemon(t, storagecheck.Fact{
		FilesystemType: "nfs",
		Local:          boolPtr(false),
		MountPoint:     "/mnt/network-home",
	})
	d.cfg.Listen = addr
	d.sv = nil
	d.serve = func(context.Context) error {
		t.Fatal("serve should not run when storage preflight fails")
		return nil
	}

	err := d.Run(context.Background())
	if !errors.Is(err, storagecheck.ErrUnsupportedRuntimeStorage) {
		t.Fatalf("Run error = %v, want ErrUnsupportedRuntimeStorage", err)
	}
	conn, dialErr := net.DialTimeout("tcp", addr, 50*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		t.Fatalf("daemon bound %s despite unsupported storage", addr)
	}
}

func TestStoragePreflightRunsBeforeConfigWrites(t *testing.T) {
	d := newStoragePreflightDaemon(t, storagecheck.Fact{
		FilesystemType: "smbfs",
		Local:          boolPtr(false),
		MountPoint:     "/Volumes/shared",
	})

	err := d.setMaxHistory(300)
	if !errors.Is(err, storagecheck.ErrUnsupportedRuntimeStorage) {
		t.Fatalf("setMaxHistory error = %v, want ErrUnsupportedRuntimeStorage", err)
	}
}

func TestStoragePreflightAllowsLocalRootsAfterSmoke(t *testing.T) {
	configRoot := t.TempDir()
	stateRoot := t.TempDir()
	d, err := NewWithOptions(&config.Config{
		Listen:    "127.0.0.1:0",
		SharedKey: config.NewSharedKey(),
		Discovery: "static",
	}, Options{
		StoragePreflight: StoragePreflightPolicy{
			Required:   true,
			ConfigRoot: configRoot,
			StateRoot:  stateRoot,
			Checker: storagecheck.Checker{
				Probe: func(path string) (storagecheck.Fact, error) {
					return storagecheck.Fact{FilesystemType: "apfs", Local: boolPtr(true), MountPoint: filepath.Dir(path)}, nil
				},
				Smoke: func(string) error {
					return nil
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	served := false
	d.serve = func(context.Context) error {
		served = true
		return nil
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !served {
		t.Fatal("daemon did not serve after local storage preflight passed")
	}
}

func TestStoragePreflightDoesNotRepairUnsafeExistingStateRootBeforeSmoke(t *testing.T) {
	configRoot := t.TempDir()
	stateRoot := t.TempDir()
	if err := os.Chmod(stateRoot, 0o777); err != nil {
		t.Fatal(err)
	}
	d, err := NewWithOptions(&config.Config{
		Listen:    "127.0.0.1:0",
		SharedKey: config.NewSharedKey(),
		Discovery: "static",
	}, Options{
		StoragePreflight: StoragePreflightPolicy{
			Required:   true,
			ConfigRoot: configRoot,
			StateRoot:  stateRoot,
			Checker: storagecheck.Checker{
				Probe: func(path string) (storagecheck.Fact, error) {
					return storagecheck.Fact{FilesystemType: "apfs", Local: boolPtr(true), MountPoint: filepath.Dir(path)}, nil
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	d.serve = func(context.Context) error {
		t.Fatal("daemon served despite unsafe state root")
		return nil
	}

	err = d.Run(context.Background())
	if !errors.Is(err, storagecheck.ErrStorageCheckInconclusive) {
		t.Fatalf("Run error = %v, want ErrStorageCheckInconclusive", err)
	}
}

func newStoragePreflightDaemon(t *testing.T, fact storagecheck.Fact) *Daemon {
	t.Helper()
	configRoot := t.TempDir()
	stateRoot := t.TempDir()
	d, err := NewWithOptions(&config.Config{
		Listen:    "127.0.0.1:0",
		SharedKey: config.NewSharedKey(),
		Discovery: "static",
	}, Options{
		StoragePreflight: StoragePreflightPolicy{
			Required:   true,
			ConfigRoot: configRoot,
			StateRoot:  stateRoot,
			Checker: storagecheck.Checker{
				Probe: func(string) (storagecheck.Fact, error) {
					return fact, nil
				},
				Smoke: func(string) error {
					t.Fatal("smoke check should not run for failing storage")
					return nil
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func boolPtr(v bool) *bool {
	return &v
}

func freeTCPAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}
