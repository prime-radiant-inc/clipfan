// Package tmux fans clipboard content out to every running tmux server on
// the host by calling `tmux -S <socket> load-buffer -`. The intent is that
// after every clipboard change, prefix-] in any tmux session pastes the same
// content as the OS clipboard.
package tmux

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

// LoadBufferAll writes `content` as the top paste buffer on every tmux server
// owned by the current user. Returns the first error encountered, if any. Not
// having any tmux sockets is not an error.
func LoadBufferAll(content []byte) error {
	socks, err := Sockets()
	if err != nil {
		return err
	}
	var first error
	for _, s := range socks {
		cmd := exec.Command("tmux", "-S", s, "load-buffer", "-")
		cmd.Stdin = bytes.NewReader(content)
		if err := cmd.Run(); err != nil && first == nil {
			first = fmt.Errorf("%s: %w", s, err)
		}
	}
	return first
}

// Sockets returns the absolute paths of every tmux socket the current user
// owns under the standard tmux directory.
func Sockets() ([]string, error) {
	dir := socketDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSocket != 0 {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out, nil
}

func socketDir() string {
	if d := os.Getenv("TMUX_TMPDIR"); d != "" {
		return filepath.Join(d, fmt.Sprintf("tmux-%d", os.Getuid()))
	}
	return fmt.Sprintf("/tmp/tmux-%d", os.Getuid())
}
