package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func TestSetMaxHistoryClampsAndRejects(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	d, _, _ := newTestDaemon(t)

	if err := d.setMaxHistory(0); err == nil {
		t.Fatal("setMaxHistory(0) should error")
	}
	if err := d.setMaxHistory(10); err != nil {
		t.Fatal(err)
	}
	if got := readSavedMax(t); got != 50 {
		t.Fatalf("saved max = %d, want clamped 50", got)
	}
	if err := d.setMaxHistory(99999); err != nil {
		t.Fatal(err)
	}
	if got := readSavedMax(t); got != 5000 {
		t.Fatalf("saved max = %d, want clamped 5000", got)
	}
	if err := d.setMaxHistory(300); err != nil {
		t.Fatal(err)
	}
	if got := readSavedMax(t); got != 300 {
		t.Fatalf("saved max = %d, want 300", got)
	}
}

func TestPeersHandlerIncludesMaxHistory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, _, _ := newTestDaemon(t)
	out := d.peersHandler().(map[string]any)
	if _, ok := out["max_history"]; !ok {
		t.Fatal("peers response missing max_history")
	}
}

func TestSetMaxHistoryRejectsConfigV2WhenWritesDisabled(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := filepath.Join(root, "clipfan")
	path := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	before := []byte(`{"config_version":2,"config_revision":1,"shared_key":"` + config.NewSharedKey() + `","max_history":50,"future":{"keep":true}}`)
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}

	d, _, _ := newTestDaemon(t)
	err := d.setMaxHistory(300)
	if !errors.Is(err, config.ErrConfigV2WritesDisabled) {
		t.Fatalf("setMaxHistory error = %v, want ErrConfigV2WritesDisabled", err)
	}
	if !strings.Contains(err.Error(), "config_v2_writes_disabled") {
		t.Fatalf("setMaxHistory error = %v, want stable code", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("v2 config changed\nbefore: %s\nafter: %s", before, after)
	}
}

func readSavedMax(t *testing.T) int {
	t.Helper()
	p := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "clipfan", "config.json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		MaxHistory int `json:"max_history"`
	}
	if err := jsonUnmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	return c.MaxHistory
}
