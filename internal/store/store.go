// Package store persists clipboard images to the XDG state directory so that
// remote tools (Claude Code, Codex, tmux paste) can reference them by path.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

const maxImages = 50

func imagesDir() string {
	return filepath.Join(config.StateDir(), "images")
}

// SaveImage writes the bytes to $XDG_STATE_HOME/clipfan/images/<sha256>.png
// and returns the absolute path. If a file with the same content hash already
// exists it is left untouched.
func SaveImage(body []byte) (string, error) {
	dir := imagesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	sum := sha256.Sum256(body)
	name := hex.EncodeToString(sum[:]) + ".png"
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	go gc(dir)
	return path, nil
}

// gc trims the oldest images beyond the retention bound, never deleting an
// image still referenced by a history entry.
func gc(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	referenced, _ := ReferencedImages() // best-effort; nil set spares nothing

	type stamped struct {
		path string
		name string
		mod  int64
	}
	all := make([]stamped, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		all = append(all, stamped{filepath.Join(dir, e.Name()), e.Name(), fi.ModTime().UnixNano()})
	}

	bound := maxImages
	if c := capLimit(); c > bound {
		bound = c
	}
	if len(all) <= bound {
		return
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mod < all[j].mod })
	toRemove := len(all) - bound
	for _, s := range all {
		if toRemove == 0 {
			break
		}
		if _, ok := referenced[s.name]; ok {
			continue // keep referenced images
		}
		if err := os.Remove(s.path); err == nil {
			toRemove--
		}
	}
}
