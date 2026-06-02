package storagecheck

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyUnsupportedFilesystemTypes(t *testing.T) {
	for _, fsType := range []string{"nfs", "nfs4", "cifs", "smb3", "smbfs", "9p", "afpfs", "webdav", "sshfs", "fuse", "fuseblk", "fuse.sshfs", "fuse.rclone"} {
		t.Run(fsType, func(t *testing.T) {
			checker := Checker{
				Probe: fakeProbe(Fact{FilesystemType: fsType, Local: boolPtr(false), MountPoint: "/mnt/shared"}),
				Smoke: func(string) error {
					t.Fatal("smoke check should not run for unsupported storage")
					return nil
				},
			}
			results, err := checker.CheckRoots([]Root{{Role: RootConfig, Path: "/mnt/shared/clipfan"}})
			if err == nil || !errors.Is(err, ErrUnsupportedRuntimeStorage) {
				t.Fatalf("CheckRoots error = %v, want ErrUnsupportedRuntimeStorage", err)
			}
			if got := results[0].Code; got != CodeUnsupportedRuntimeStorage {
				t.Fatalf("Code = %q, want %q", got, CodeUnsupportedRuntimeStorage)
			}
		})
	}
}

func TestUnsupportedFilesystemMagicFailsClosed(t *testing.T) {
	for _, magic := range []int64{0x6969, 0x517b, 0xff534d42, 0x01021997, 0x65735546} {
		t.Run(fmt.Sprintf("0x%x", magic), func(t *testing.T) {
			checker := Checker{
				Probe: fakeProbe(Fact{FilesystemType: "mysteryfs", FilesystemMagic: magic, MountPoint: "/mnt/shared"}),
				Smoke: func(string) error {
					t.Fatal("smoke check should not run for unsupported storage")
					return nil
				},
			}
			results, err := checker.CheckRoots([]Root{{Role: RootConfig, Path: "/mnt/shared/clipfan"}})
			if err == nil || !errors.Is(err, ErrUnsupportedRuntimeStorage) {
				t.Fatalf("CheckRoots error = %v, want ErrUnsupportedRuntimeStorage", err)
			}
			if got := results[0].Code; got != CodeUnsupportedRuntimeStorage {
				t.Fatalf("Code = %q, want %q", got, CodeUnsupportedRuntimeStorage)
			}
		})
	}
}

func TestNetworkHomeClassificationFailsClosed(t *testing.T) {
	checker := Checker{
		Probe: fakeProbe(Fact{FilesystemType: "apfs", Local: boolPtr(false), MountPoint: "/Network/Users/me"}),
		Smoke: func(string) error {
			t.Fatal("smoke check should not run for non-local network homes")
			return nil
		},
	}

	results, err := checker.CheckRoots([]Root{{Role: RootState, Path: "/Network/Users/me/.clipfan"}})
	if err == nil || !errors.Is(err, ErrUnsupportedRuntimeStorage) {
		t.Fatalf("CheckRoots error = %v, want ErrUnsupportedRuntimeStorage", err)
	}
	if results[0].Reason != "volume_not_local" {
		t.Fatalf("Reason = %q, want volume_not_local", results[0].Reason)
	}
}

func TestClassifyCloudSyncRootsUnsupported(t *testing.T) {
	home := t.TempDir()
	cloudRoots := []string{
		filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs", "clipfan"),
		filepath.Join(home, "Library", "CloudStorage", "Dropbox", "clipfan"),
		filepath.Join(home, "Library", "CloudStorage", "GoogleDrive-me", "clipfan"),
		filepath.Join(home, "Dropbox", "clipfan"),
		filepath.Join(home, "Google Drive", "clipfan"),
		filepath.Join(home, "OneDrive", "clipfan"),
		filepath.Join(home, "Syncthing", "clipfan"),
		filepath.Join(home, "rclone", "clipfan"),
	}

	for _, root := range cloudRoots {
		t.Run(root, func(t *testing.T) {
			checker := Checker{
				HomeDir: home,
				Probe:   fakeProbe(Fact{FilesystemType: "apfs", Local: boolPtr(true), MountPoint: home}),
				Smoke: func(string) error {
					t.Fatal("smoke check should not run for cloud storage")
					return nil
				},
			}
			results, err := checker.CheckRoots([]Root{{Role: RootState, Path: root}})
			if err == nil || !errors.Is(err, ErrUnsupportedRuntimeStorage) {
				t.Fatalf("CheckRoots error = %v, want ErrUnsupportedRuntimeStorage", err)
			}
			if results[0].StorageClass != ClassCloudSync {
				t.Fatalf("StorageClass = %q, want %q", results[0].StorageClass, ClassCloudSync)
			}
		})
	}
}

