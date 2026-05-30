package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestIsImageStorePath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	path, err := SaveImage([]byte("PNGDATA"))
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(path) // <sha256>.png

	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"our store path", path, true},
		{"trailing newline", path + "\n", true},
		{"surrounding whitespace", "  " + path + "  ", true},
		// Content-addressed: a peer's path has a different dir prefix but the
		// same <sha256>.png basename, which exists in our store.
		{"cross-host same basename", "/home/jesse/.local/state/clipfan/images/" + base, true},
		{"prose text", "remember to call the dentist", false},
		{"unrelated file path", "/Users/jesse/notes.txt", false},
		{"hex name not in store", "/x/" + strings.Repeat("a", 64) + ".png", false},
		{"non-hex png basename", "/x/screenshot.png", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		if got := IsImageStorePath(tc.in); got != tc.want {
			t.Errorf("%s: IsImageStorePath(%q) = %v, want %v", tc.name, tc.in, got, tc.want)
		}
	}
}
