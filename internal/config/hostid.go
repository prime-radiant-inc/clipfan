package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
)

var (
	ErrHostIDNotPersisted = errors.New("host_id_not_persisted")
	ErrHostIDMismatch     = errors.New("host_id_mismatch")
	ErrInvalidHostID      = errors.New("invalid_host_id")
)

type HostIDMigrationResult struct {
	HostID         string
	RevisionState  RevisionState
	ConfigRevision *uint64
	Migrated       bool
}

func ValidateHostID(hostID string) error {
	if len(hostID) < 1 {
		return fmt.Errorf("%w: empty", ErrInvalidHostID)
	}
	if len(hostID) > 63 {
		return fmt.Errorf("%w: too long", ErrInvalidHostID)
	}
	if hostID[0] == '-' {
		return fmt.Errorf("%w: must not start with hyphen", ErrInvalidHostID)
	}
	for _, ch := range hostID {
		if ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '.' || ch == '_' || ch == '-' {
			continue
		}
		return fmt.Errorf("%w: contains invalid character %q", ErrInvalidHostID, ch)
	}
	return nil
}

func DeriveHostID() (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", err
	}
	hostID := deriveHostIDFromHostname(hostname)
	if err := ValidateHostID(hostID); err != nil {
		return "", err
	}
	return hostID, nil
}

func deriveHostIDFromHostname(hostname string) string {
	hostname = strings.TrimSuffix(hostname, ".local")
	return strings.SplitN(hostname, ".", 2)[0]
}

func RequirePersistedHostID(cfg *Config) (string, error) {
	if cfg == nil || cfg.Hostname == "" {
		return "", ErrHostIDNotPersisted
	}
	if err := ValidateHostID(cfg.Hostname); err != nil {
		return "", err
	}
	return cfg.Hostname, nil
}

func RequirePersistedHostIDAtRevision(path string, expectedHostID string, expected RevisionExpectation) (HostIDMigrationResult, error) {
	var result HostIDMigrationResult
	if err := ValidateHostID(expectedHostID); err != nil {
		return result, err
	}

	err := withConfigFileLock(path, func() error {
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
		if doc.Config.Hostname == "" {
			return ErrHostIDNotPersisted
		}
		if err := ValidateHostID(doc.Config.Hostname); err != nil {
			return err
		}
		if doc.Config.Hostname != expectedHostID {
			return ErrHostIDMismatch
		}
		result = HostIDMigrationResult{
			HostID:         doc.Config.Hostname,
			RevisionState:  doc.RevisionState,
			ConfigRevision: doc.ConfigRevision,
		}
		return nil
	})
	return result, err
}

func EnsurePersistedHostID(path string) (HostIDMigrationResult, error) {
	return ensurePersistedHostIDWithGate(path, releaseflags.ConfigV2WriteEnabled, DeriveHostID)
}

func ensurePersistedHostIDWithGate(path string, gateEnabled bool, derive func() (string, error)) (HostIDMigrationResult, error) {
	var result HostIDMigrationResult
	if derive == nil {
		return result, fmt.Errorf("missing host ID derivation")
	}

	err := withConfigFileLock(path, func() error {
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
		result = HostIDMigrationResult{
			HostID:         doc.Config.Hostname,
			RevisionState:  doc.RevisionState,
			ConfigRevision: doc.ConfigRevision,
		}
		if doc.Config.Hostname != "" {
			if err := ValidateHostID(doc.Config.Hostname); err != nil {
				return err
			}
			return nil
		}
		if doc.RevisionState != RevisionStatePreV2 && !gateEnabled {
			return ErrConfigV2WritesDisabled
		}

		hostID, err := derive()
		if err != nil {
			return fmt.Errorf("derive_host_id: %w", err)
		}
		if err := ValidateHostID(hostID); err != nil {
			return err
		}

		cfg := doc.Config
		cfg.Hostname = hostID
		var out []byte
		var nextRevision *uint64
		if doc.RevisionState == RevisionStatePreV2 {
			out, err = marshalLegacyHostIDDocument(doc, hostID)
			if err != nil {
				return err
			}
		} else {
			revision, err := nextConfigRevision(doc)
			if err != nil {
				return err
			}
			out, err = marshalConfigDocument(doc, cfg, revision)
			if err != nil {
				return err
			}
			nextRevision = &revision
		}
		if err := writeConfigV2Atomic(path, out, 0o600); err != nil {
			return err
		}

		result.HostID = hostID
		result.Migrated = true
		if nextRevision != nil {
			result.RevisionState = RevisionStateVersioned
			result.ConfigRevision = nextRevision
		}
		return nil
	})
	return result, err
}

func marshalLegacyHostIDDocument(doc *configDocument, hostID string) ([]byte, error) {
	out := make(map[string]json.RawMessage, len(doc.raw)+1)
	for key, value := range doc.raw {
		rawCopy := make([]byte, len(value))
		copy(rawCopy, value)
		out[key] = rawCopy
	}
	delete(out, "config_version")
	delete(out, "config_revision")
	setRaw(out, "hostname", hostID)
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
