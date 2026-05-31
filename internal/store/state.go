package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

// State is the metadata about the most recently received clipboard payload.
// Persisted so the xclip / wl-paste shim can answer queries from anywhere on
// the system without IPC to the daemon.
type State struct {
	Kind      string    `json:"kind"` // "text" or "image"
	TS        time.Time `json:"ts"`
	ImagePath string    `json:"image_path,omitempty"`
}

func statePath() string { return filepath.Join(config.StateDir(), "state.json") }
func textPath() string  { return filepath.Join(config.StateDir(), "current.txt") }

const (
	privateDirMode  os.FileMode = 0o700
	privateFileMode os.FileMode = 0o600
)

func ensureDir() error {
	return ensurePrivateDir(config.StateDir())
}

func ensurePrivateDir(path string) error {
	if err := os.MkdirAll(path, privateDirMode); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsDir() {
		return fmt.Errorf("state directory %s is not a directory", path)
	}
	return os.Chmod(path, privateDirMode)
}

// SaveState writes the metadata (and the text payload, if kind is text) so
// the shim can answer xclip/wl-paste calls.
func SaveState(s State, text []byte) error {
	if err := ensureDir(); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	if err := writeAtomic(statePath(), data, privateFileMode); err != nil {
		return err
	}
	return writeAtomic(textPath(), text, privateFileMode)
}

func LoadState() (State, error) {
	if err := repairStateFile(statePath()); errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	} else if err != nil {
		return State{}, err
	}
	data, err := os.ReadFile(statePath())
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, err
	}
	return s, nil
}

// LoadText returns the contents of current.txt — the text representation of
// the current clipboard (either the literal text payload, or the image file's
// absolute path for an image payload).
func LoadText() ([]byte, error) {
	if err := repairStateFile(textPath()); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(textPath())
	if err != nil {
		return nil, err
	}
	return data, nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := removePathIfExists(tmp); err != nil {
		return err
	}
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()
	n, err := file.Write(data)
	if err != nil {
		_ = file.Close()
		return err
	}
	if n != len(data) {
		_ = file.Close()
		return fmt.Errorf("short write to %s", tmp)
	}
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func repairStateFile(path string) error {
	if err := ensurePrivateDir(config.StateDir()); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("state file %s is not a regular file", path)
	}
	return os.Chmod(path, privateFileMode)
}

func removePathIfExists(path string) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return os.Remove(path)
}
