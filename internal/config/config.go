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

// withDefaults fills zero-valued fields with the current release defaults.
func withDefaults(c Config) Config {
	return withDefaultsForGeneratedListen(c, GeneratedLoopbackDefaultsEnabled())
}

func withDefaultsForGeneratedListen(c Config, loopbackDefault bool) Config {
	if c.Port == 0 {
		c.Port = 7853
	}
	if c.Listen == "" {
		c.Listen = DefaultListen(loopbackDefault, c.Port)
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
	return load(Path(), GeneratedLoopbackDefaultsEnabled())
}

func LoadForDaemonStart() (*Config, error) {
	return loadForDaemonStart(Path(), ListenerMigrationPolicy{
		GeneratedLoopbackListenEnabled: GeneratedLoopbackDefaultsEnabled(),
		ConfigV2WriteEnabled:           releaseflags.ConfigV2WriteEnabled,
	})
}

func loadForDaemonStart(path string, policy ListenerMigrationPolicy) (*Config, error) {
	cfg, doc, err := loadDocument(path, policy.GeneratedLoopbackListenEnabled)
	if err != nil {
		return nil, err
	}
	if !ApplyGeneratedListenMigration(cfg, policy.GeneratedLoopbackListenEnabled) {
		return cfg, nil
	}
	if doc.RevisionState != RevisionStateVersioned || !policy.ConfigV2WriteEnabled {
		return cfg, nil
	}
	expected := RevisionExpectation{
		State:    RevisionStateVersioned,
		Revision: doc.ConfigRevision,
	}
	if err := updateConfigV2ScopedWithGate(path, true, expected, func(c *Config) error {
		ApplyGeneratedListenMigration(c, true)
		return nil
	}); err != nil {
		return nil, err
	}
	cfg, _, err = loadDocument(path, policy.GeneratedLoopbackListenEnabled)
	return cfg, err
}

func load(path string, loopbackDefault bool) (*Config, error) {
	cfg, _, err := loadDocument(path, loopbackDefault)
	return cfg, err
}

func loadDocument(path string, loopbackDefault bool) (*Config, *configDocument, error) {
	if err := ensureConfigDir(filepath.Dir(path)); err != nil {
		return nil, nil, err
	}
	if err := repairConfigFile(path); errors.Is(err, os.ErrNotExist) {
		cfg := withDefaultsForGeneratedListen(Config{SharedKey: NewSharedKey()}, loopbackDefault)
		c := &cfg
		if err := saveAtPath(path, c); err != nil {
			return nil, nil, fmt.Errorf("save initial config: %w", err)
		}
		doc, err := parseConfigDocumentFromPath(path)
		return c, doc, err
	} else if err != nil {
		return nil, nil, err
	}
	doc, err := parseConfigDocumentFromPath(path)
	if err != nil {
		return nil, nil, err
	}
	c := &doc.Config
	*c = withDefaultsForGeneratedListen(*c, loopbackDefault)
	return c, doc, nil
}

func parseConfigDocumentFromPath(path string) (*configDocument, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseConfigDocument(data)
}

func Save(c *Config) error {
	if c != nil {
		if c.ConfigVersion != nil && *c.ConfigVersion != 2 {
			return fmt.Errorf("unsupported_config_version: %d", *c.ConfigVersion)
		}
		if !releaseflags.ConfigV2WriteEnabled && (c.ConfigVersion != nil || c.ConfigRevision != nil) {
			return ErrConfigV2WritesDisabled
		}
	}
	path := Path()
	if err := ensureConfigDir(filepath.Dir(path)); err != nil {
		return err
	}
	return saveAtPath(path, c)
}

func saveAtPath(path string, c *Config) error {
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
