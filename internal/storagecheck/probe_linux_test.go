//go:build linux

package storagecheck

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathWithinMountIncludesRootMount(t *testing.T) {
	if !pathWithinMount("/var/lib/clipfan", "/") {
		t.Fatal("root mount should match absolute paths")
	}
	if pathWithinMount("/var/lib/clipfan", "/var/lib/clip") {
		t.Fatal("mount matching should honor path boundaries")
	}
	if !pathWithinMount("/var/lib/clipfan", "/var/lib/clipfan") {
		t.Fatal("mount should match itself")
	}
}

func TestReadLinuxMountInfoUnescapesMountPoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mountinfo")
	if err := os.WriteFile(path, []byte("26 20 0:22 / / rw,relatime - ext4 /dev/root rw\n27 20 0:23 / /home/me/Google\\040Drive rw,relatime - fuse.sshfs sshfs rw\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	mounts, err := readLinuxMountInfo(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 2 {
		t.Fatalf("len(mounts) = %d, want 2", len(mounts))
	}
	if mounts[1].MountPoint != "/home/me/Google Drive" {
		t.Fatalf("MountPoint = %q, want unescaped path", mounts[1].MountPoint)
	}
	if mounts[1].FilesystemType != "fuse.sshfs" {
		t.Fatalf("FilesystemType = %q, want fuse.sshfs", mounts[1].FilesystemType)
	}
}
