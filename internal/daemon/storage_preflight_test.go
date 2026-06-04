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

func TestStoragePreflightDefaultRequiredForCurrentCutoverGates(t *testing.T) {
	policy := DefaultStoragePreflightPolicy()
	if !policy.Required {
		t.Fatal("current generated cutover gates should require daemon storage preflight")
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

func TestStoragePreflightDoesNotCreateMissingStateRootUnderUnsupportedParent(t *testing.T) {
	home := t.TempDir()
	configRoot := t.TempDir()
	cloudRoot := filepath.Join(home, "Dropbox")
	if err := os.MkdirAll(cloudRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	stateRoot := filepath.Join(cloudRoot, "clipfan")

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
				HomeDir: home,
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
	d.serve = func(context.Context) error {
		t.Fatal("daemon served despite unsupported state root parent")
		return nil
	}

	err = d.Run(context.Background())
	if !errors.Is(err, storagecheck.ErrUnsupportedRuntimeStorage) {
		t.Fatalf("Run error = %v, want ErrUnsupportedRuntimeStorage", err)
	}
	if _, statErr := os.Lstat(stateRoot); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("state root stat error = %v, want not exist", statErr)
	}
}

func TestStoragePreflightCreatesMissingStateRootOnlyAfterLocalParentPasses(t *testing.T) {
	configRoot := t.TempDir()
	parent := t.TempDir()
	stateRoot := filepath.Join(parent, "clipfan")
	smoked := map[string]bool{}

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
				Smoke: func(path string) error {
					smoked[path] = true
					if filepath.Base(path) == "clipfan" {
						if _, err := os.Lstat(stateRoot); err != nil {
							t.Fatalf("state root was not created before smoke: %v", err)
						}
					}
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
		t.Fatal("daemon did not serve after local storage parent and state root passed")
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	resolvedStateRoot, err := filepath.EvalSymlinks(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !smoked[resolvedParent] || !smoked[resolvedStateRoot] {
		t.Fatalf("smoked = %#v, want parent and created state root", smoked)
	}
	info, err := os.Lstat(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("state root mode = %o, want 700", got)
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
