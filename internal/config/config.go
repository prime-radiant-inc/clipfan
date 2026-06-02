package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
)

const defaultMaxHistory = 200

type Config struct {
	ConfigVersion  *int     `json:"config_version,omitempty"`
	ConfigRevision *uint64  `json:"config_revision,omitempty"`
	Listen         string   `json:"listen"`
	SharedKey      string   `json:"shared_key"`
	Discovery      string   `json:"discovery"`
	StaticPeers    []string `json:"static_peers,omitempty"`
	Hostname       string   `json:"hostname,omitempty"`
	Port           int      `json:"port,omitempty"`
	// MaxHistory caps the number of retained clipboard history entries.
	MaxHistory int `json:"max_history,omitempty"`
}

// withDefaults fills zero-valued fields with their defaults.
func withDefaults(c Config) Config {
	if c.Port == 0 {
		c.Port = 7853
	}
	if c.Listen == "" {
		c.Listen = ":7853"
	}
	if c.Discovery == "" {
		c.Discovery = "tailscale"
	}
	if c.MaxHistory == 0 {
		c.MaxHistory = defaultMaxHistory
	}
	return c
}

func Path() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "clipfan", "config.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "clipfan", "config.json")
}

func StateDir() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "clipfan")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "clipfan")
}

func Load() (*Config, error) {
	path := Path()
	if err := ensureConfigDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if err := repairConfigFile(path); errors.Is(err, os.ErrNotExist) {
		cfg := withDefaults(Config{SharedKey: NewSharedKey()})
		c := &cfg
		if err := Save(c); err != nil {
			return nil, fmt.Errorf("save initial config: %w", err)
		}
		return c, nil
	} else if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := parseConfigDocument(data)
	if err != nil {
		return nil, err
	}
	c := &doc.Config
	*c = withDefaults(*c)
	return c, nil
}

func Save(c *Config) error {
	if c != nil && c.ConfigVersion != nil && *c.ConfigVersion == 2 && !releaseflags.ConfigV2WriteEnabled {
		return ErrConfigV2WritesDisabled
	}
	path := Path()
	if err := ensureConfigDir(filepath.Dir(path)); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(c, "", "  ")
	return writeConfigAtomic(path, data, 0o600)
}

func repairConfigFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("config file %s is not a regular file", path)
	}
	return os.Chmod(path, 0o600)
}

func ensureConfigDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsDir() {
		return fmt.Errorf("config directory %s is not a directory", dir)
	}
	return os.Chmod(dir, 0o700)
}

func writeConfigAtomic(path string, data []byte, mode os.FileMode) error {
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

func removePathIfExists(path string) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return os.Remove(path)
}

func NewSharedKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}
