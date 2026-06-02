package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
)

var (
	ErrConfigV2WritesDisabled = errors.New("config_v2_writes_disabled")
	ErrConfigRevisionConflict = errors.New("config_revision_conflict")
)

type RevisionState string

const (
	RevisionStatePreV2           RevisionState = "pre_v2"
	RevisionStateMissingRevision RevisionState = "missing_revision"
	RevisionStateVersioned       RevisionState = "versioned"
)

type RevisionExpectation struct {
	State    RevisionState
	Revision *uint64
}

type configDocument struct {
	Config         Config
	RevisionState  RevisionState
	ConfigRevision *uint64
	raw            map[string]json.RawMessage
}

var configV2TempCounter uint64

func parseConfigDocument(data []byte) (*configDocument, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("malformed_config: expected object")
	}

	doc := &configDocument{
		RevisionState: RevisionStatePreV2,
		raw:           raw,
	}

	versionRaw, ok := raw["config_version"]
	if !ok {
		cfg, err := unmarshalTypedConfig(raw)
		if err != nil {
			return nil, err
		}
		doc.Config = cfg
		doc.Config.ConfigVersion = nil
		doc.Config.ConfigRevision = nil
		return doc, nil
	}
	version, err := parseJSONUint(versionRaw, "invalid_config_version")
	if err != nil {
		return nil, err
	}
	if version != 2 {
		return nil, fmt.Errorf("unsupported_config_version: %d", version)
	}
	cfg, err := unmarshalTypedConfig(raw)
	if err != nil {
		return nil, err
	}
	versionInt := 2
	doc.Config = cfg
	doc.Config.ConfigVersion = &versionInt
	doc.RevisionState = RevisionStateMissingRevision

	revisionRaw, ok := raw["config_revision"]
	if !ok || bytes.Equal(bytes.TrimSpace(revisionRaw), []byte("null")) {
		doc.Config.ConfigRevision = nil
		return doc, nil
	}
	revision, err := parseJSONUint(revisionRaw, "invalid_config_revision")
	if err != nil {
		return nil, err
	}
	if revision == 0 {
		return nil, fmt.Errorf("invalid_config_revision: must be >= 1")
	}
	doc.RevisionState = RevisionStateVersioned
	doc.ConfigRevision = &revision
	doc.Config.ConfigRevision = &revision
	return doc, nil
}

func unmarshalTypedConfig(raw map[string]json.RawMessage) (Config, error) {
	typedRaw := make(map[string]json.RawMessage, len(raw))
	for key, value := range raw {
		if key == "config_version" || key == "config_revision" {
			continue
		}
		typedRaw[key] = value
	}
	data, err := json.Marshal(typedRaw)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func parseJSONUint(raw json.RawMessage, code string) (uint64, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] < '0' || trimmed[0] > '9' {
		return 0, fmt.Errorf("%s: expected unsigned integer", code)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var num json.Number
	if err := decoder.Decode(&num); err != nil {
		return 0, fmt.Errorf("%s: %w", code, err)
	}
	if decoder.More() {
		return 0, fmt.Errorf("%s: trailing data", code)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return 0, fmt.Errorf("%s: %w", code, err)
		}
		return 0, fmt.Errorf("%s: trailing data", code)
	}
	n, err := strconv.ParseUint(num.String(), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", code, err)
	}
	return n, nil
}

func UpdateConfigV2Scoped(path string, expected RevisionExpectation, mutate func(*Config) error) error {
	return updateConfigV2ScopedWithGate(path, releaseflags.ConfigV2WriteEnabled, expected, mutate)
}

