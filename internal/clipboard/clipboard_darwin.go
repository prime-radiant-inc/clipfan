//go:build darwin

package clipboard

import (
	"bytes"
	"os/exec"
	"time"
)

type macBackend struct{}

func NewBackend() Backend { return &macBackend{} }

func (macBackend) Read() (Content, error) {
	// Prefer image: NSPasteboard often holds both text and image
	// representations of the same item; an image is the richer signal.
	if png, ok := readPNG(); ok {
		return New(KindImage, png, time.Now().UTC()), nil
	}
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		return Content{}, err
	}
	return New(KindText, out, time.Now().UTC()), nil
}

func (macBackend) Write(c Content) error {
	// Image bytes are not written to the OS clipboard here — the daemon's
	// image path is "save file + put path on text clipboard". This keeps the
	// pasteboard text-only, which is what tmux load-buffer and Claude Code /
	// Codex bracketed paste want.
	if c.Kind != KindText {
		return nil
	}
	cmd := exec.Command("pbcopy")
	cmd.Stdin = bytes.NewReader(c.Bytes)
	return cmd.Run()
}

func readPNG() ([]byte, bool) {
	cmd := exec.Command("pngpaste", "-")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, false
	}
	if stdout.Len() == 0 {
		return nil, false
	}
	return stdout.Bytes(), true
}
