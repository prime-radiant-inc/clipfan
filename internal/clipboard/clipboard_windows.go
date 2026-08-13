//go:build windows

package clipboard

import (
	"bytes"
	"os/exec"
	"time"
)

// windowsBackend reads/writes the clipboard via the built-in PowerShell
// Get-Clipboard and clip.exe. Text only for now; image support is a follow-up
// (matching the Linux backend's text-only fallback for images).
type windowsBackend struct{}

// NewBackend returns the Windows clipboard backend.
func NewBackend() Backend { return &windowsBackend{} }

// Read returns the current clipboard text via "Get-Clipboard -Raw".
func (windowsBackend) Read() (Content, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", "Get-Clipboard -Raw")
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return Content{}, nil
	}
	return New(KindText, out, time.Now().UTC()), nil
}

// WriteText pipes text into clip.exe (the built-in Windows clipboard setter).
func (windowsBackend) WriteText(text []byte) error {
	cmd := exec.Command("clip")
	cmd.Stdin = bytes.NewReader(text)
	return cmd.Run()
}

// WriteImage is not yet supported on Windows; fall back to text (the file
// path), matching the Linux backend's behaviour.
func (windowsBackend) WriteImage(body []byte, path string) error {
	if path == "" {
		return nil
	}
	return (windowsBackend{}).WriteText([]byte(path))
}
