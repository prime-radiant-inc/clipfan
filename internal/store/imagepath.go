package store

import (
	"os"
	"path/filepath"
	"strings"
)

// IsImageStorePath reports whether s names an image in this host's
// content-addressed image store: a path whose basename is <sha256>.png and which
// exists in our images dir. Such a path is the daemon's own representation of an
// image (written to the clipboard as text on backends that can't hold images),
// not user-authored clipboard content — so it must never be broadcast into the
// mesh as a text clip, where it would clobber the real image on image-capable
// hosts. Matching by basename makes this work across hosts, since the sha256 name
// is identical everywhere the same image is stored.
func IsImageStorePath(s string) bool {
	s = strings.TrimSpace(s)
	base := filepath.Base(s)
	name, ok := strings.CutSuffix(base, ".png")
	if !ok || !isHex64(name) {
		return false
	}
	if _, err := os.Stat(filepath.Join(imagesDir(), base)); err != nil {
		return false
	}
	return true
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
