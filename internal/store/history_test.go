package store

import (
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

func ts(s int) time.Time { return time.Unix(int64(s), 0).UTC() }

func TestAppendAndLoadNewestFirst(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	AppendHistory(clipboard.New(clipboard.KindText, []byte("first"), ts(1)), "m4", "")
	AppendHistory(clipboard.New(clipboard.KindText, []byte("second"), ts(2)), "flower-garden", "")
	got, err := LoadHistory(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Preview != "second" {
		t.Fatalf("newest-first broken: got[0]=%q", got[0].Preview)
	}
	if got[0].Origin != "flower-garden" {
		t.Fatalf("origin = %q, want flower-garden", got[0].Origin)
	}
}

func TestAppendDedupMovesToTop(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	AppendHistory(clipboard.New(clipboard.KindText, []byte("dup"), ts(1)), "m4", "")
	AppendHistory(clipboard.New(clipboard.KindText, []byte("other"), ts(2)), "m4", "")
	AppendHistory(clipboard.New(clipboard.KindText, []byte("dup"), ts(3)), "m4", "")
	got, _ := LoadHistory(10)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (dup collapsed)", len(got))
	}
	if got[0].Preview != "dup" {
		t.Fatalf("re-copied item not floated to top: got[0]=%q", got[0].Preview)
	}
}

func TestLinkDetection(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	AppendHistory(clipboard.New(clipboard.KindText, []byte("https://example.com/x"), ts(1)), "m4", "")
	got, _ := LoadHistory(10)
	if got[0].Kind != "link" {
		t.Fatalf("kind = %q, want link", got[0].Kind)
	}
}

func TestImageEntryUsesPath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	png := []byte("\x89PNG\r\n\x1a\nfake")
	AppendHistory(clipboard.New(clipboard.KindImage, png, ts(1)), "m4", "/abs/path.png")
	got, _ := LoadHistory(10)
	if got[0].Kind != "image" || got[0].ImagePath != "/abs/path.png" {
		t.Fatalf("image entry wrong: %+v", got[0])
	}
	if got[0].Text != "" {
		t.Fatalf("image entry should not inline bytes as text")
	}
}
