package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewSharedKeyUnique(t *testing.T) {
	a := NewSharedKey()
	b := NewSharedKey()
	if a == b {
		t.Fatal("two fresh keys collided")
	}
	if len(a) < 40 {
		t.Fatalf("key too short: %d", len(a))
	}
}

func TestMaxHistoryDefault(t *testing.T) {
	c := withDefaults(Config{})
	if c.MaxHistory != 200 {
		t.Fatalf("MaxHistory default = %d, want 200", c.MaxHistory)
	}
}

func TestMaxHistoryRespectsExplicit(t *testing.T) {
	c := withDefaults(Config{MaxHistory: 50})
	if c.MaxHistory != 50 {
		t.Fatalf("MaxHistory = %d, want 50 (explicit kept)", c.MaxHistory)
	}
}

func TestLoadCreatesDefaultConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.SharedKey == "" {
		t.Fatal("default config missing shared key")
	}
	if c.Port != 7853 {
		t.Fatalf("default port = %d, want 7853", c.Port)
	}
	want := filepath.Join(tmp, "clipfan", "config.json")
	if Path() != want {
		t.Fatalf("Path() = %q, want %q", Path(), want)
	}
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "shared_key") {
		t.Fatal("config file missing shared_key field")
	}
}

func TestLoadRepairsExistingConfigDirectoryPermissions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	dir := filepath.Join(root, "clipfan")
	path := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"shared_key":"`+NewSharedKey()+`","max_history":50}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxHistory != 50 {
		t.Fatalf("MaxHistory = %d, want 50", cfg.MaxHistory)
	}
	assertMode(t, dir, 0o700)
}

func TestLoadRepairsExistingConfigFilePermissions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	dir := filepath.Join(root, "clipfan")
	path := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"shared_key":"`+NewSharedKey()+`","max_history":50}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxHistory != 50 {
		t.Fatalf("MaxHistory = %d, want 50", cfg.MaxHistory)
	}
	assertMode(t, path, 0o600)
}

func TestLoadRejectsSymlinkedConfigFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	dir := filepath.Join(root, "clipfan")
	path := filepath.Join(dir, "config.json")
	target := filepath.Join(root, "target.json")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{"shared_key":"`+NewSharedKey()+`","max_history":50}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(); err == nil {
		t.Fatal("Load returned nil error for symlinked config file")
	}
}

func TestSaveUsesPrivatePermissions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	cfg := &Config{SharedKey: NewSharedKey()}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	assertMode(t, filepath.Join(root, "clipfan"), 0o700)
	assertMode(t, filepath.Join(root, "clipfan", "config.json"), 0o600)
}

func TestSaveDoesNotFollowSymlinkedConfigFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	dir := filepath.Join(root, "clipfan")
	path := filepath.Join(dir, "config.json")
	target := filepath.Join(root, "target.json")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	if err := Save(&Config{SharedKey: NewSharedKey(), MaxHistory: 50}); err != nil {
		t.Fatal(err)
	}
	targetData, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(targetData) != "keep" {
		t.Fatalf("symlink target was overwritten: %q", targetData)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("config path mode = %v, want regular file", info.Mode())
	}
	assertMode(t, path, 0o600)
}

func TestSaveRejectsSymlinkedConfigDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	targetDir := filepath.Join(root, "target")
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetDir, filepath.Join(root, "clipfan")); err != nil {
		t.Fatal(err)
	}

	if err := Save(&Config{SharedKey: NewSharedKey()}); err == nil {
		t.Fatal("Save returned nil error for symlinked config directory")
	}
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