func TestUnsupportedStorageHasNoUserOverride(t *testing.T) {
	t.Setenv("CLIPFAN_ALLOW_UNSUPPORTED_RUNTIME_STORAGE", "1")
	t.Setenv("CLIPFAN_STORAGE_LOCAL_OVERRIDE", "1")
	checker := Checker{
		Probe: fakeProbe(Fact{FilesystemType: "nfs", Local: boolPtr(false), MountPoint: "/mnt/network-home"}),
		Smoke: func(string) error {
			t.Fatal("smoke check should not run for unsupported storage")
			return nil
		},
	}

	results, err := checker.CheckRoots([]Root{{Role: RootState, Path: "/mnt/network-home/clipfan"}})
	if err == nil || !errors.Is(err, ErrUnsupportedRuntimeStorage) {
		t.Fatalf("CheckRoots error = %v, want ErrUnsupportedRuntimeStorage", err)
	}
	if results[0].Code != CodeUnsupportedRuntimeStorage {
		t.Fatalf("Code = %q, want %q", results[0].Code, CodeUnsupportedRuntimeStorage)
	}
}

func TestClassifyInconclusiveProbe(t *testing.T) {
	checker := Checker{
		Probe: func(string) (Fact, error) {
			return Fact{}, errors.New("mount table unreadable")
		},
		Smoke: func(string) error {
			t.Fatal("smoke check should not run when probe is inconclusive")
			return nil
		},
	}

	results, err := checker.CheckRoots([]Root{{Role: RootConfig, Path: "/tmp/clipfan"}})
	if err == nil || !errors.Is(err, ErrStorageCheckInconclusive) {
		t.Fatalf("CheckRoots error = %v, want ErrStorageCheckInconclusive", err)
	}
	if results[0].Code != CodeStorageCheckInconclusive {
		t.Fatalf("Code = %q, want %q", results[0].Code, CodeStorageCheckInconclusive)
	}
}

func TestLocalClassifiedRootRequiresSmokeCheck(t *testing.T) {
	root := t.TempDir()
	var smoked string
	checker := Checker{
		Probe: fakeProbe(Fact{FilesystemType: "apfs", Local: boolPtr(true), MountPoint: filepath.Dir(root)}),
		Smoke: func(path string) error {
			smoked = path
			return nil
		},
	}

	results, err := checker.CheckRoots([]Root{{Role: RootConfig, Path: root}})
	if err != nil {
		t.Fatal(err)
	}
	if smoked != root {
		t.Fatalf("smoked = %q, want %q", smoked, root)
	}
	if results[0].Code != CodeOK || results[0].StorageClass != ClassLocal {
		t.Fatalf("result = %#v, want local ok", results[0])
	}
	if results[0].CrossHostLockingClaimed {
		t.Fatal("local smoke check must not claim cross-host lock coordination")
	}
}

func TestLocalSmokeRejectsUnsafeRootAndExercisesAtomicRename(t *testing.T) {
	root := t.TempDir()
	if err := LocalSmokeCheck(root); err != nil {
		t.Fatalf("LocalSmokeCheck safe root: %v", err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == ".clipfan-storage-check.lock" ||
			strings.HasPrefix(name, ".clipfan-storage-check.tmp.") ||
			strings.HasPrefix(name, ".clipfan-storage-check.rename.") {
			t.Fatalf("smoke check left temp entry %s", name)
		}
	}

	unsafeRoot := filepath.Join(t.TempDir(), "unsafe")
	if err := os.Mkdir(unsafeRoot, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafeRoot, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := LocalSmokeCheck(unsafeRoot); err == nil {
		t.Fatal("LocalSmokeCheck returned nil for group/world-writable root")
	}
}

func TestCheckRuntimeRootsCoversConfigAndStateOnly(t *testing.T) {
	configRoot := t.TempDir()
	stateRoot := t.TempDir()
	calls := map[string]bool{}
	checker := Checker{
		Probe: fakeProbe(Fact{FilesystemType: "apfs", Local: boolPtr(true), MountPoint: filepath.Dir(configRoot)}),
		Smoke: func(path string) error {
			calls[path] = true
			return nil
		},
	}

	results, err := checker.CheckRoots(RuntimeRoots(configRoot, stateRoot))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if !calls[configRoot] || !calls[stateRoot] {
		t.Fatalf("smoke calls = %#v, want config and state roots only", calls)
	}
}

func fakeProbe(fact Fact) ProbeFunc {
	return func(string) (Fact, error) {
		return fact, nil
	}
}

func boolPtr(v bool) *bool {
	return &v
}
