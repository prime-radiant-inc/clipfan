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
		c := &Config{
			Listen:    ":7853",
			SharedKey: NewSharedKey(),
			Discovery: "tailscale",
			Port:      7853,
		}
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
	if c.Port == 0 {
		c.Port = 7853
	}
	if c.Listen == "" {
		c.Listen = ":7853"
	}
	if c.Discovery == "" {
		c.Discovery = "tailscale"
	}
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
