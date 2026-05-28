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
