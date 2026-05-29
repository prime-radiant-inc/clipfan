package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Listen      string   `json:"listen"`
	SharedKey   string   `json:"shared_key"`
	Discovery   string   `json:"discovery"`
	StaticPeers []string `json:"static_peers,omitempty"`
	Hostname    string   `json:"hostname,omitempty"`
	Port        int      `json:"port,omitempty"`
	MaxHistory  int      `json:"max_history,omitempty"`
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
		c.MaxHistory = 200
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
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg := withDefaults(Config{SharedKey: NewSharedKey()})
		c := &cfg
		if err := Save(c); err != nil {
			return nil, fmt.Errorf("save initial config: %w", err)
		}
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	c := &Config{}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, err
	}
	*c = withDefaults(*c)
	return c, nil
}

func Save(c *Config) error {
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(path, data, 0o600)
}

func NewSharedKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}
