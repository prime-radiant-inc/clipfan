//go:build linux

package clipboard

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"time"
)

type linuxBackend struct {
	// readText / writeText run the OS clipboard tool for text payloads.
	readText  []string
	writeText []string
	// readTargets returns the MIME types available on the clipboard.
	readTargets []string
	// readImage reads PNG bytes from the clipboard.
	readImage []string
}

func NewBackend() Backend {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if _, err := exec.LookPath("wl-paste"); err == nil {
			return &linuxBackend{
				readText:    []string{"wl-paste", "--no-newline"},
				writeText:   []string{"wl-copy"},
				readTargets: []string{"wl-paste", "--list-types"},
				readImage:   []string{"wl-paste", "--type", "image/png"},
			}
		}
	}
	if _, err := exec.LookPath("xclip"); err == nil {
		return &linuxBackend{
			readText:    []string{"xclip", "-selection", "clipboard", "-o"},
			writeText:   []string{"xclip", "-selection", "clipboard", "-i"},
			readTargets: []string{"xclip", "-selection", "clipboard", "-t", "TARGETS", "-o"},
			readImage:   []string{"xclip", "-selection", "clipboard", "-t", "image/png", "-o"},
		}
	}
	return &headlessBackend{}
}

func (b *linuxBackend) Read() (Content, error) {
	// Look at TARGETS first: if image/png is advertised, prefer it.
	if out, err := exec.Command(b.readTargets[0], b.readTargets[1:]...).Output(); err == nil {
		if strings.Contains(string(out), "image/png") {
			if png, err := exec.Command(b.readImage[0], b.readImage[1:]...).Output(); err == nil && len(png) > 0 {
				return New(KindImage, png, time.Now().UTC()), nil
			}
		}
	}
	out, err := exec.Command(b.readText[0], b.readText[1:]...).Output()
	if err != nil {
		return Content{}, nil
	}
	return New(KindText, out, time.Now().UTC()), nil
}

func (b *linuxBackend) Write(c Content) error {
	if c.Kind != KindText {
		return nil
	}
	cmd := exec.Command(b.writeText[0], b.writeText[1:]...)
	cmd.Stdin = bytes.NewReader(c.Bytes)
	return cmd.Run()
}

// headlessBackend is the no-op fallback for hosts with neither xclip nor
// wl-clipboard installed (typical headless Linux). Receiving daemons still
// run; only the OS-clipboard write step is skipped. The daemon's
// path-on-text + tmux load-buffer steps still fire.
type headlessBackend struct{}

func (headlessBackend) Read() (Content, error) { return Content{}, nil }
func (headlessBackend) Write(c Content) error  { return nil }
