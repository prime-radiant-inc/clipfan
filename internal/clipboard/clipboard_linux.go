//go:build linux

package clipboard

import (
	"bytes"
	"os"
	"os/exec"
	"time"
)

type linuxBackend struct {
	read  []string
	write []string
}

func NewBackend() Backend {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if _, err := exec.LookPath("wl-paste"); err == nil {
			return &linuxBackend{
				read:  []string{"wl-paste", "--no-newline"},
				write: []string{"wl-copy"},
			}
		}
	}
	if _, err := exec.LookPath("xclip"); err == nil {
		return &linuxBackend{
			read:  []string{"xclip", "-selection", "clipboard", "-o"},
			write: []string{"xclip", "-selection", "clipboard", "-i"},
		}
	}
	return &headlessBackend{}
}

func (b *linuxBackend) Read() (Content, error) {
	out, err := exec.Command(b.read[0], b.read[1:]...).Output()
	if err != nil {
		// no display, no content
		return Content{}, nil
	}
	return New(KindText, out, time.Now().UTC()), nil
}

func (b *linuxBackend) Write(c Content) error {
	if c.Kind != KindText {
		return nil
	}
	cmd := exec.Command(b.write[0], b.write[1:]...)
	cmd.Stdin = bytes.NewReader(c.Bytes)
	return cmd.Run()
}

// headlessBackend is used when no xclip/wl-paste is available. It silently
// no-ops so the daemon can still receive content and forward to tmux/state.
type headlessBackend struct{}

func (headlessBackend) Read() (Content, error)  { return Content{}, nil }
func (headlessBackend) Write(c Content) error   { return nil }
