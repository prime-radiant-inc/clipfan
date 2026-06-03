package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
	"github.com/prime-radiant-inc/clipfan/internal/storagecheck"
)

const LocalFleetResetConfirmation = "RESET LOCAL CLIPFAN FLEET"

var (
	ErrFleetResetConfirmationRequired = errors.New("fleet_reset_confirmation_required")
	ErrFleetResetUnsafeListener       = errors.New("fleet_reset_unsafe_listener")
	ErrFleetResetSharedKeyStillValid  = errors.New("fleet_reset_shared_key_still_valid")
	ErrFleetResetSSHMaterialPresent   = errors.New("fleet_reset_ssh_material_present")
)

type LocalFleetResetRequest struct {
	Confirmation           string
	ExpectedRevisionState  RevisionState
	ExpectedConfigRevision *uint64
}

type LocalFleetResetResult struct {
	HostID         string        `json:"hostname"`
	ConfigRevision *uint64       `json:"config_revision,omitempty"`
	RevisionState  RevisionState `json:"revision_state"`
	BackupPath     string        `json:"backup_path,omitempty"`
}

type LocalFleetResetStatus struct {
	HostID            string        `json:"hostname,omitempty"`
	ConfigVersion     *int          `json:"config_version,omitempty"`
	ConfigRevision    *uint64       `json:"config_revision,omitempty"`
	RevisionState     RevisionState `json:"revision_state"`
	SafeListener      bool          `json:"safe_listener"`
	SharedKeyInvalid  bool          `json:"shared_key_invalid"`
	SSHMaterialAbsent bool          `json:"ssh_material_absent"`
}

func ReadLocalFleetResetStatus(path, stateDir string) (LocalFleetResetStatus, error) {
	if stateDir == "" {
		stateDir = StateDir()
	}
	var status LocalFleetResetStatus
	err := tryConfigFileLock(path, func() error {
		data, err := readConfigFileSafe(path)
		if err != nil {
			return err
		}
		doc, err := parseConfigDocument(data)
		if err != nil {
			return err
		}
		status = localFleetResetStatusFromDocument(doc, filepath.Dir(path), stateDir)
		return nil
	})
	return status, err
}

func ResetLocalFleetWithBackup(path, stateDir string, req LocalFleetResetRequest, backupPath string) (LocalFleetResetResult, error) {
	return resetLocalFleetWithGate(path, stateDir, releaseflags.ConfigV2WriteEnabled, req, backupPath, DeriveHostID, NewSharedKey)
}

func resetLocalFleetWithGate(path, stateDir string, gateEnabled bool, req LocalFleetResetRequest, backupPath string, deriveHostID func() (string, error), newSharedKey func() string) (LocalFleetResetResult, error) {
	return resetLocalFleetWithGateAndDeps(path, stateDir, gateEnabled, req, backupPath, localFleetResetDeps{
		deriveHostID: deriveHostID,
		newSharedKey: newSharedKey,
	})
}

type localFleetResetDeps struct {
	deriveHostID     func() (string, error)
	newSharedKey     func() string
	syncKeyChecker   storagecheck.Checker
	syncKeyGenerator SyncKeyGenerator
	now              func() time.Time
}