func updateConfigV2ScopedWithGate(path string, gateEnabled bool, expected RevisionExpectation, mutate func(*Config) error) error {
	if !gateEnabled {
		return ErrConfigV2WritesDisabled
	}
	if mutate == nil {
		return fmt.Errorf("missing scoped config mutation")
	}

	dir := filepath.Dir(path)
	if err := ensureConfigDir(dir); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("config file %s is not a regular file", path)
	}

	lockFile, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lockFile.Close()
	if err := lockFile.Chmod(0o600); err != nil {
		return err
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	if err := removeStaleConfigV2Temps(path); err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	doc, err := parseConfigDocument(data)
	if err != nil {
		return err
	}
	if err := validateRevisionExpectation(doc, expected); err != nil {
		return err
	}

	cfg := doc.Config
	if err := mutate(&cfg); err != nil {
		return err
	}
	nextRevision := uint64(1)
	if doc.RevisionState == RevisionStateVersioned {
		if *doc.ConfigRevision == ^uint64(0) {
			return fmt.Errorf("invalid_config_revision: cannot increment max uint64")
		}
		nextRevision = *doc.ConfigRevision + 1
	}
	out, err := marshalConfigDocument(doc, cfg, nextRevision)
	if err != nil {
		return err
	}
	return writeConfigV2Atomic(path, out, 0o600)
}

func validateRevisionExpectation(doc *configDocument, expected RevisionExpectation) error {
	if doc == nil {
		return ErrConfigRevisionConflict
	}
	if doc.RevisionState != expected.State {
		return ErrConfigRevisionConflict
	}
	switch expected.State {
	case RevisionStatePreV2, RevisionStateMissingRevision:
		if expected.Revision != nil {
			return ErrConfigRevisionConflict
		}
	case RevisionStateVersioned:
		if expected.Revision == nil || *expected.Revision == 0 || doc.ConfigRevision == nil || *doc.ConfigRevision != *expected.Revision {
			return ErrConfigRevisionConflict
		}
	default:
		return ErrConfigRevisionConflict
	}
	return nil
}

func marshalConfigDocument(doc *configDocument, cfg Config, nextRevision uint64) ([]byte, error) {
	out := make(map[string]json.RawMessage, len(doc.raw)+2)
	for key, value := range doc.raw {
		rawCopy := make([]byte, len(value))
		copy(rawCopy, value)
		out[key] = rawCopy
	}

	setRawIf(out, "listen", cfg.Listen, hasRaw(doc, "listen") || cfg.Listen != "")
	setRawIf(out, "shared_key", cfg.SharedKey, hasRaw(doc, "shared_key") || cfg.SharedKey != "")
	setRawIf(out, "discovery", cfg.Discovery, hasRaw(doc, "discovery") || cfg.Discovery != "")
	setRawIf(out, "static_peers", cfg.StaticPeers, hasRaw(doc, "static_peers") || len(cfg.StaticPeers) > 0)
	setRawIf(out, "hostname", cfg.Hostname, hasRaw(doc, "hostname") || cfg.Hostname != "")
	setRawIf(out, "port", cfg.Port, hasRaw(doc, "port") || cfg.Port != 0)
	setRawIf(out, "max_history", cfg.MaxHistory, hasRaw(doc, "max_history") || cfg.MaxHistory != 0)
	setRaw(out, "config_version", 2)
	setRaw(out, "config_revision", nextRevision)

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func hasRaw(doc *configDocument, key string) bool {
	_, ok := doc.raw[key]
	return ok
}

func setRawIf(out map[string]json.RawMessage, key string, value any, include bool) {
	if !include {
		delete(out, key)
		return
	}
	setRaw(out, key, value)
}

func setRaw(out map[string]json.RawMessage, key string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	out[key] = data
}

func removeStaleConfigV2Temps(path string) error {
	dir := filepath.Dir(path)
	prefix := configV2TempPrefix(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func configV2TempPrefix(path string) string {
	return ".config-v2-" + filepath.Base(path) + ".tmp."
}

func writeConfigV2Atomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, fmt.Sprintf("%s%d.%d", configV2TempPrefix(path), os.Getpid(), atomic.AddUint64(&configV2TempCounter, 1)))
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
	if err := syncBestEffort(file); err != nil {
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
	return syncDirBestEffort(dir)
}

func syncBestEffort(file *os.File) error {
	if err := file.Sync(); err != nil && !isUnsupportedSync(err) {
		return err
	}
	return nil
}

func syncDirBestEffort(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Sync(); err != nil && !isUnsupportedSync(err) {
		return err
	}
	return nil
}

func isUnsupportedSync(err error) bool {
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.ENOSYS)
}
