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
	"syscall"
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
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if err := requireTrustedSocketDir(dir, dirInfo); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSocket != 0 && trustedSocket(info) {
			out = append(out, path)
		}
	}
	return out, nil
}

func requireTrustedSocketDir(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("tmux socket dir %s is a symlink", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("tmux socket dir %s is not a directory", path)
	}
	if !ownedByCurrentUser(info) {
		return fmt.Errorf("tmux socket dir %s is not owned by current user", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("tmux socket dir %s permissions %03o are too open", path, info.Mode().Perm())
	}
	return nil
}

func trustedSocket(info os.FileInfo) bool {
	// tmux commonly creates sockets as 0660/0770 under a 0700 user-owned
	// directory. The directory gate above is what prevents other users from
	// reaching them; reject only world-writable sockets here.
	return ownedByCurrentUser(info) && info.Mode().Perm()&0o002 == 0
}

func ownedByCurrentUser(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}

func socketDir() string {
	if d := os.Getenv("TMUX_TMPDIR"); d != "" {
		return filepath.Join(d, fmt.Sprintf("tmux-%d", os.Getuid()))
	}
	return fmt.Sprintf("/tmp/tmux-%d", os.Getuid())
}
