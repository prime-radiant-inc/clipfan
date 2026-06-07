package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResolveTmuxBinaryFallsBackToAbsoluteCandidate(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	missing := func(string) (string, error) { return "", exec.ErrNotFound }
	got := resolveTmuxBinary(missing, []string{"/nonexistent/tmux", fake})
	if got != fake {
		t.Fatalf("got %q, want %q", got, fake)
	}
}

func TestResolveTmuxBinaryPrefersPathLookup(t *testing.T) {
	found := func(string) (string, error) { return "/from/path/tmux", nil }
	got := resolveTmuxBinary(found, []string{"/abs/tmux"})
	if got != "/from/path/tmux" {
		t.Fatalf("got %q, want /from/path/tmux", got)
	}
}

func TestResolveTmuxBinaryLastResortIsBareName(t *testing.T) {
	missing := func(string) (string, error) { return "", exec.ErrNotFound }
	got := resolveTmuxBinary(missing, []string{"/nope/tmux"})
	if got != "tmux" {
		t.Fatalf("got %q, want \"tmux\"", got)
	}
}
