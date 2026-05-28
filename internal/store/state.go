package store

import (
	"encoding/json"
	"errors"
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

func ensureDir() error {
	return os.MkdirAll(config.StateDir(), 0o755)
}

// SaveState writes the metadata (and the text payload, if kind is text) so
// the shim can answer xclip/wl-paste calls.
func SaveState(s State, text []byte) error {
	if err := ensureDir(); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	if err := writeAtomic(statePath(), data, 0o644); err != nil {
		return err
	}
	return writeAtomic(textPath(), text, 0o644)
}

func LoadState() (State, error) {
	data, err := os.ReadFile(statePath())
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
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
	data, err := os.ReadFile(textPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
