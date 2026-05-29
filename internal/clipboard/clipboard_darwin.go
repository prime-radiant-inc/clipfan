//go:build darwin

package clipboard

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type macBackend struct{}

func NewBackend() Backend { return &macBackend{} }

func (macBackend) Read() (Content, error) {
	if png, ok := readPNG(); ok {
		c := New(KindImage, png, time.Now().UTC())
		c.Concealed = concealed()
		return c, nil
	}
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		return Content{}, err
	}
	c := New(KindText, out, time.Now().UTC())
	c.Concealed = concealed()
	return c, nil
}

// concealed reports whether the current pasteboard item is marked as
// concealed or transient (e.g. by a password manager). Password managers
// declare org.nspasteboard.ConcealedType so the clip is excluded from
// clipboard history. Detection requires inspecting the raw pasteboard types,
// which pbpaste and osascript cannot surface; the bundled Swift helper exposes
// them via --check-concealed. Returns false when the helper is unavailable.
func concealed() bool {
	helper, err := findHelper()
	if err != nil {
		return false
	}
	out, err := exec.Command(helper, "--check-concealed").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "concealed")
}

func (macBackend) WriteText(text []byte) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = bytes.NewReader(text)
	return cmd.Run()
}

// WriteImage shells out to the bundled clipfan-pasteboard-helper Swift
// binary to write a single NSPasteboardItem containing BOTH the PNG bytes
// (public.png) and the file path as text (public.utf8-plain-text).
// JXA failed (the bridge interpreted NSPasteboard.generalPasteboard as a
// number); writing a 3-line Swift CLI was the path of less surprise.
// Falls back to text-only if the helper isn't installed.
func (macBackend) WriteImage(body []byte, path string) error {
	if path == "" {
		return nil
	}
	helper, err := findHelper()
	if err != nil {
		return (macBackend{}).WriteText([]byte(path))
	}
	out, err := exec.Command(helper, path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("clipfan-pasteboard-helper %s: %w (output: %s)", path, err, string(out))
	}
	return nil
}

// findHelper resolves the Swift pasteboard helper across the launchd plist's
// stripped PATH. We check LookPath first (covers the case where the helper
// is in /usr/local/bin), then $HOME/.local/bin where install.sh drops it.
func findHelper() (string, error) {
	if p, err := exec.LookPath("clipfan-pasteboard-helper"); err == nil {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err == nil {
		candidate := filepath.Join(home, ".local", "bin", "clipfan-pasteboard-helper")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("clipfan-pasteboard-helper not found in PATH or ~/.local/bin")
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
