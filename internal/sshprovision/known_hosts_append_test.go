package sshprovision

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendKnownHostsLinesCreatesFile0600(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	path := filepath.Join(dir, "regular_known_hosts")
	line := "host.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAISAMPLE"

	if err := AppendKnownHostsLines(path, []string{line}); err != nil {
		t.Fatalf("AppendKnownHostsLines() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), line) {
		t.Fatalf("file missing appended line: %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestAppendKnownHostsLinesPreservesExistingAndForces0600(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	path := filepath.Join(dir, "regular_known_hosts")
	// A real user's ~/.ssh/known_hosts is routinely 0644 — must not be rejected,
	// and must be tightened to 0600 on write.
	if err := os.WriteFile(path, []byte("existing.example ssh-rsa AAAAEXISTING\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	line := "host.example ssh-ed25519 AAAANEW"

	if err := AppendKnownHostsLines(path, []string{line}); err != nil {
		t.Fatalf("AppendKnownHostsLines() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "existing.example") || !strings.Contains(string(data), line) {
		t.Fatalf("file lost content: %q", data)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestAppendKnownHostsLinesRejectsControlChars(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	path := filepath.Join(dir, "regular_known_hosts")
	if err := AppendKnownHostsLines(path, []string{"injected.example ssh-ed25519 AAAA\nevil.example ssh-rsa BBBB"}); err == nil {
		t.Fatalf("expected error for embedded newline in line")
	}
}

func TestAppendKnownHostsLinesEmptyIsNoop(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	path := filepath.Join(dir, "regular_known_hosts")
	if err := AppendKnownHostsLines(path, nil); err != nil {
		t.Fatalf("AppendKnownHostsLines(nil) error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("empty append should not create the file, stat err = %v", err)
	}
}