func resetLocalFleetWithGateAndDeps(path, stateDir string, gateEnabled bool, req LocalFleetResetRequest, backupPath string, deps localFleetResetDeps) (LocalFleetResetResult, error) {
	var result LocalFleetResetResult
	if req.Confirmation != LocalFleetResetConfirmation {
		return result, ErrFleetResetConfirmationRequired
	}
	if deps.deriveHostID == nil {
		return result, fmt.Errorf("missing host ID derivation")
	}
	if deps.newSharedKey == nil {
		return result, fmt.Errorf("missing shared key generator")
	}
	if stateDir == "" {
		stateDir = StateDir()
	}
	expected := RevisionExpectation{
		State:    req.ExpectedRevisionState,
		Revision: copyUint64Ptr(req.ExpectedConfigRevision),
	}
	var stagedSyncKey *stagedSyncKeyReset
	err := updateConfigV2ScopedRawWithBackupAndLock(path, gateEnabled, expected, backupPath, tryConfigFileLock, func(cfg *Config, raw map[string]json.RawMessage) error {
		if cfg == nil {
			return fmt.Errorf("missing config")
		}
		if PlanListener(*cfg, true).SafeMode {
			return ErrFleetResetUnsafeListener
		}
		if sharedKeyIsStandard32Bytes(cfg.SharedKey) {
			return ErrFleetResetSharedKeyStillValid
		}
		if localFleetResetHasUnsupportedSSHMaterial(*cfg, raw, filepath.Dir(path), stateDir) {
			return ErrFleetResetSSHMaterialPresent
		}

		hostID := cfg.Hostname
		if hostID == "" {
			derived, err := deps.deriveHostID()
			if err != nil {
				return fmt.Errorf("derive_host_id: %w", err)
			}
			hostID = derived
		}
		if err := ValidateHostID(hostID); err != nil {
			return err
		}
		key := deps.newSharedKey()
		if !sharedKeyIsStandard32Bytes(key) {
			return fmt.Errorf("invalid_generated_shared_key")
		}

		syncKeyPath := localFleetResetSyncKeyPathForReset(*cfg, filepath.Dir(path))
		if syncKeyPath != "" {
			if cfg.SSH == nil {
				cfg.SSH = &SSHConfig{}
			}
			if cfg.SSH.SyncKey == "" {
				cfg.SSH.SyncKey = syncKeyPath
			}
			if err := ValidateSSHExecutablePath(syncKeyPath); err != nil {
				return fmt.Errorf("invalid_sync_key: %w", err)
			}
			stage, err := stageLocalSyncKeyReset(SyncKeyResetOptions{
				KeyPath:   syncKeyPath,
				HostID:    hostID,
				Checker:   deps.syncKeyChecker,
				Now:       deps.now,
				Generator: deps.syncKeyGenerator,
			})
			if err != nil {
				return err
			}
			stagedSyncKey = &stage
		}

		plan := PlanListener(*cfg, true)
		listen := plan.BindListen
		_, port, ok := splitListenHostPort(listen)
		if !ok {
			port = defaultPort
			listen = DefaultListen(true, port)
		}

		cfg.SharedKey = key
		cfg.Hostname = hostID
		cfg.Listen = listen
		cfg.Port = port
		cfg.Discovery = "static"
		cfg.StaticPeers = nil
		delete(raw, "static_peers")
		delete(raw, "previous_listen")

		result.HostID = hostID
		return nil
	})
	if err != nil {
		if stagedSyncKey != nil {
			stagedSyncKey.rollback()
		}
		return LocalFleetResetResult{}, err
	}
	if stagedSyncKey != nil {
		stagedSyncKey.commit()
	}

	doc, err := readConfigDocumentLocked(path)
	if err != nil {
		return LocalFleetResetResult{}, err
	}
	result.RevisionState = doc.RevisionState
	result.ConfigRevision = copyUint64Ptr(doc.ConfigRevision)
	result.BackupPath = backupPath
	if result.HostID == "" {
		result.HostID = doc.Config.Hostname
	}
	return result, nil
}

func LocalFleetResetBackupPath(configPath string, ts time.Time) string {
	stamp := ts.UTC().Format("20060102T150405Z")
	return filepath.Join(filepath.Dir(configPath), filepath.Base(configPath)+".fleet-reset."+stamp+".bak")
}

func localFleetResetStatusFromDocument(doc *configDocument, configDir, stateDir string) LocalFleetResetStatus {
	if doc == nil {
		return LocalFleetResetStatus{}
	}
	return LocalFleetResetStatus{
		HostID:            doc.Config.Hostname,
		ConfigVersion:     copyIntPtr(doc.Config.ConfigVersion),
		ConfigRevision:    copyUint64Ptr(doc.ConfigRevision),
		RevisionState:     doc.RevisionState,
		SafeListener:      !PlanListener(doc.Config, true).SafeMode,
		SharedKeyInvalid:  !sharedKeyIsStandard32Bytes(doc.Config.SharedKey),
		SSHMaterialAbsent: !localFleetResetHasUnsupportedSSHMaterial(doc.Config, doc.raw, configDir, stateDir),
	}
}

func sharedKeyIsStandard32Bytes(value string) bool {
	if value == "" {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return false
	}
	return base64.StdEncoding.EncodeToString(decoded) == value
}

func localFleetResetHasUnsupportedSSHMaterial(cfg Config, raw map[string]json.RawMessage, configDir, stateDir string) bool {
	resettable := localFleetResetResettableSyncKeyPaths(cfg, configDir)
	for key, value := range raw {
		if localFleetResetUnsupportedSSHMaterialField(key, value) {
			return true
		}
	}
	for root, rels := range map[string][]string{
		configDir: localFleetResetConfigMaterialPaths(),
		stateDir:  localFleetResetStateMaterialPaths(),
	} {
		if root == "" {
			continue
		}
		for _, rel := range rels {
			path := filepath.Join(root, rel)
			if _, ok := resettable[filepath.Clean(path)]; ok {
				continue
			}
			if _, err := os.Lstat(path); err == nil {
				return true
			}
		}
	}
	return false
}

