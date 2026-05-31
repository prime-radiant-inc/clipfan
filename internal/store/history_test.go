package store

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
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

func idOf(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func itoa(i int) string    { return strconv.Itoa(i) }

func TestPinExemptFromTrim(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	AppendHistory(clipboard.New(clipboard.KindText, []byte("keepme"), ts(1)), "m4", "")
	if err := SetPinned(idOf("keepme"), true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		AppendHistory(clipboard.New(clipboard.KindText, []byte(itoa(i)+"-x"), ts(100+i)), "m4", "")
	}
	got, _ := LoadHistory(1000)
	found := false
	for _, e := range got {
		if e.Preview == "keepme" {
			found = true
		}
	}
	if !found {
		t.Fatal("pinned entry was trimmed away")
	}
}

func TestDeleteEntry(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	AppendHistory(clipboard.New(clipboard.KindText, []byte("a"), ts(1)), "m4", "")
	AppendHistory(clipboard.New(clipboard.KindText, []byte("b"), ts(2)), "m4", "")
	if err := DeleteEntry(idOf("a")); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadHistory(10)
	if len(got) != 1 || got[0].Preview != "b" {
		t.Fatalf("delete failed: %+v", got)
	}
}

func TestClearUnpinnedKeepsPinned(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	AppendHistory(clipboard.New(clipboard.KindText, []byte("pin"), ts(1)), "m4", "")
	AppendHistory(clipboard.New(clipboard.KindText, []byte("drop"), ts(2)), "m4", "")
	_ = SetPinned(idOf("pin"), true)
	if err := ClearUnpinned(); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadHistory(10)
	if len(got) != 1 || got[0].Preview != "pin" {
		t.Fatalf("clear-unpinned failed: %+v", got)
	}
}

func TestEntryByID(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	AppendHistory(clipboard.New(clipboard.KindText, []byte("findme"), ts(1)), "m4", "")
	e, ok, err := EntryByID(idOf("findme"))
	if err != nil || !ok {
		t.Fatalf("EntryByID ok=%v err=%v", ok, err)
	}
	if e.Preview != "findme" {
		t.Fatalf("wrong entry: %+v", e)
	}
	_, ok2, _ := EntryByID("nonexistent")
	if ok2 {
		t.Fatal("EntryByID returned ok for missing id")
	}
}

func TestReferencedImages(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	png := []byte("\x89PNG\r\n\x1a\nx")
	AppendHistory(clipboard.New(clipboard.KindImage, png, ts(1)), "m4", "/some/dir/abc.png")
	AppendHistory(clipboard.New(clipboard.KindText, []byte("txt"), ts(2)), "m4", "")
	set, err := ReferencedImages()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := set["abc.png"]; !ok {
		t.Fatalf("referenced set missing abc.png: %v", set)
	}
	if len(set) != 1 {
		t.Fatalf("referenced set should have 1 entry, got %v", set)
	}
}

func TestImageGCSpareReferenced(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// Save a real image and record it in history.
	png := []byte("\x89PNG\r\n\x1a\n-referenced-")
	p, err := SaveImage(png)
	if err != nil {
		t.Fatal(err)
	}
	c := clipboard.New(clipboard.KindImage, png, ts(1))
	if err := AppendHistory(c, "m4", p); err != nil {
		t.Fatal(err)
	}
	// Flood with more unreferenced images than maxImages to force GC.
	for i := 0; i < maxImages+10; i++ {
		if _, err := SaveImage([]byte("filler-" + itoa(i))); err != nil {
			t.Fatal(err)
		}
	}
	// gc runs in a goroutine inside SaveImage; call it synchronously to assert.
	gc(imagesDir())
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("referenced image was GC'd: %v", err)
	}
}

func TestAppendHistoryUsesPrivatePermissions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := AppendHistory(clipboard.New(clipboard.KindText, []byte("secret"), ts(1)), "m4", ""); err != nil {
		t.Fatal(err)
	}
	assertMode(t, filepath.Join(root, "clipfan"), 0o700)
	assertMode(t, filepath.Join(root, "clipfan", "history.json"), 0o600)
}

func TestLoadHistoryRepairsExistingPrivatePermissions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	dir := filepath.Join(root, "clipfan")
	path := filepath.Join(dir, "history.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`[{"id":"abc","kind":"text","preview":"secret","text":"secret","size_bytes":6,"origin":"m4","ts":"2026-05-28T12:00:00Z"}]`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadHistory(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Preview != "secret" {
		t.Fatalf("history = %+v, want secret entry", got)
	}
	assertMode(t, dir, 0o700)
	assertMode(t, path, 0o600)
}
