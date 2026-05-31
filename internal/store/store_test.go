package store

import (
	"crypto/sha256"
	"encoding/hex"
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

func TestSaveImageUsesPrivatePermissions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	path, err := SaveImage([]byte("image-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	assertMode(t, filepath.Join(root, "clipfan"), 0o700)
	assertMode(t, filepath.Join(root, "clipfan", "images"), 0o700)
	assertMode(t, path, 0o600)
}

func TestSaveImageRepairsExistingPrivatePermissions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	body := []byte("same image bytes")
	sum := sha256.Sum256(body)
	path := filepath.Join(root, "clipfan", "images", hex.EncodeToString(sum[:])+".png")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := SaveImage(body)
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("path = %q, want %q", got, path)
	}
	assertMode(t, filepath.Join(root, "clipfan"), 0o700)
	assertMode(t, filepath.Join(root, "clipfan", "images"), 0o700)
	assertMode(t, path, 0o600)
}

func TestSaveImageRejectsExistingNonRegularPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	body := []byte("same image bytes")
	sum := sha256.Sum256(body)
	path := filepath.Join(root, "clipfan", "images", hex.EncodeToString(sum[:])+".png")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := SaveImage(body); err == nil {
		t.Fatal("SaveImage returned nil error for existing image directory")
	}
}

func TestSaveImageRejectsExistingSymlink(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	body := []byte("same image bytes")
	sum := sha256.Sum256(body)
	path := filepath.Join(root, "clipfan", "images", hex.EncodeToString(sum[:])+".png")
	target := filepath.Join(root, "target.png")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	if _, err := SaveImage(body); err == nil {
		t.Fatal("SaveImage returned nil error for existing image symlink")
	}
}

func TestSaveImageDoesNotFollowTempSymlink(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	body := []byte("same image bytes")
	sum := sha256.Sum256(body)
	path := filepath.Join(root, "clipfan", "images", hex.EncodeToString(sum[:])+".png")
	target := filepath.Join(root, "target.png")
	tmp := path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, tmp); err != nil {
		t.Fatal(err)
	}

	got, err := SaveImage(body)
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("path = %q, want %q", got, path)
	}
	targetData, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(targetData) != "keep" {
		t.Fatalf("temp symlink target was overwritten: %q", targetData)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("image path mode = %v, want regular file", info.Mode())
	}
	assertMode(t, path, 0o600)
}