func localFleetResetUnsupportedSSHMaterialField(key string, value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false
	}
	lower := strings.ToLower(key)
	switch lower {
	case "ssh":
		return localFleetResetSSHObjectHasUnsupportedMaterial(trimmed)
	case "sync_key_path",
		"sync_private_key_path",
		"sync_public_key_path",
		"private_key_path",
		"known_hosts_path",
		"authorized_keys_path",
		"managed_authorized_keys",
		"authorized_keys_metadata",
		"transport_current_path":
		return true
	case "transport":
		var transport string
		if err := json.Unmarshal(trimmed, &transport); err != nil {
			return true
		}
		return strings.EqualFold(transport, "ssh")
	default:
		return false
	}
}

func localFleetResetSSHObjectHasUnsupportedMaterial(value json.RawMessage) bool {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(value, &raw); err != nil || raw == nil {
		return true
	}
	for key, field := range raw {
		trimmed := bytes.TrimSpace(field)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			continue
		}
		switch strings.ToLower(key) {
		case "sync_key",
			"max_sessions",
			"max_sessions_per_peer",
			"log_limit_bytes":
			continue
		case "peers":
			var peers []json.RawMessage
			if err := json.Unmarshal(trimmed, &peers); err != nil {
				return true
			}
			if len(peers) > 0 {
				return true
			}
		case "known_hosts":
			var knownHosts string
			if err := json.Unmarshal(trimmed, &knownHosts); err != nil {
				return true
			}
			if knownHosts != "" {
				return true
			}
		default:
			return true
		}
	}
	return false
}

func localFleetResetConfiguredSyncKeyPath(cfg Config) string {
	if cfg.SSH == nil {
		return ""
	}
	return cfg.SSH.SyncKey
}

func localFleetResetSyncKeyPathForReset(cfg Config, configDir string) string {
	if path := localFleetResetConfiguredSyncKeyPath(cfg); path != "" {
		return path
	}
	defaultPath := localFleetResetDefaultSyncKeyPath(configDir)
	for _, path := range syncKeyMaterialPaths(defaultPath) {
		if _, err := os.Lstat(path); err == nil {
			return defaultPath
		}
	}
	return ""
}

func localFleetResetDefaultSyncKeyPath(configDir string) string {
	return filepath.Join(configDir, "ssh", "sync_ed25519")
}

func localFleetResetResettableSyncKeyPaths(cfg Config, configDir string) map[string]struct{} {
	syncKeyPath := localFleetResetSyncKeyPathForReset(cfg, configDir)
	if syncKeyPath == "" {
		return nil
	}
	paths := make(map[string]struct{}, 3)
	for _, path := range syncKeyMaterialPaths(syncKeyPath) {
		paths[filepath.Clean(path)] = struct{}{}
	}
	return paths
}

func localFleetResetStateMaterialPaths() []string {
	return []string{
		"transport-current",
		"transport_current",
		filepath.Join("ssh", "transport-current"),
		filepath.Join("ssh", "transport_current"),
		filepath.Join("ssh", "sync_ed25519"),
		filepath.Join("ssh", "sync_ed25519.pub"),
		filepath.Join("ssh", "sync_ed25519.clipfan.json"),
		filepath.Join("ssh", "sync_ed25519.next"),
		filepath.Join("ssh", "sync_ed25519.next.pub"),
		filepath.Join("ssh", "sync_ed25519.next.clipfan.json"),
		filepath.Join("ssh", "known_hosts"),
		filepath.Join("ssh", "managed_authorized_keys.json"),
		filepath.Join("ssh", "authorized_keys.metadata.json"),
	}
}

func localFleetResetConfigMaterialPaths() []string {
	return []string{
		filepath.Join("ssh", "sync_ed25519"),
		filepath.Join("ssh", "sync_ed25519.pub"),
		filepath.Join("ssh", "sync_ed25519.clipfan.json"),
		filepath.Join("ssh", "sync_ed25519.next"),
		filepath.Join("ssh", "sync_ed25519.next.pub"),
		filepath.Join("ssh", "sync_ed25519.next.clipfan.json"),
		filepath.Join("ssh", "known_hosts"),
		filepath.Join("ssh", "managed_authorized_keys.json"),
		filepath.Join("ssh", "authorized_keys.metadata.json"),
	}
}
