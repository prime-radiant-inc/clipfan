package config

import (
	"fmt"

	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
)

type ConfigRevisionStatus struct {
	ConfigVersion  *int          `json:"config_version,omitempty"`
	ConfigRevision *uint64       `json:"config_revision,omitempty"`
	RevisionState  RevisionState `json:"revision_state"`
}

type SSHLocalMaterialUpdateRequest struct {
	ExpectedConfigRevision *uint64
	Transport              *string
	SharedKey              *string
	SyncKey                *string
	KnownHosts             *string
}

func ReadConfigRevision(path string) (ConfigRevisionStatus, error) {
	doc, err := readConfigDocumentLocked(path)
	if err != nil {
		return ConfigRevisionStatus{}, err
	}
	return ConfigRevisionStatus{
		ConfigVersion:  copyIntPtr(doc.Config.ConfigVersion),
		ConfigRevision: copyUint64Ptr(doc.Config.ConfigRevision),
		RevisionState:  doc.RevisionState,
	}, nil
}

func UpdateSSHLocalMaterial(path string, req SSHLocalMaterialUpdateRequest) (ConfigRevisionStatus, error) {
	return updateSSHLocalMaterialWithGate(path, releaseflags.ConfigV2WriteEnabled, req)
}

func updateSSHLocalMaterialWithGate(path string, gateEnabled bool, req SSHLocalMaterialUpdateRequest) (ConfigRevisionStatus, error) {
	if !gateEnabled {
		return ConfigRevisionStatus{}, ErrConfigV2WritesDisabled
	}
	if req.ExpectedConfigRevision == nil || *req.ExpectedConfigRevision == 0 {
		return ConfigRevisionStatus{}, ErrConfigRevisionConflict
	}
	if req.Transport == nil && req.SharedKey == nil && req.SyncKey == nil && req.KnownHosts == nil {
		return ConfigRevisionStatus{}, fmt.Errorf("ssh_local_material_update_empty")
	}
	if req.Transport != nil && *req.Transport != TransportSSH {
		return ConfigRevisionStatus{}, fmt.Errorf("invalid_transport: %s", *req.Transport)
	}
	if req.SharedKey != nil && !SharedKeyIsStandard32Bytes(*req.SharedKey) {
		return ConfigRevisionStatus{}, fmt.Errorf("invalid_shared_key")
	}
	if req.SyncKey != nil {
		if err := ValidateSSHExecutablePath(*req.SyncKey); err != nil {
			return ConfigRevisionStatus{}, fmt.Errorf("invalid_sync_key: %w", err)
		}
	}
	if req.KnownHosts != nil {
		if err := ValidateSSHExecutablePath(*req.KnownHosts); err != nil {
			return ConfigRevisionStatus{}, fmt.Errorf("invalid_known_hosts: %w", err)
		}
	}

	expected := RevisionExpectation{State: RevisionStateVersioned, Revision: copyUint64Ptr(req.ExpectedConfigRevision)}
	var result ConfigRevisionStatus
	err := withConfigFileLock(path, func() error {
		data, err := readConfigFileSafe(path)
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
		if err := removeStaleConfigV2Temps(path); err != nil {
			return err
		}

		raw := cloneRawMap(doc.raw)
		cfg := doc.Config
		cfg.StaticPeers = append([]string(nil), doc.Config.StaticPeers...)
		cfg.SSH = cloneSSHConfig(doc.Config.SSH)
		if req.Transport != nil {
			cfg.Transport = *req.Transport
			setRaw(raw, "transport", *req.Transport)
		}
		if req.SharedKey != nil {
			cfg.SharedKey = *req.SharedKey
			setRaw(raw, "shared_key", *req.SharedKey)
		}
		if cfg.SSH == nil {
			cfg.SSH = &SSHConfig{}
		}
		sshRaw, _, err := rawSSHPeers(raw)
		if err != nil {
			return err
		}
		if req.SyncKey != nil {
			cfg.SSH.SyncKey = *req.SyncKey
			setRaw(sshRaw, "sync_key", *req.SyncKey)
		}
		if req.KnownHosts != nil {
			cfg.SSH.KnownHosts = *req.KnownHosts
			setRaw(sshRaw, "known_hosts", *req.KnownHosts)
		}
		setRaw(raw, "ssh", sshRaw)
		if err := NormalizeLocalSSHPaths(&cfg); err != nil {
			return err
		}
		if err := ValidateSSHTransportConfig(cfg); err != nil {
			return err
		}
		nextRevision, err := nextConfigRevision(doc)
		if err != nil {
			return err
		}
		out, err := marshalConfigDocumentPreservingRawSSH(doc, cfg, raw, nextRevision)
		if err != nil {
			return err
		}
		if err := writeConfigV2Atomic(path, out, 0o600); err != nil {
			return err
		}
		version := 2
		revision := nextRevision
		result = ConfigRevisionStatus{
			ConfigVersion:  &version,
			ConfigRevision: &revision,
			RevisionState:  RevisionStateVersioned,
		}
		return nil
	})
	if err != nil {
		return ConfigRevisionStatus{}, err
	}
	return result, nil
}
