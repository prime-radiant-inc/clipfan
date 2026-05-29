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
	readText    []string
	writeText   []string
	readTargets []string
	readImage   []string
}

func NewBackend() Backend {
	have := func(bin string) bool { _, err := exec.LookPath(bin); return err == nil }
	switch chooseBackend(os.Getenv("WAYLAND_DISPLAY"), os.Getenv("DISPLAY"), have) {
	case backendWayland:
		return &linuxBackend{
			readText:    []string{"wl-paste", "--no-newline"},
			writeText:   []string{"wl-copy"},
			readTargets: []string{"wl-paste", "--list-types"},
			readImage:   []string{"wl-paste", "--type", "image/png"},
		}
	case backendXclip:
		return &linuxBackend{
			readText:    []string{"xclip", "-selection", "clipboard", "-o"},
			writeText:   []string{"xclip", "-selection", "clipboard", "-i"},
			readTargets: []string{"xclip", "-selection", "clipboard", "-t", "TARGETS", "-o"},
			readImage:   []string{"xclip", "-selection", "clipboard", "-t", "image/png", "-o"},
		}
	default:
		// No usable display server: avoid shelling out to a clipboard tool that
		// has nothing to talk to (which logged a write failure on every clip).
		return &headlessBackend{}
	}
}

func (b *linuxBackend) Read() (Content, error) {
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

func (b *linuxBackend) WriteText(text []byte) error {
	cmd := exec.Command(b.writeText[0], b.writeText[1:]...)
	cmd.Stdin = bytes.NewReader(text)
	return cmd.Run()
}

// WriteImage on Linux falls back to text-only (the file path) because xclip
// can't easily set multiple targets on the same selection. The xclip/wl-paste
// shim covers Ctrl-V image paste in Claude Code by serving the image from
// the file on disk; GUI image-paste on a Linux remote isn't a target.
func (b *linuxBackend) WriteImage(body []byte, path string) error {
	if path == "" {
		return nil
	}
	return b.WriteText([]byte(path))
}

type headlessBackend struct{}

func (headlessBackend) Read() (Content, error)             { return Content{}, nil }
func (headlessBackend) WriteText(text []byte) error        { return nil }
func (headlessBackend) WriteImage([]byte, string) error    { return nil }
