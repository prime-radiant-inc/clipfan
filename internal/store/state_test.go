package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadStateText(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	want := State{Kind: "text", TS: time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)}
	if err := SaveState(want, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != want.Kind || !got.TS.Equal(want.TS) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	text, err := LoadText()
	if err != nil {
		t.Fatal(err)
	}
	if string(text) != "hello" {
		t.Fatalf("text = %q, want hello", text)
	}
}

func TestLoadStateMissingIsEmpty(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if s.Kind != "" {
		t.Fatalf("expected zero state, got %+v", s)
	}
}

func TestSaveStateUsesPrivatePermissions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	want := State{Kind: "text", TS: time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)}
	if err := SaveState(want, []byte("secret")); err != nil {
		t.Fatal(err)
	}
	assertMode(t, filepath.Join(root, "clipfan"), 0o700)
	assertMode(t, filepath.Join(root, "clipfan", "state.json"), 0o600)
	assertMode(t, filepath.Join(root, "clipfan", "current.txt"), 0o600)
}

func TestSaveStateRejectsSymlinkedStateDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	targetDir := filepath.Join(root, "target")
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetDir, filepath.Join(root, "clipfan")); err != nil {
		t.Fatal(err)
	}

	want := State{Kind: "text", TS: time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)}
	if err := SaveState(want, []byte("secret")); err == nil {
		t.Fatal("SaveState returned nil error for symlinked state directory")
	}
}

func TestSaveStateDoesNotFollowTempSymlink(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	dir := filepath.Join(root, "clipfan")
	target := filepath.Join(root, "target.json")
	tmp := filepath.Join(dir, "state.json.tmp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, tmp); err != nil {
		t.Fatal(err)
	}

	want := State{Kind: "text", TS: time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)}
	if err := SaveState(want, []byte("secret")); err != nil {
		t.Fatal(err)
	}
	targetData, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(targetData) != "keep" {
		t.Fatalf("temp symlink target was overwritten: %q", targetData)
	}
	info, err := os.Lstat(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("state path mode = %v, want regular file", info.Mode())
	}
	assertMode(t, filepath.Join(dir, "state.json"), 0o600)
}

func TestLoadStateRepairsExistingPrivatePermissions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	dir := filepath.Join(root, "clipfan")
	path := filepath.Join(dir, "state.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"kind":"text","ts":"2026-05-28T12:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "text" {
		t.Fatalf("Kind = %q, want text", got.Kind)
	}
	assertMode(t, dir, 0o700)
	assertMode(t, path, 0o600)
}

func TestLoadStateRejectsSymlinkedStateFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	dir := filepath.Join(root, "clipfan")
	path := filepath.Join(dir, "state.json")
	target := filepath.Join(root, "target-state.json")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{"kind":"text","ts":"2026-05-28T12:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadState(); err == nil {
		t.Fatal("LoadState returned nil error for symlinked state file")
	}
}

func TestLoadTextRepairsExistingPrivatePermissions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	dir := filepath.Join(root, "clipfan")
	path := filepath.Join(dir, "current.txt")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadText()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "secret" {
		t.Fatalf("text = %q, want secret", got)
	}
	assertMode(t, dir, 0o700)
	assertMode(t, path, 0o600)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}
