package tmux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTmuxSnippetDoesNotInstallGlobalBufferHooks(t *testing.T) {
	snippet, err := os.ReadFile(filepath.Join("..", "..", "dist", "tmux.conf.snippet"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(snippet)

	if !strings.Contains(text, "copy-pipe-and-cancel") {
		t.Fatal("tmux snippet should still route copy-mode yanks through clipfan")
	}
	for _, hook := range []string{"after-set-buffer", "after-load-buffer"} {
		if strings.Contains(text, hook) {
			t.Fatalf("tmux snippet must not install global %s hooks", hook)
		}
	}
}
