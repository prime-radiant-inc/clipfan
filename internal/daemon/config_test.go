package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
