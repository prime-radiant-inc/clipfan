package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

func TestRecapTrimsUnpinnedKeepsPinned(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "cfg"))

	for i := 0; i < 5; i++ {
		body := []byte{byte('a' + i)}
		c := clipboard.New(clipboard.KindText, body, time.Now().Add(time.Duration(i)*time.Second))
		if err := AppendHistory(c, "self", ""); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := readHistory()
	if len(list) != 5 {
		t.Fatalf("seeded %d entries, want 5", len(list))
	}
	oldest := list[len(list)-1].ID
	if err := SetPinned(oldest, true); err != nil {
		t.Fatal(err)
	}

	cfgDir := filepath.Join(tmp, "cfg", "clipfan")
	os.MkdirAll(cfgDir, 0o755)
	os.WriteFile(filepath.Join(cfgDir, "config.json"),
		[]byte(`{"shared_key":"k","max_history":2}`), 0o600)

	if got := CapLimit(); got != 2 {
		t.Fatalf("CapLimit() = %d, want 2", got)
	}
	if err := Recap(); err != nil {
		t.Fatal(err)
	}
	after, _ := readHistory()
	if len(after) != 2 {
		t.Fatalf("after Recap len = %d, want 2 (1 pinned + 1 unpinned)", len(after))
	}
	pinnedSurvived := false
	for _, e := range after {
		if e.ID == oldest && e.Pinned {
			pinnedSurvived = true
		}
	}
	if !pinnedSurvived {
		t.Fatal("pinned entry was trimmed by Recap")
	}
}
