package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveImageWritesAndDedupes(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)

	body := []byte("not really a png but bytes are bytes")
	path, err := SaveImage(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, ".png") {
		t.Fatalf("want .png suffix: %s", path)
	}
	if !strings.HasPrefix(path, filepath.Join(tmp, "clipfan", "images")) {
		t.Fatalf("not under XDG_STATE_HOME: %s", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatal("content mismatch")
	}

	// Saving again is a no-op (idempotent by content).
	path2, err := SaveImage(body)
	if err != nil {
		t.Fatal(err)
	}
	if path2 != path {
		t.Fatalf("paths diverged: %s vs %s", path, path2)
	}
}
