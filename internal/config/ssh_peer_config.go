package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
)

type SSHPeerConfigReadResult struct {
	Peer           map[string]any `json:"peer"`
	ConfigRevision *uint64        `json:"config_revision"`
	RevisionState  RevisionState  `json:"revision_state"`
	ConfigVersion  *int           `json:"config_version,omitempty"`
}

type SSHPeerUpsertRequest struct {
	ExpectedConfigRevision *uint64             `json:"expected_config_revision"`
	Peer                   SSHPeerUpsertFields `json:"peer"`
}

type SSHPeerProofPatchRequest struct {
	ExpectedConfigRevision *uint64                       `json:"expected_config_revision"`
	AcceptProof            *SSHPeerDirectionalProofPatch `json:"accept_proof,omitempty"`
	ConnectProof           *SSHPeerDirectionalProofPatch `json:"connect_proof,omitempty"`
}

type SSHPeerTransitionRequest struct {
	ExpectedConfigRevision   *uint64                          `json:"expected_config_revision"`
	FromState                MigrationState                   `json:"from_state"`
	ToState                  MigrationState                   `json:"to_state"`
	Reason                   string                           `json:"reason"`
	LogID                    string                           `json:"log_id"`
	FailedPhase              *string                          `json:"failed_phase,omitempty"`
	RemoteSecretAbsenceProof *SSHPeerRemoteSecretAbsenceProof `json:"remote_secret_absence_proof,omitempty"`
}

type SSHPeerDisableRequest struct {
	ExpectedConfigRevision *uint64 `json:"expected_config_revision"`
	Reason                 string  `json:"reason"`
}

type SSHPeerDeleteRequest struct {
	ExpectedConfigRevision *uint64 `json:"expected_config_revision"`
	Reason                 string  `json:"reason"`
	LogID                  string  `json:"log_id"`
}

type SSHPeerRemoteSecretAbsenceProof struct {
	FailedPhase               string  `json:"failed_phase"`
	SecretWriteCommandSpawned bool    `json:"secret_write_command_spawned"`
	AbsenceVerifiedBy         string  `json:"absence_verified_by"`
	VerifiedAt                string  `json:"verified_at"`
	RemoteConfigRevision      *uint64 `json:"remote_config_revision,omitempty"`
	LogID                     string  `json:"log_id"`
}

type SSHPeerDirectionalProofPatch struct {
	KeyID       string `json:"key_id"`
	GatewayPath string `json:"gateway_path"`
	VerifiedAt  string `json:"verified_at"`
	VerifiedBy  string `json:"verified_by"`
}

type SSHPeerUpsertFields struct {
	ID             *string         `json:"id,omitempty"`
	Enabled        *bool           `json:"enabled,omitempty"`
	Accept         *bool           `json:"accept,omitempty"`
	Connect        *bool           `json:"connect,omitempty"`
	Persistent     *bool           `json:"persistent,omitempty"`
	OnDemand       *bool           `json:"on_demand,omitempty"`
	SSHHost        *string         `json:"ssh_host,omitempty"`
	SSHUser        *string         `json:"ssh_user,omitempty"`
	SSHPort        *int            `json:"ssh_port,omitempty"`
	InstallPath    *string         `json:"install_path,omitempty"`
	GatewayPath    *string         `json:"gateway_path,omitempty"`
	MigrationState *MigrationState `json:"migration_state,omitempty"`

	SharedKey *string `json:"shared_key,omitempty"`
}

var stableSSHPeerReasonPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)

func ReadSSHPeer(path string, peerID string) (SSHPeerConfigReadResult, error) {
	if err := ValidateHostID(peerID); err != nil {
		return SSHPeerConfigReadResult{}, fmt.Errorf("invalid_ssh_peer_id: %w", err)
	}
	doc, err := readConfigDocumentLocked(path)
	if err != nil {
		return SSHPeerConfigReadResult{}, err
	}
	rawPeer, _, err := rawSSHPeerByID(doc.raw, peerID)
	if err != nil {
		return SSHPeerConfigReadResult{}, err
	}
	return sshPeerConfigReadResultFromRaw(doc, rawPeer)
}

func DecodeSSHPeerUpsertRequest(r io.Reader) (SSHPeerUpsertRequest, error) {
	decoder := json.NewDecoder(r)
	var raw map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return SSHPeerUpsertRequest{}, fmt.Errorf("malformed_ssh_peer_upsert_request: %w", err)
	}
	if raw == nil {
		return SSHPeerUpsertRequest{}, fmt.Errorf("malformed_ssh_peer_upsert_request: expected object")
	}
	if err := rejectTrailingJSON(decoder, "malformed_ssh_peer_upsert_request"); err != nil {
		return SSHPeerUpsertRequest{}, err
	}
	for field := range raw {
		switch field {
		case "expected_config_revision", "peer":
		default:
			return SSHPeerUpsertRequest{}, fmt.Errorf("unknown_field: %s", field)
		}
	}
	if _, ok := raw["expected_config_revision"]; !ok {
		return SSHPeerUpsertRequest{}, fmt.Errorf("missing_ssh_peer_upsert_field: expected_config_revision")
	}
	revision, err := decodeSSHPeerExpectedRevision(raw)
	if err != nil {
		return SSHPeerUpsertRequest{}, err
	}
	if revision == nil || *revision == 0 {
		return SSHPeerUpsertRequest{}, ErrConfigRevisionConflict
	}
	peerRaw, ok := raw["peer"]
	if !ok {
		return SSHPeerUpsertRequest{}, fmt.Errorf("missing_ssh_peer_upsert_field: peer")
	}
	peer, err := decodeSSHPeerUpsertFields(peerRaw)
	if err != nil {
		return SSHPeerUpsertRequest{}, err
	}
	return SSHPeerUpsertRequest{ExpectedConfigRevision: revision, Peer: peer}, nil
}

func DecodeSSHPeerProofPatchRequest(r io.Reader) (SSHPeerProofPatchRequest, error) {
	decoder := json.NewDecoder(r)
	var raw map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return SSHPeerProofPatchRequest{}, fmt.Errorf("malformed_ssh_peer_proof_patch_request: %w", err)
	}
	if raw == nil {
		return SSHPeerProofPatchRequest{}, fmt.Errorf("malformed_ssh_peer_proof_patch_request: expected object")
	}
	if err := rejectTrailingJSON(decoder, "malformed_ssh_peer_proof_patch_request"); err != nil {
		return SSHPeerProofPatchRequest{}, err
	}
	for field := range raw {
		switch field {
		case "expected_config_revision", "accept_proof", "connect_proof":
		default:
			return SSHPeerProofPatchRequest{}, fmt.Errorf("unknown_field: %s", field)
		}
	}
	if _, ok := raw["expected_config_revision"]; !ok {
		return SSHPeerProofPatchRequest{}, fmt.Errorf("missing_ssh_peer_proof_patch_field: expected_config_revision")
	}
	revision, err := decodeSSHPeerExpectedRevision(raw)
	if err != nil {
		return SSHPeerProofPatchRequest{}, err
	}
	if revision == nil || *revision == 0 {
		return SSHPeerProofPatchRequest{}, ErrConfigRevisionConflict
	}

	req := SSHPeerProofPatchRequest{ExpectedConfigRevision: revision}
	if value, ok := raw["accept_proof"]; ok {
		proof, err := decodeSSHPeerDirectionalProofPatch(value, "accept_proof")
		if err != nil {
			return SSHPeerProofPatchRequest{}, err
		}
		req.AcceptProof = &proof
	}
	if value, ok := raw["connect_proof"]; ok {
		proof, err := decodeSSHPeerDirectionalProofPatch(value, "connect_proof")
		if err != nil {
			return SSHPeerProofPatchRequest{}, err
		}
		req.ConnectProof = &proof
	}
	if req.AcceptProof == nil && req.ConnectProof == nil {
		return SSHPeerProofPatchRequest{}, fmt.Errorf("ssh_peer_proof_patch_empty")
	}
	return req, nil
}

func DecodeSSHPeerTransitionRequest(r io.Reader) (SSHPeerTransitionRequest, error) {
	decoder := json.NewDecoder(r)
	var raw map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return SSHPeerTransitionRequest{}, fmt.Errorf("malformed_ssh_peer_transition_request: %w", err)
	}
	if raw == nil {
		return SSHPeerTransitionRequest{}, fmt.Errorf("malformed_ssh_peer_transition_request: expected object")
	}
	if err := rejectTrailingJSON(decoder, "malformed_ssh_peer_transition_request"); err != nil {
		return SSHPeerTransitionRequest{}, err
	}
	for field := range raw {
		switch field {
		case "expected_config_revision", "from_state", "to_state", "reason", "log_id", "failed_phase", "remote_secret_absence_proof":
		default:
			return SSHPeerTransitionRequest{}, fmt.Errorf("unknown_field: %s", field)
		}
	}
	if _, ok := raw["expected_config_revision"]; !ok {
		return SSHPeerTransitionRequest{}, fmt.Errorf("missing_ssh_peer_transition_field: expected_config_revision")
	}
	revision, err := decodeSSHPeerExpectedRevision(raw)
	if err != nil {
		return SSHPeerTransitionRequest{}, err
	}
	if revision == nil || *revision == 0 {
		return SSHPeerTransitionRequest{}, ErrConfigRevisionConflict
	}

	fromState, err := decodeRequiredTransitionStringField(raw, "from_state")
	if err != nil {
		return SSHPeerTransitionRequest{}, err
	}
	toState, err := decodeRequiredTransitionStringField(raw, "to_state")
	if err != nil {
		return SSHPeerTransitionRequest{}, err
	}
	reason, err := decodeRequiredTransitionStringField(raw, "reason")
	if err != nil {
		return SSHPeerTransitionRequest{}, err
	}
	logID, err := decodeRequiredTransitionStringField(raw, "log_id")
	if err != nil {
		return SSHPeerTransitionRequest{}, err
	}

	req := SSHPeerTransitionRequest{
		ExpectedConfigRevision: revision,
		FromState:              MigrationState(fromState),
		ToState:                MigrationState(toState),
		Reason:                 reason,
		LogID:                  logID,
	}
	if value, ok := raw["failed_phase"]; ok {
		failedPhase, err := decodeTransitionStringValue(value, "failed_phase")
		if err != nil {
			return SSHPeerTransitionRequest{}, err
		}
		req.FailedPhase = &failedPhase
	}
	if value, ok := raw["remote_secret_absence_proof"]; ok {
		proof, err := decodeSSHPeerRemoteSecretAbsenceProof(value)
		if err != nil {
			return SSHPeerTransitionRequest{}, err
		}
		req.RemoteSecretAbsenceProof = &proof
	}
	return req, nil
}

func DecodeSSHPeerDisableRequest(r io.Reader) (SSHPeerDisableRequest, error) {
	decoder := json.NewDecoder(r)
	var raw map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return SSHPeerDisableRequest{}, fmt.Errorf("malformed_ssh_peer_disable_request: %w", err)
	}
	if raw == nil {
		return SSHPeerDisableRequest{}, fmt.Errorf("malformed_ssh_peer_disable_request: expected object")
	}
	if err := rejectTrailingJSON(decoder, "malformed_ssh_peer_disable_request"); err != nil {
		return SSHPeerDisableRequest{}, err
	}
	for field := range raw {
		switch field {
		case "expected_config_revision", "reason":
		default:
			return SSHPeerDisableRequest{}, fmt.Errorf("unknown_field: %s", field)
		}
	}
	revision, err := decodeRequiredSSHPeerMutationRevision(raw, "disable")
	if err != nil {
		return SSHPeerDisableRequest{}, err
	}
	reason, err := decodeRequiredSSHPeerMutationStringField(raw, "disable", "reason")
	if err != nil {
		return SSHPeerDisableRequest{}, err
	}
	if !stableSSHPeerReasonPattern.MatchString(reason) {
		return SSHPeerDisableRequest{}, fmt.Errorf("invalid_ssh_peer_disable_field: reason")
	}
	return SSHPeerDisableRequest{ExpectedConfigRevision: revision, Reason: reason}, nil
}

func DecodeSSHPeerDeleteRequest(r io.Reader) (SSHPeerDeleteRequest, error) {
	decoder := json.NewDecoder(r)
	var raw map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return SSHPeerDeleteRequest{}, fmt.Errorf("malformed_ssh_peer_delete_request: %w", err)
	}
	if raw == nil {
		return SSHPeerDeleteRequest{}, fmt.Errorf("malformed_ssh_peer_delete_request: expected object")
	}
	if err := rejectTrailingJSON(decoder, "malformed_ssh_peer_delete_request"); err != nil {
		return SSHPeerDeleteRequest{}, err
	}
	for field := range raw {
		switch field {
		case "expected_config_revision", "reason", "log_id":
		default:
			return SSHPeerDeleteRequest{}, fmt.Errorf("unknown_field: %s", field)
		}
	}
	revision, err := decodeRequiredSSHPeerMutationRevision(raw, "delete")
	if err != nil {
		return SSHPeerDeleteRequest{}, err
	}
	reason, err := decodeRequiredSSHPeerMutationStringField(raw, "delete", "reason")
	if err != nil {
		return SSHPeerDeleteRequest{}, err
	}
	if !stableSSHPeerReasonPattern.MatchString(reason) {
		return SSHPeerDeleteRequest{}, fmt.Errorf("invalid_ssh_peer_delete_field: reason")
	}
	logID, err := decodeRequiredSSHPeerMutationStringField(raw, "delete", "log_id")
	if err != nil {
		return SSHPeerDeleteRequest{}, err
	}
	return SSHPeerDeleteRequest{ExpectedConfigRevision: revision, Reason: reason, LogID: logID}, nil
}

func decodeSSHPeerExpectedRevision(raw map[string]json.RawMessage) (*uint64, error) {
	value, ok := raw["expected_config_revision"]
	if !ok || isJSONNull(value) {
		return nil, nil
	}
	revision, err := parseJSONUint(value, "invalid_expected_config_revision")
	if err != nil {
		return nil, err
	}
	if revision == 0 {
		return nil, ErrConfigRevisionConflict
	}
	return &revision, nil
}

func decodeRequiredSSHPeerMutationRevision(raw map[string]json.RawMessage, action string) (*uint64, error) {
	value, ok := raw["expected_config_revision"]
	if !ok || isJSONNull(value) {
		return nil, fmt.Errorf("missing_ssh_peer_%s_field: expected_config_revision", action)
	}
	revision, err := decodeSSHPeerExpectedRevision(raw)
	if err != nil {
		return nil, err
	}
	if revision == nil || *revision == 0 {
		return nil, ErrConfigRevisionConflict
	}
	return revision, nil
}

func decodeRequiredSSHPeerMutationStringField(raw map[string]json.RawMessage, action string, field string) (string, error) {
	value, ok := raw[field]
	if !ok || isJSONNull(value) {
		return "", fmt.Errorf("missing_ssh_peer_%s_field: %s", action, field)
	}
	var out string
	if err := json.Unmarshal(value, &out); err != nil {
		return "", fmt.Errorf("invalid_ssh_peer_%s_field: %s", action, field)
	}
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("missing_ssh_peer_%s_field: %s", action, field)
	}
	return out, nil
}

func UpsertSSHPeer(path string, peerID string, req SSHPeerUpsertRequest) (SSHPeerConfigReadResult, error) {
	return upsertSSHPeerWithGate(path, releaseflags.ConfigV2WriteEnabled, peerID, req)
}

func PatchSSHPeerProof(path string, peerID string, req SSHPeerProofPatchRequest) (SSHPeerConfigReadResult, error) {
	return patchSSHPeerProofWithGate(path, releaseflags.ConfigV2WriteEnabled, peerID, req)
}

func TransitionSSHPeer(path string, peerID string, req SSHPeerTransitionRequest) (SSHPeerConfigReadResult, error) {
	return transitionSSHPeerWithGate(path, releaseflags.ConfigV2WriteEnabled, peerID, req)
}

func DisableSSHPeer(path string, peerID string, req SSHPeerDisableRequest) (SSHPeerConfigReadResult, error) {
	return disableSSHPeerWithGate(path, releaseflags.ConfigV2WriteEnabled, peerID, req)
}

func DeleteSSHPeer(path string, peerID string, req SSHPeerDeleteRequest) (SSHPeerConfigReadResult, error) {
	return deleteSSHPeerWithGate(path, releaseflags.ConfigV2WriteEnabled, peerID, req)
}

func upsertSSHPeerWithGate(path string, gateEnabled bool, peerID string, req SSHPeerUpsertRequest) (SSHPeerConfigReadResult, error) {
	if err := ValidateHostID(peerID); err != nil {
		return SSHPeerConfigReadResult{}, fmt.Errorf("invalid_ssh_peer_id: %w", err)
	}
	if req.ExpectedConfigRevision == nil || *req.ExpectedConfigRevision == 0 {
		return SSHPeerConfigReadResult{}, ErrConfigRevisionConflict
	}
	if req.Peer.ID != nil {
		if err := ValidateHostID(*req.Peer.ID); err != nil {
			return SSHPeerConfigReadResult{}, fmt.Errorf("invalid_ssh_peer_id: %w", err)
		}
		if *req.Peer.ID != peerID {
			return SSHPeerConfigReadResult{}, fmt.Errorf("ssh_peer_id_mismatch")
		}
	}
	if req.Peer.SharedKey != nil {
		return SSHPeerConfigReadResult{}, fmt.Errorf("ssh_peer_secret_field_not_allowed: peer.shared_key")
	}
	expected := RevisionExpectation{State: RevisionStateVersioned, Revision: copyUint64Ptr(req.ExpectedConfigRevision)}

	var result SSHPeerConfigReadResult
	err := updateSSHPeerConfigRaw(path, gateEnabled, expected, peerID, req, &result)
	if err != nil {
		return SSHPeerConfigReadResult{}, err
	}
	return result, nil
}

func patchSSHPeerProofWithGate(path string, gateEnabled bool, peerID string, req SSHPeerProofPatchRequest) (SSHPeerConfigReadResult, error) {
	if err := ValidateHostID(peerID); err != nil {
		return SSHPeerConfigReadResult{}, fmt.Errorf("invalid_ssh_peer_id: %w", err)
	}
	if req.ExpectedConfigRevision == nil || *req.ExpectedConfigRevision == 0 {
		return SSHPeerConfigReadResult{}, ErrConfigRevisionConflict
	}
	if req.AcceptProof == nil && req.ConnectProof == nil {
		return SSHPeerConfigReadResult{}, fmt.Errorf("ssh_peer_proof_patch_empty")
	}
	expected := RevisionExpectation{State: RevisionStateVersioned, Revision: copyUint64Ptr(req.ExpectedConfigRevision)}

	var result SSHPeerConfigReadResult
	err := updateSSHPeerProofConfigRaw(path, gateEnabled, expected, peerID, req, &result)
	if err != nil {
		return SSHPeerConfigReadResult{}, err
	}
	return result, nil
}

func transitionSSHPeerWithGate(path string, gateEnabled bool, peerID string, req SSHPeerTransitionRequest) (SSHPeerConfigReadResult, error) {
	if err := ValidateHostID(peerID); err != nil {
		return SSHPeerConfigReadResult{}, fmt.Errorf("invalid_ssh_peer_id: %w", err)
	}
	if req.ExpectedConfigRevision == nil || *req.ExpectedConfigRevision == 0 {
		return SSHPeerConfigReadResult{}, ErrConfigRevisionConflict
	}
	expected := RevisionExpectation{State: RevisionStateVersioned, Revision: copyUint64Ptr(req.ExpectedConfigRevision)}

	var result SSHPeerConfigReadResult
	err := updateSSHPeerTransitionConfigRaw(path, gateEnabled, expected, peerID, req, &result)
	if err != nil {
		return SSHPeerConfigReadResult{}, err
	}
	return result, nil
}

func disableSSHPeerWithGate(path string, gateEnabled bool, peerID string, req SSHPeerDisableRequest) (SSHPeerConfigReadResult, error) {
	if err := ValidateHostID(peerID); err != nil {
		return SSHPeerConfigReadResult{}, fmt.Errorf("invalid_ssh_peer_id: %w", err)
	}
	if req.ExpectedConfigRevision == nil || *req.ExpectedConfigRevision == 0 {
		return SSHPeerConfigReadResult{}, ErrConfigRevisionConflict
	}
	// Decode helpers validate HTTP bodies, but exported entrypoints repeat this
	// check because callers may construct request structs directly.
	if err := validateStableSSHPeerReason("disable", req.Reason); err != nil {
		return SSHPeerConfigReadResult{}, err
	}
	expected := RevisionExpectation{State: RevisionStateVersioned, Revision: copyUint64Ptr(req.ExpectedConfigRevision)}

	var result SSHPeerConfigReadResult
	err := updateSSHPeerDisableConfigRaw(path, gateEnabled, expected, peerID, req, &result)
	if err != nil {
		return SSHPeerConfigReadResult{}, err
	}
	return result, nil
}

func deleteSSHPeerWithGate(path string, gateEnabled bool, peerID string, req SSHPeerDeleteRequest) (SSHPeerConfigReadResult, error) {
	if err := ValidateHostID(peerID); err != nil {
		return SSHPeerConfigReadResult{}, fmt.Errorf("invalid_ssh_peer_id: %w", err)
	}
	if req.ExpectedConfigRevision == nil || *req.ExpectedConfigRevision == 0 {
		return SSHPeerConfigReadResult{}, ErrConfigRevisionConflict
	}
	// Decode helpers validate HTTP bodies, but exported entrypoints repeat this
	// check because callers may construct request structs directly.
	if err := validateStableSSHPeerReason("delete", req.Reason); err != nil {
		return SSHPeerConfigReadResult{}, err
	}
	if strings.TrimSpace(req.LogID) == "" {
		return SSHPeerConfigReadResult{}, fmt.Errorf("missing_ssh_peer_delete_field: log_id")
	}
	expected := RevisionExpectation{State: RevisionStateVersioned, Revision: copyUint64Ptr(req.ExpectedConfigRevision)}

	var result SSHPeerConfigReadResult
	err := updateSSHPeerDeleteConfigRaw(path, gateEnabled, expected, peerID, req, &result)
	if err != nil {
		return SSHPeerConfigReadResult{}, err
	}
	return result, nil
}

func updateSSHPeerConfigRaw(path string, gateEnabled bool, expected RevisionExpectation, peerID string, req SSHPeerUpsertRequest, result *SSHPeerConfigReadResult) error {
	return withSSHPeerConfigUpdate(path, gateEnabled, expected, result, func(cfg *Config, raw map[string]json.RawMessage) (map[string]json.RawMessage, error) {
		updatedPeer, err := applySSHPeerUpsert(cfg, raw, peerID, req)
		if err != nil {
			return nil, err
		}
		if err := NormalizeLocalSSHPaths(cfg); err != nil {
			return nil, err
		}
		if err := persistNormalizedLocalSSHPaths(raw, cfg); err != nil {
			return nil, err
		}
		return updatedPeer, nil
	})
}

func updateSSHPeerProofConfigRaw(path string, gateEnabled bool, expected RevisionExpectation, peerID string, req SSHPeerProofPatchRequest, result *SSHPeerConfigReadResult) error {
	return withSSHPeerConfigUpdate(path, gateEnabled, expected, result, func(cfg *Config, raw map[string]json.RawMessage) (map[string]json.RawMessage, error) {
		return applySSHPeerProofPatch(cfg, raw, peerID, req)
	})
}

func updateSSHPeerTransitionConfigRaw(path string, gateEnabled bool, expected RevisionExpectation, peerID string, req SSHPeerTransitionRequest, result *SSHPeerConfigReadResult) error {
	return withSSHPeerConfigUpdate(path, gateEnabled, expected, result, func(cfg *Config, raw map[string]json.RawMessage) (map[string]json.RawMessage, error) {
		return applySSHPeerTransition(cfg, raw, peerID, req)
	})
}

func updateSSHPeerDisableConfigRaw(path string, gateEnabled bool, expected RevisionExpectation, peerID string, req SSHPeerDisableRequest, result *SSHPeerConfigReadResult) error {
	return withSSHPeerConfigUpdate(path, gateEnabled, expected, result, func(cfg *Config, raw map[string]json.RawMessage) (map[string]json.RawMessage, error) {
		return applySSHPeerDisable(cfg, raw, peerID, req)
	})
}

func updateSSHPeerDeleteConfigRaw(path string, gateEnabled bool, expected RevisionExpectation, peerID string, req SSHPeerDeleteRequest, result *SSHPeerConfigReadResult) error {
	return withSSHPeerConfigUpdate(path, gateEnabled, expected, result, func(cfg *Config, raw map[string]json.RawMessage) (map[string]json.RawMessage, error) {
		return applySSHPeerDelete(cfg, raw, peerID, req)
	})
}

func withSSHPeerConfigUpdate(path string, gateEnabled bool, expected RevisionExpectation, result *SSHPeerConfigReadResult, apply func(*Config, map[string]json.RawMessage) (map[string]json.RawMessage, error)) error {
	if !gateEnabled {
		return ErrConfigV2WritesDisabled
	}
	if apply == nil {
		return fmt.Errorf("missing ssh peer config mutation")
	}
	return withConfigFileLock(path, func() error {
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

		updatedPeer, err := apply(&cfg, raw)
		if err != nil {
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
		if result != nil {
			revision := nextRevision
			version := 2
			*result = SSHPeerConfigReadResult{
				Peer:           redactRawPeer(updatedPeer),
				ConfigVersion:  &version,
				ConfigRevision: &revision,
				RevisionState:  RevisionStateVersioned,
			}
		}
		return nil
	})
}

func decodeSSHPeerUpsertFields(raw json.RawMessage) (SSHPeerUpsertFields, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return SSHPeerUpsertFields{}, fmt.Errorf("invalid_ssh_peer_upsert_field: peer")
	}
	if fields == nil {
		return SSHPeerUpsertFields{}, fmt.Errorf("invalid_ssh_peer_upsert_field: peer")
	}
	allowed := map[string]bool{
		"id": true, "enabled": true, "accept": true, "connect": true,
		"persistent": true, "on_demand": true, "ssh_host": true, "ssh_user": true,
		"ssh_port": true, "install_path": true, "gateway_path": true, "migration_state": true,
	}
	var req SSHPeerUpsertFields
	for field, value := range fields {
		if field == "shared_key" || secretLikePeerField(field) {
			return SSHPeerUpsertFields{}, fmt.Errorf("ssh_peer_secret_field_not_allowed: peer.%s", field)
		}
		if !allowed[field] {
			return SSHPeerUpsertFields{}, fmt.Errorf("unknown_field: peer.%s", field)
		}
		switch field {
		case "id":
			v, err := decodeStringField(value, field)
			if err != nil {
				return SSHPeerUpsertFields{}, err
			}
			req.ID = &v
		case "enabled":
			v, err := decodeBoolField(value, field)
			if err != nil {
				return SSHPeerUpsertFields{}, err
			}
			req.Enabled = &v
		case "accept":
			v, err := decodeBoolField(value, field)
			if err != nil {
				return SSHPeerUpsertFields{}, err
			}
			req.Accept = &v
		case "connect":
			v, err := decodeBoolField(value, field)
			if err != nil {
				return SSHPeerUpsertFields{}, err
			}
			req.Connect = &v
		case "persistent":
			v, err := decodeBoolField(value, field)
			if err != nil {
				return SSHPeerUpsertFields{}, err
			}
			req.Persistent = &v
		case "on_demand":
			v, err := decodeBoolField(value, field)
			if err != nil {
				return SSHPeerUpsertFields{}, err
			}
			req.OnDemand = &v
		case "ssh_host":
			v, err := decodeStringField(value, field)
			if err != nil {
				return SSHPeerUpsertFields{}, err
			}
			req.SSHHost = &v
		case "ssh_user":
			v, err := decodeStringField(value, field)
			if err != nil {
				return SSHPeerUpsertFields{}, err
			}
			req.SSHUser = &v
		case "install_path":
			v, err := decodeStringField(value, field)
			if err != nil {
				return SSHPeerUpsertFields{}, err
			}
			req.InstallPath = &v
		case "gateway_path":
			v, err := decodeStringField(value, field)
			if err != nil {
				return SSHPeerUpsertFields{}, err
			}
			req.GatewayPath = &v
		case "ssh_port":
			if isJSONNull(value) {
				return SSHPeerUpsertFields{}, fmt.Errorf("invalid_ssh_peer_upsert_field: peer.%s", field)
			}
			var port int
			if err := json.Unmarshal(value, &port); err != nil {
				return SSHPeerUpsertFields{}, fmt.Errorf("invalid_ssh_peer_upsert_field: peer.%s", field)
			}
			req.SSHPort = &port
		case "migration_state":
			v, err := decodeStringField(value, field)
			if err != nil {
				return SSHPeerUpsertFields{}, err
			}
			state := MigrationState(v)
			req.MigrationState = &state
		}
	}
	return req, nil
}

func decodeSSHPeerDirectionalProofPatch(raw json.RawMessage, wrapperField string) (SSHPeerDirectionalProofPatch, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return SSHPeerDirectionalProofPatch{}, fmt.Errorf("invalid_ssh_peer_proof_patch_field: %s", wrapperField)
	}
	if fields == nil {
		return SSHPeerDirectionalProofPatch{}, fmt.Errorf("invalid_ssh_peer_proof_patch_field: %s", wrapperField)
	}
	allowed := map[string]bool{
		"key_id": true, "gateway_path": true, "verified_at": true, "verified_by": true,
	}
	for field := range fields {
		if !allowed[field] {
			return SSHPeerDirectionalProofPatch{}, fmt.Errorf("unknown_field: %s.%s", wrapperField, field)
		}
	}
	var proof SSHPeerDirectionalProofPatch
	var err error
	if proof.KeyID, err = decodeRequiredProofPatchStringField(fields, wrapperField, "key_id"); err != nil {
		return SSHPeerDirectionalProofPatch{}, err
	}
	if proof.GatewayPath, err = decodeRequiredProofPatchStringField(fields, wrapperField, "gateway_path"); err != nil {
		return SSHPeerDirectionalProofPatch{}, err
	}
	if proof.VerifiedAt, err = decodeRequiredProofPatchStringField(fields, wrapperField, "verified_at"); err != nil {
		return SSHPeerDirectionalProofPatch{}, err
	}
	if proof.VerifiedBy, err = decodeRequiredProofPatchStringField(fields, wrapperField, "verified_by"); err != nil {
		return SSHPeerDirectionalProofPatch{}, err
	}
	if err := validateProofFields(proof.KeyID, proof.GatewayPath, proof.VerifiedAt, proof.VerifiedBy); err != nil {
		return SSHPeerDirectionalProofPatch{}, fmt.Errorf("invalid_ssh_peer_proof_patch_field: %s: %w", wrapperField, err)
	}
	return proof, nil
}

func decodeRequiredProofPatchStringField(fields map[string]json.RawMessage, wrapperField, field string) (string, error) {
	value, ok := fields[field]
	if !ok || isJSONNull(value) {
		return "", fmt.Errorf("missing_ssh_peer_proof_patch_field: %s.%s", wrapperField, field)
	}
	var out string
	if err := json.Unmarshal(value, &out); err != nil {
		return "", fmt.Errorf("invalid_ssh_peer_proof_patch_field: %s.%s", wrapperField, field)
	}
	return out, nil
}

func decodeSSHPeerRemoteSecretAbsenceProof(raw json.RawMessage) (SSHPeerRemoteSecretAbsenceProof, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return SSHPeerRemoteSecretAbsenceProof{}, fmt.Errorf("invalid_ssh_peer_transition_field: remote_secret_absence_proof")
	}
	if fields == nil {
		return SSHPeerRemoteSecretAbsenceProof{}, fmt.Errorf("invalid_ssh_peer_transition_field: remote_secret_absence_proof")
	}
	allowed := map[string]bool{
		"failed_phase": true, "secret_write_command_spawned": true, "absence_verified_by": true,
		"verified_at": true, "remote_config_revision": true, "log_id": true,
	}
	for field := range fields {
		if !allowed[field] {
			return SSHPeerRemoteSecretAbsenceProof{}, fmt.Errorf("unknown_field: remote_secret_absence_proof.%s", field)
		}
	}

	failedPhase, err := decodeRequiredRemoteAbsenceProofStringField(fields, "failed_phase")
	if err != nil {
		return SSHPeerRemoteSecretAbsenceProof{}, err
	}
	spawned, err := decodeRequiredRemoteAbsenceProofBoolField(fields, "secret_write_command_spawned")
	if err != nil {
		return SSHPeerRemoteSecretAbsenceProof{}, err
	}
	absenceVerifiedBy, err := decodeRequiredRemoteAbsenceProofStringField(fields, "absence_verified_by")
	if err != nil {
		return SSHPeerRemoteSecretAbsenceProof{}, err
	}
	verifiedAt, err := decodeRequiredRemoteAbsenceProofStringField(fields, "verified_at")
	if err != nil {
		return SSHPeerRemoteSecretAbsenceProof{}, err
	}
	logID, err := decodeRequiredRemoteAbsenceProofStringField(fields, "log_id")
	if err != nil {
		return SSHPeerRemoteSecretAbsenceProof{}, err
	}
	proof := SSHPeerRemoteSecretAbsenceProof{
		FailedPhase:               failedPhase,
		SecretWriteCommandSpawned: spawned,
		AbsenceVerifiedBy:         absenceVerifiedBy,
		VerifiedAt:                verifiedAt,
		LogID:                     logID,
	}
	if value, ok := fields["remote_config_revision"]; ok {
		if isJSONNull(value) {
			return SSHPeerRemoteSecretAbsenceProof{}, fmt.Errorf("invalid_ssh_peer_transition_field: remote_secret_absence_proof.remote_config_revision")
		}
		remoteRevision, err := parseJSONUint(value, "invalid_ssh_peer_transition_field: remote_secret_absence_proof.remote_config_revision")
		if err != nil {
			return SSHPeerRemoteSecretAbsenceProof{}, err
		}
		proof.RemoteConfigRevision = &remoteRevision
	}
	return proof, nil
}

func decodeRequiredTransitionStringField(fields map[string]json.RawMessage, field string) (string, error) {
	value, ok := fields[field]
	if !ok || isJSONNull(value) {
		return "", fmt.Errorf("missing_ssh_peer_transition_field: %s", field)
	}
	out, err := decodeTransitionStringValue(value, field)
	if err != nil {
		return "", err
	}
	return out, nil
}

func decodeTransitionStringValue(value json.RawMessage, field string) (string, error) {
	if isJSONNull(value) {
		return "", fmt.Errorf("invalid_ssh_peer_transition_field: %s", field)
	}
	var out string
	if err := json.Unmarshal(value, &out); err != nil {
		return "", fmt.Errorf("invalid_ssh_peer_transition_field: %s", field)
	}
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("invalid_ssh_peer_transition_field: %s", field)
	}
	return out, nil
}

func decodeRequiredRemoteAbsenceProofStringField(fields map[string]json.RawMessage, field string) (string, error) {
	value, ok := fields[field]
	if !ok || isJSONNull(value) {
		return "", fmt.Errorf("missing_ssh_peer_transition_field: remote_secret_absence_proof.%s", field)
	}
	var out string
	if err := json.Unmarshal(value, &out); err != nil {
		return "", fmt.Errorf("invalid_ssh_peer_transition_field: remote_secret_absence_proof.%s", field)
	}
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("invalid_ssh_peer_transition_field: remote_secret_absence_proof.%s", field)
	}
	return out, nil
}

func decodeRequiredRemoteAbsenceProofBoolField(fields map[string]json.RawMessage, field string) (bool, error) {
	value, ok := fields[field]
	if !ok || isJSONNull(value) {
		return false, fmt.Errorf("missing_ssh_peer_transition_field: remote_secret_absence_proof.%s", field)
	}
	var out bool
	if err := json.Unmarshal(value, &out); err != nil {
		return false, fmt.Errorf("invalid_ssh_peer_transition_field: remote_secret_absence_proof.%s", field)
	}
	return out, nil
}

func decodeBoolField(raw json.RawMessage, field string) (bool, error) {
	if isJSONNull(raw) {
		return false, fmt.Errorf("invalid_ssh_peer_upsert_field: peer.%s", field)
	}
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		return false, fmt.Errorf("invalid_ssh_peer_upsert_field: peer.%s", field)
	}
	return v, nil
}

func decodeStringField(raw json.RawMessage, field string) (string, error) {
	if isJSONNull(raw) {
		return "", fmt.Errorf("invalid_ssh_peer_upsert_field: peer.%s", field)
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", fmt.Errorf("invalid_ssh_peer_upsert_field: peer.%s", field)
	}
	return v, nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func applySSHPeerUpsert(cfg *Config, raw map[string]json.RawMessage, peerID string, req SSHPeerUpsertRequest) (map[string]json.RawMessage, error) {
	if req.Peer.ID != nil {
		if err := ValidateHostID(*req.Peer.ID); err != nil {
			return nil, fmt.Errorf("invalid_ssh_peer_id: %w", err)
		}
	}
	sshRaw, peers, err := rawSSHPeers(raw)
	if err != nil {
		return nil, err
	}
	index, peer, found, err := findRawPeerForUpdate(peers, peerID)
	if err != nil {
		return nil, err
	}
	creating := !found
	if creating {
		peer = map[string]json.RawMessage{}
		setRaw(peer, "id", peerID)
	}

	currentState, err := rawPeerMigrationState(peer)
	if err != nil {
		return nil, err
	}
	if creating {
		if req.Peer.MigrationState == nil || *req.Peer.MigrationState != MigrationStateLoopbackUnprovisioned {
			return nil, fmt.Errorf("ssh_peer_create_requires_loopback_unprovisioned")
		}
		if err := validateSSHPeerCreateFields(req.Peer); err != nil {
			return nil, err
		}
	} else if req.Peer.MigrationState != nil && *req.Peer.MigrationState != currentState {
		return nil, fmt.Errorf("ssh_peer_migration_state_change_not_allowed")
	}
	if req.Peer.MigrationState != nil {
		setRaw(peer, "migration_state", *req.Peer.MigrationState)
	}
	setRawPointer(peer, "enabled", req.Peer.Enabled)
	setRawPointer(peer, "accept", req.Peer.Accept)
	setRawPointer(peer, "connect", req.Peer.Connect)
	setRawPointer(peer, "persistent", req.Peer.Persistent)
	setRawPointer(peer, "on_demand", req.Peer.OnDemand)
	setRawPointer(peer, "ssh_user", req.Peer.SSHUser)
	setRawPointer(peer, "ssh_port", req.Peer.SSHPort)
	setRawPointer(peer, "install_path", req.Peer.InstallPath)
	setRawPointer(peer, "gateway_path", req.Peer.GatewayPath)
	if req.Peer.SSHHost != nil {
		host, err := CanonicalSSHHost(*req.Peer.SSHHost)
		if err != nil {
			return nil, fmt.Errorf("invalid_ssh_host: %w", err)
		}
		setRaw(peer, "ssh_host", host)
	}

	typedPeer, err := typedPeerFromRaw(peer)
	if err != nil {
		return nil, err
	}
	if err := validateSSHPeer(cfg.Hostname, typedPeer, cfg.Transport == TransportSSH); err != nil {
		return nil, err
	}
	if creating {
		peers = append(peers, peer)
	} else {
		peers[index] = peer
	}
	return peer, rebuildTypedSSHPeersAndWriteBack(cfg, raw, sshRaw, peers)
}

func applySSHPeerProofPatch(cfg *Config, raw map[string]json.RawMessage, peerID string, req SSHPeerProofPatchRequest) (map[string]json.RawMessage, error) {
	sshRaw, peers, err := rawSSHPeers(raw)
	if err != nil {
		return nil, err
	}
	index, peer, found, err := findRawPeerForUpdate(peers, peerID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("ssh_peer_not_found: %s", peerID)
	}

	typedPeer, err := typedPeerFromRaw(peer)
	if err != nil {
		return nil, err
	}
	if req.AcceptProof != nil && (!typedPeer.Enabled || !typedPeer.Accept) {
		return nil, fmt.Errorf("proof_mismatch: accept")
	}
	if req.ConnectProof != nil && (!typedPeer.Enabled || !typedPeer.Connect) {
		return nil, fmt.Errorf("proof_mismatch: connect")
	}

	proofRaw, err := rawPeerProofObject(peer)
	if err != nil {
		return nil, err
	}
	if req.AcceptProof != nil {
		setDirectionalProofRaw(proofRaw, DirectionAccept, *req.AcceptProof)
	}
	if req.ConnectProof != nil {
		setDirectionalProofRaw(proofRaw, DirectionConnect, *req.ConnectProof)
	}
	setRaw(peer, "proof", proofRaw)

	peers[index] = peer
	return peer, rebuildTypedSSHPeersAndWriteBack(cfg, raw, sshRaw, peers)
}

func applySSHPeerTransition(cfg *Config, raw map[string]json.RawMessage, peerID string, req SSHPeerTransitionRequest) (map[string]json.RawMessage, error) {
	sshRaw, peers, err := rawSSHPeers(raw)
	if err != nil {
		return nil, err
	}
	index, peer, found, err := findRawPeerForUpdate(peers, peerID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("ssh_peer_not_found: %s", peerID)
	}

	currentState, err := rawPeerMigrationState(peer)
	if err != nil {
		return nil, err
	}
	if currentState != req.FromState {
		return nil, fmt.Errorf("ssh_peer_transition_state_mismatch")
	}
	if err := validateSSHPeerTransitionRequest(peer, req); err != nil {
		return nil, err
	}

	if req.ToState == MigrationStateLoopbackUnprovisioned {
		delete(peer, "proof")
		if _, err := scrubSecretLikeRawFields(peer, map[string]struct{}{"migration_log": {}}); err != nil {
			return nil, err
		}
	}
	setRaw(peer, "migration_state", req.ToState)
	if err := appendSSHPeerMigrationLog(peer, req); err != nil {
		return nil, err
	}

	peers[index] = peer
	return peer, rebuildTypedSSHPeersAndWriteBack(cfg, raw, sshRaw, peers)
}

func applySSHPeerDisable(cfg *Config, raw map[string]json.RawMessage, peerID string, req SSHPeerDisableRequest) (map[string]json.RawMessage, error) {
	sshRaw, peers, err := rawSSHPeers(raw)
	if err != nil {
		return nil, err
	}
	index, peer, found, err := findRawPeerForUpdate(peers, peerID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("ssh_peer_not_found: %s", peerID)
	}
	currentState, err := rawPeerMigrationState(peer)
	if err != nil {
		return nil, err
	}
	if !validSSHPeerConfigMutationState(currentState) {
		return nil, fmt.Errorf("invalid_ssh_peer_disable_state: %s", currentState)
	}

	// Disable records each explicit request as a durable audit event, even if
	// the peer was already disabled before this mutation.
	setRaw(peer, "enabled", false)
	if err := appendSSHAuditLog(sshRaw, sshPeerDisableAuditEntry(peerID, currentState, req.Reason)); err != nil {
		return nil, err
	}

	peers[index] = peer
	return peer, rebuildTypedSSHPeersAndWriteBack(cfg, raw, sshRaw, peers)
}

func applySSHPeerDelete(cfg *Config, raw map[string]json.RawMessage, peerID string, req SSHPeerDeleteRequest) (map[string]json.RawMessage, error) {
	sshRaw, peers, err := rawSSHPeers(raw)
	if err != nil {
		return nil, err
	}
	index, peer, found, err := findRawPeerForUpdate(peers, peerID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("ssh_peer_not_found: %s", peerID)
	}
	currentState, err := rawPeerMigrationState(peer)
	if err != nil {
		return nil, err
	}
	if !validSSHPeerConfigMutationState(currentState) {
		return nil, fmt.Errorf("invalid_ssh_peer_delete_state: %s", currentState)
	}

	removedAt := time.Now().UTC().Format(time.RFC3339)
	responsePeer := cloneRawMap(peer)
	if err := appendSSHAuditLog(sshRaw, sshPeerDeleteAuditEntry(peerID, currentState, req, removedAt)); err != nil {
		return nil, err
	}
	cleanupStatus, remediation, err := sshPeerDeleteCleanupStatus(peerID, currentState, peer, req, removedAt)
	if err != nil {
		return nil, err
	}
	if remediation != nil {
		if err := appendSSHRemediation(sshRaw, remediation); err != nil {
			return nil, err
		}
	}
	setRaw(responsePeer, "cleanup_status", cleanupStatus)

	peers = append(peers[:index], peers[index+1:]...)
	return responsePeer, rebuildTypedSSHPeersAndWriteBack(cfg, raw, sshRaw, peers)
}

func validateSSHPeerTransitionRequest(peer map[string]json.RawMessage, req SSHPeerTransitionRequest) error {
	if !validSSHPeerTransitionState(req.FromState) {
		return fmt.Errorf("invalid_ssh_peer_transition_state: from_state")
	}
	if !validSSHPeerTransitionState(req.ToState) {
		return fmt.Errorf("invalid_ssh_peer_transition_state: to_state")
	}
	if strings.TrimSpace(req.Reason) == "" {
		return fmt.Errorf("missing_ssh_peer_transition_field: reason")
	}
	if strings.TrimSpace(req.LogID) == "" {
		return fmt.Errorf("missing_ssh_peer_transition_field: log_id")
	}
	if req.FromState == req.ToState {
		return fmt.Errorf("ssh_peer_transition_not_allowed: %s_to_%s", req.FromState, req.ToState)
	}
	if req.ToState != MigrationStateProvisionFailed {
		if req.FailedPhase != nil {
			return fmt.Errorf("invalid_ssh_peer_transition_field: failed_phase")
		}
		if req.RemoteSecretAbsenceProof != nil {
			return fmt.Errorf("invalid_ssh_peer_transition_field: remote_secret_absence_proof")
		}
	}

	typedPeer, err := typedPeerFromRaw(peer)
	if err != nil {
		return err
	}

	switch {
	case req.FromState == MigrationStateLoopbackUnprovisioned && req.ToState == MigrationStateSSHMaterialStaged:
		return validateSSHPeerTransitionTargetStaged(typedPeer)
	case (req.FromState == MigrationStateLoopbackUnprovisioned || req.FromState == MigrationStateSSHMaterialStaged) && req.ToState == MigrationStateProvisionFailed:
		return validateSSHPeerProvisionFailedTransition(req)
	case req.FromState == MigrationStateProvisionFailed && (req.ToState == MigrationStateLoopbackUnprovisioned || req.ToState == MigrationStateSSHMaterialStaged):
		if req.Reason != "retry_progress" {
			return fmt.Errorf("invalid_ssh_peer_transition_reason: %s", req.Reason)
		}
		if req.ToState == MigrationStateSSHMaterialStaged {
			return validateSSHPeerTransitionTargetStaged(typedPeer)
		}
		return nil
	case req.FromState == MigrationStateSSHMaterialStaged && req.ToState == MigrationStateSharedKeyWrittenUnverified:
		if req.Reason != "remote_shared_key_written" && req.Reason != "secret_write_outcome_unknown" {
			return fmt.Errorf("invalid_ssh_peer_transition_reason: %s", req.Reason)
		}
		return nil
	case req.FromState == MigrationStateSharedKeyWrittenUnverified && req.ToState == MigrationStateSSHKeysReady:
		if req.Reason != "gateway_version_verified" {
			return fmt.Errorf("invalid_ssh_peer_transition_reason: %s", req.Reason)
		}
		return validateSSHPeerTransitionEnabledProofs(typedPeer)
	case req.FromState == MigrationStateSSHMaterialStaged && req.ToState == MigrationStateSSHKeysReady:
		if req.Reason != "ssh_material_verified" {
			return fmt.Errorf("invalid_ssh_peer_transition_reason: %s", req.Reason)
		}
		return validateSSHPeerTransitionEnabledProofs(typedPeer)
	case (req.FromState == MigrationStateSharedKeyWrittenUnverified || req.FromState == MigrationStateSSHKeysReady) && req.ToState == MigrationStateSSHMaterialStaged:
		if req.Reason != "remote_shared_key_cleanup_verified" {
			return fmt.Errorf("invalid_ssh_peer_transition_reason: %s", req.Reason)
		}
		return nil
	case req.FromState == MigrationStateSSHKeysReady && req.ToState == MigrationStateLoopbackUnprovisioned:
		if !identityResetOrRemovalPrepReason(req.Reason) {
			return fmt.Errorf("invalid_ssh_peer_transition_reason: %s", req.Reason)
		}
		return nil
	default:
		return fmt.Errorf("ssh_peer_transition_not_allowed: %s_to_%s", req.FromState, req.ToState)
	}
}

func validSSHPeerTransitionState(state MigrationState) bool {
	switch state {
	case MigrationStateLoopbackUnprovisioned,
		MigrationStateSSHMaterialStaged,
		MigrationStateProvisionFailed,
		MigrationStateSharedKeyWrittenUnverified,
		MigrationStateSSHKeysReady:
		return true
	default:
		return false
	}
}

func validateSSHPeerTransitionTargetStaged(peer SSHPeer) error {
	if !peer.Enabled {
		return nil
	}
	if peer.Accept && peer.GatewayPath == "" {
		return fmt.Errorf("ssh_peer_transition_requires_accept_material")
	}
	if peer.Connect {
		if peer.SSHHost == "" || peer.SSHUser == "" || peer.SSHPort == 0 || peer.InstallPath == "" || peer.GatewayPath == "" {
			return fmt.Errorf("ssh_peer_transition_requires_connect_material")
		}
	}
	return validateSSHPeerTransitionEnabledProofs(peer)
}

func validateSSHPeerTransitionEnabledProofs(peer SSHPeer) error {
	if err := ValidateDirectionalProof(peer, DirectionAccept); err != nil {
		return fmt.Errorf("ssh_peer_transition_requires_current_proof: %w", err)
	}
	if err := ValidateDirectionalProof(peer, DirectionConnect); err != nil {
		return fmt.Errorf("ssh_peer_transition_requires_current_proof: %w", err)
	}
	return nil
}

func validateSSHPeerProvisionFailedTransition(req SSHPeerTransitionRequest) error {
	if req.FailedPhase == nil || strings.TrimSpace(*req.FailedPhase) == "" {
		return fmt.Errorf("missing_ssh_peer_transition_field: failed_phase")
	}
	if req.RemoteSecretAbsenceProof == nil {
		return fmt.Errorf("missing_ssh_peer_transition_field: remote_secret_absence_proof")
	}
	proof := req.RemoteSecretAbsenceProof
	if proof.FailedPhase != *req.FailedPhase {
		return fmt.Errorf("ssh_peer_transition_absence_proof_failed_phase_mismatch")
	}
	if _, err := time.Parse(time.RFC3339, proof.VerifiedAt); err != nil {
		return fmt.Errorf("invalid_ssh_peer_transition_field: remote_secret_absence_proof.verified_at: %w", err)
	}
	if !proof.SecretWriteCommandSpawned && !preSecretProvisionFailedPhase(proof.FailedPhase) {
		return fmt.Errorf("invalid_ssh_peer_transition_failed_phase: %s", proof.FailedPhase)
	}
	return nil
}

func preSecretProvisionFailedPhase(phase string) bool {
	switch phase {
	case "host_key_confirmation",
		"upload_install",
		"identity_probe",
		"daemon_stop",
		"required_sync_key_provision",
		"known_hosts_provision",
		"non_secret_config_write",
		"managed_authorized_keys_write",
		"pre_secret_forced_command_probe",
		"local_peer_create",
		"local_proof_patch",
		"staged_transition":
		return true
	default:
		return false
	}
}

func identityResetOrRemovalPrepReason(reason string) bool {
	switch reason {
	case "identity_reset_prepared",
		"identity_removal_prepared":
		return true
	default:
		return false
	}
}

func appendSSHPeerMigrationLog(peer map[string]json.RawMessage, req SSHPeerTransitionRequest) error {
	var log []json.RawMessage
	if value, ok := peer["migration_log"]; ok && !isJSONNull(value) {
		if err := json.Unmarshal(value, &log); err != nil {
			return fmt.Errorf("invalid_ssh_peer_migration_log: %w", err)
		}
	}

	entry := map[string]any{
		"from_state": req.FromState,
		"to_state":   req.ToState,
		"reason":     req.Reason,
		"log_id":     req.LogID,
	}
	if req.FailedPhase != nil {
		entry["failed_phase"] = *req.FailedPhase
	}
	if req.RemoteSecretAbsenceProof != nil {
		proof := map[string]any{
			"failed_phase":                 req.RemoteSecretAbsenceProof.FailedPhase,
			"secret_write_command_spawned": req.RemoteSecretAbsenceProof.SecretWriteCommandSpawned,
			"absence_verified_by":          req.RemoteSecretAbsenceProof.AbsenceVerifiedBy,
			"verified_at":                  req.RemoteSecretAbsenceProof.VerifiedAt,
			"log_id":                       req.RemoteSecretAbsenceProof.LogID,
		}
		if req.RemoteSecretAbsenceProof.RemoteConfigRevision != nil {
			proof["remote_config_revision"] = *req.RemoteSecretAbsenceProof.RemoteConfigRevision
		}
		entry["remote_secret_absence_proof"] = proof
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	log = append(log, data)
	setRaw(peer, "migration_log", log)
	return nil
}

func validateStableSSHPeerReason(action string, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("missing_ssh_peer_%s_field: reason", action)
	}
	if !stableSSHPeerReasonPattern.MatchString(reason) {
		return fmt.Errorf("invalid_ssh_peer_%s_field: reason", action)
	}
	return nil
}

func sshPeerDisableAuditEntry(peerID string, previousState MigrationState, reason string) map[string]any {
	return map[string]any{
		"source":                   "ssh_peer_disable",
		"durable":                  true,
		"peer_id":                  peerID,
		"previous_migration_state": previousState,
		"reason":                   reason,
		"disabled_at":              time.Now().UTC().Format(time.RFC3339),
	}
}

func sshPeerDeleteAuditEntry(peerID string, previousState MigrationState, req SSHPeerDeleteRequest, removedAt string) map[string]any {
	return map[string]any{
		"source":                   "ssh_peer_delete",
		"durable":                  true,
		"peer_id":                  peerID,
		"previous_migration_state": previousState,
		"reason":                   req.Reason,
		"log_id":                   req.LogID,
		"removed_at":               removedAt,
	}
}

func sshPeerDeleteCleanupStatus(peerID string, previousState MigrationState, peer map[string]json.RawMessage, req SSHPeerDeleteRequest, removedAt string) (map[string]any, map[string]any, error) {
	switch previousState {
	case MigrationStateLoopbackUnprovisioned:
		return map[string]any{
			"source":           "ssh_peer_delete",
			"durable":          true,
			"cleanup_required": false,
			"pending":          false,
		}, nil, nil
	case MigrationStateSSHMaterialStaged:
		return sshMaterialCleanupResult(peerID, previousState, peer, req, removedAt)
	case MigrationStateProvisionFailed:
		if provisionFailedHasRemoteSecretAbsenceProof(peer) {
			return sshMaterialCleanupResult(peerID, previousState, peer, req, removedAt)
		}
		return postSecretTombstoneResult(peerID, previousState, peer, req, removedAt)
	case MigrationStateSharedKeyWrittenUnverified, MigrationStateSSHKeysReady:
		return postSecretTombstoneResult(peerID, previousState, peer, req, removedAt)
	default:
		return nil, nil, fmt.Errorf("invalid_ssh_peer_delete_state: %s", previousState)
	}
}

func sshMaterialCleanupResult(peerID string, previousState MigrationState, peer map[string]json.RawMessage, req SSHPeerDeleteRequest, removedAt string) (map[string]any, map[string]any, error) {
	record, err := sshPeerCleanupRecord("ssh_material_cleanup", peerID, previousState, peer, req, removedAt)
	if err != nil {
		return nil, nil, err
	}
	record["cleanup_required"] = true
	record["pending"] = true
	record["cleanup_verified"] = false
	record["remaining_user_actions"] = []string{"retry_regular_ssh_cleanup", "dismiss_after_acknowledgement"}
	return sshPeerCleanupStatus("ssh_material_cleanup", true), record, nil
}

func postSecretTombstoneResult(peerID string, previousState MigrationState, peer map[string]json.RawMessage, req SSHPeerDeleteRequest, removedAt string) (map[string]any, map[string]any, error) {
	record, err := sshPeerCleanupRecord("post_secret_tombstone", peerID, previousState, peer, req, removedAt)
	if err != nil {
		return nil, nil, err
	}
	record["cleanup_required"] = true
	record["pending"] = true
	record["remote_fleet_secret_cleanup_verified"] = false
	record["remote_managed_key_cleanup_verified"] = false
	record["remaining_user_actions"] = []string{"retry_regular_ssh_cleanup", "rotate_fleet_shared_key", "dismiss_after_acknowledgement"}
	return sshPeerCleanupStatus("post_secret_tombstone", true), record, nil
}

func validSSHPeerConfigMutationState(state MigrationState) bool {
	switch state {
	case MigrationStateLoopbackUnprovisioned,
		MigrationStateSSHMaterialStaged,
		MigrationStateSharedKeyWrittenUnverified,
		MigrationStateSSHKeysReady,
		MigrationStateProvisionFailed:
		return true
	default:
		return false
	}
}

func sshPeerCleanupStatus(source string, pending bool) map[string]any {
	return map[string]any{
		"source":           source,
		"durable":          true,
		"cleanup_required": pending,
		"pending":          pending,
	}
}

func sshPeerCleanupRecord(source string, peerID string, previousState MigrationState, peer map[string]json.RawMessage, req SSHPeerDeleteRequest, removedAt string) (map[string]any, error) {
	record := map[string]any{
		"source":                   source,
		"durable":                  true,
		"peer_id":                  peerID,
		"previous_migration_state": previousState,
		"removed_at":               removedAt,
		"reason":                   req.Reason,
		"log_id":                   req.LogID,
	}
	copyRawStringToRecord(record, peer, "ssh_host")
	copyRawStringToRecord(record, peer, "ssh_user")
	copyRawIntToRecord(record, peer, "ssh_port")
	copyRawStringToRecord(record, peer, "install_path")
	copyRawStringToRecord(record, peer, "gateway_path")
	proofRaw, err := rawPeerProofObject(peer)
	if err != nil {
		return nil, err
	}
	copyRawStringToRecord(record, proofRaw, "accept_key_id")
	copyRawStringToRecord(record, proofRaw, "accept_gateway_path")
	copyRawStringToRecord(record, proofRaw, "connect_key_id")
	copyRawStringToRecord(record, proofRaw, "connect_gateway_path")
	if failedPhase := lastSSHPeerFailedPhase(peer); failedPhase != "" {
		record["last_provisioning_phase"] = failedPhase
	}
	return record, nil
}

func appendSSHAuditLog(sshRaw map[string]json.RawMessage, entry map[string]any) error {
	return appendSSHRawObjectArray(sshRaw, "audit_log", entry, "invalid_ssh_peer_audit_log")
}

func appendSSHRemediation(sshRaw map[string]json.RawMessage, entry map[string]any) error {
	return appendSSHRawObjectArray(sshRaw, "remediation", entry, "invalid_ssh_peer_remediation")
}

func appendSSHRawObjectArray(sshRaw map[string]json.RawMessage, field string, entry map[string]any, errorPrefix string) error {
	var existing []json.RawMessage
	if value, ok := sshRaw[field]; ok && !isJSONNull(value) {
		if err := json.Unmarshal(value, &existing); err != nil {
			return fmt.Errorf("%s: %w", errorPrefix, err)
		}
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	existing = append(existing, data)
	setRaw(sshRaw, field, existing)
	return nil
}

func copyRawStringToRecord(record map[string]any, raw map[string]json.RawMessage, field string) {
	value, ok := raw[field]
	if !ok || isJSONNull(value) {
		return
	}
	var decoded string
	if err := json.Unmarshal(value, &decoded); err == nil && decoded != "" {
		record[field] = decoded
	}
}

func copyRawIntToRecord(record map[string]any, raw map[string]json.RawMessage, field string) {
	value, ok := raw[field]
	if !ok || isJSONNull(value) {
		return
	}
	var decoded int
	if err := json.Unmarshal(value, &decoded); err == nil && decoded != 0 {
		record[field] = decoded
	}
}

func lastSSHPeerFailedPhase(peer map[string]json.RawMessage) string {
	value, ok := peer["migration_log"]
	if !ok || isJSONNull(value) {
		return ""
	}
	var log []map[string]json.RawMessage
	if err := json.Unmarshal(value, &log); err != nil {
		return ""
	}
	for i := len(log) - 1; i >= 0; i-- {
		var failedPhase string
		if value, ok := log[i]["failed_phase"]; ok && json.Unmarshal(value, &failedPhase) == nil && failedPhase != "" {
			return failedPhase
		}
		if value, ok := log[i]["remote_secret_absence_proof"]; ok {
			var proof map[string]json.RawMessage
			if err := json.Unmarshal(value, &proof); err == nil {
				if failedValue, ok := proof["failed_phase"]; ok && json.Unmarshal(failedValue, &failedPhase) == nil && failedPhase != "" {
					return failedPhase
				}
			}
		}
	}
	return ""
}

func provisionFailedHasRemoteSecretAbsenceProof(peer map[string]json.RawMessage) bool {
	value, ok := peer["migration_log"]
	if !ok || isJSONNull(value) {
		return false
	}
	var log []map[string]json.RawMessage
	if err := json.Unmarshal(value, &log); err != nil {
		return false
	}
	for i := len(log) - 1; i >= 0; i-- {
		value, ok := log[i]["remote_secret_absence_proof"]
		if !ok || isJSONNull(value) {
			continue
		}
		var proof map[string]json.RawMessage
		if err := json.Unmarshal(value, &proof); err != nil {
			continue
		}
		if _, ok := proof["failed_phase"]; !ok {
			return false
		}
		spawnedValue, ok := proof["secret_write_command_spawned"]
		if !ok || isJSONNull(spawnedValue) {
			return false
		}
		var spawned bool
		if err := json.Unmarshal(spawnedValue, &spawned); err != nil {
			return false
		}
		return !spawned
	}
	return false
}

func rebuildTypedSSHPeersAndWriteBack(cfg *Config, raw map[string]json.RawMessage, sshRaw map[string]json.RawMessage, peers []map[string]json.RawMessage) error {
	if cfg.SSH == nil {
		cfg.SSH = &SSHConfig{}
	}
	cfg.SSH.Peers = make([]SSHPeer, 0, len(peers))
	for _, rawPeer := range peers {
		typed, err := typedPeerFromRaw(rawPeer)
		if err != nil {
			return err
		}
		cfg.SSH.Peers = append(cfg.SSH.Peers, typed)
	}
	setRaw(sshRaw, "peers", peers)
	setRaw(raw, "ssh", sshRaw)
	return nil
}

func rawPeerProofObject(peer map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	proofRaw := map[string]json.RawMessage{}
	if value, ok := peer["proof"]; ok && !isJSONNull(value) {
		if err := json.Unmarshal(value, &proofRaw); err != nil {
			return nil, fmt.Errorf("invalid_ssh_peer_proof: %w", err)
		}
		if proofRaw == nil {
			return nil, fmt.Errorf("invalid_ssh_peer_proof: expected object")
		}
	}
	return proofRaw, nil
}

func setDirectionalProofRaw(proofRaw map[string]json.RawMessage, direction ProofDirection, proof SSHPeerDirectionalProofPatch) {
	switch direction {
	case DirectionAccept:
		setRaw(proofRaw, "accept_key_id", proof.KeyID)
		setRaw(proofRaw, "accept_gateway_path", proof.GatewayPath)
		setRaw(proofRaw, "accept_verified_at", proof.VerifiedAt)
		setRaw(proofRaw, "accept_verified_by", proof.VerifiedBy)
	case DirectionConnect:
		setRaw(proofRaw, "connect_key_id", proof.KeyID)
		setRaw(proofRaw, "connect_gateway_path", proof.GatewayPath)
		setRaw(proofRaw, "connect_verified_at", proof.VerifiedAt)
		setRaw(proofRaw, "connect_verified_by", proof.VerifiedBy)
	}
}

func validateSSHPeerCreateFields(peer SSHPeerUpsertFields) error {
	if peer.ID == nil {
		return fmt.Errorf("ssh_peer_create_requires_id")
	}
	if peer.Enabled == nil {
		return fmt.Errorf("ssh_peer_create_requires_enabled")
	}
	if peer.Accept == nil {
		return fmt.Errorf("ssh_peer_create_requires_accept")
	}
	if peer.Connect == nil {
		return fmt.Errorf("ssh_peer_create_requires_connect")
	}
	if !*peer.Accept && !*peer.Connect {
		return fmt.Errorf("ssh_peer_create_requires_direction")
	}
	if *peer.Connect {
		if peer.Persistent == nil || peer.OnDemand == nil {
			return fmt.Errorf("ssh_peer_create_requires_outbound_mode")
		}
		if peer.SSHHost == nil || peer.SSHUser == nil || peer.SSHPort == nil {
			return fmt.Errorf("ssh_peer_create_requires_connect_locator")
		}
		if peer.InstallPath == nil {
			return fmt.Errorf("ssh_peer_create_requires_install_path")
		}
		if peer.GatewayPath == nil {
			return fmt.Errorf("ssh_peer_create_requires_gateway_path")
		}
	}
	return nil
}

func persistNormalizedLocalSSHPaths(raw map[string]json.RawMessage, cfg *Config) error {
	if cfg == nil || cfg.SSH == nil {
		return nil
	}
	sshRaw, _, err := rawSSHPeers(raw)
	if err != nil {
		return err
	}
	persistNormalizedLocalSSHPath(sshRaw, "sync_key", cfg.SSH.SyncKey)
	persistNormalizedLocalSSHPath(sshRaw, "known_hosts", cfg.SSH.KnownHosts)
	setRaw(raw, "ssh", sshRaw)
	return nil
}

func persistNormalizedLocalSSHPath(sshRaw map[string]json.RawMessage, key string, normalized string) {
	value, ok := sshRaw[key]
	if !ok {
		return
	}
	var current string
	if err := json.Unmarshal(value, &current); err != nil {
		return
	}
	if needsHomeExpansion(current) {
		setRaw(sshRaw, key, normalized)
	}
}

func rawSSHPeers(raw map[string]json.RawMessage) (map[string]json.RawMessage, []map[string]json.RawMessage, error) {
	sshRaw := map[string]json.RawMessage{}
	if value, ok := raw["ssh"]; ok && !isJSONNull(value) {
		if err := json.Unmarshal(value, &sshRaw); err != nil {
			return nil, nil, fmt.Errorf("invalid_ssh_config: %w", err)
		}
		if sshRaw == nil {
			sshRaw = map[string]json.RawMessage{}
		}
	}
	var peers []map[string]json.RawMessage
	if value, ok := sshRaw["peers"]; ok && !isJSONNull(value) {
		if err := json.Unmarshal(value, &peers); err != nil {
			return nil, nil, fmt.Errorf("invalid_ssh_peers: %w", err)
		}
	}
	return sshRaw, peers, nil
}

func rawSSHPeerByID(raw map[string]json.RawMessage, peerID string) (map[string]json.RawMessage, int, error) {
	_, peers, err := rawSSHPeers(raw)
	if err != nil {
		return nil, -1, err
	}
	for i, peer := range peers {
		id, err := rawPeerID(peer)
		if err != nil {
			return nil, -1, err
		}
		if id == peerID {
			return peer, i, nil
		}
	}
	return nil, -1, fmt.Errorf("ssh_peer_not_found: %s", peerID)
}

func findRawPeerForUpdate(peers []map[string]json.RawMessage, peerID string) (int, map[string]json.RawMessage, bool, error) {
	for i, candidate := range peers {
		id, err := rawPeerID(candidate)
		if err != nil {
			return -1, nil, false, err
		}
		if id == peerID {
			return i, cloneRawMap(candidate), true, nil
		}
	}
	return -1, nil, false, nil
}

func rawPeerID(peer map[string]json.RawMessage) (string, error) {
	value, ok := peer["id"]
	if !ok {
		return "", fmt.Errorf("invalid_ssh_peer_id: missing")
	}
	var id string
	if err := json.Unmarshal(value, &id); err != nil {
		return "", fmt.Errorf("invalid_ssh_peer_id: %w", err)
	}
	if err := ValidateHostID(id); err != nil {
		return "", fmt.Errorf("invalid_ssh_peer_id: %w", err)
	}
	return id, nil
}

func rawPeerMigrationState(peer map[string]json.RawMessage) (MigrationState, error) {
	value, ok := peer["migration_state"]
	if !ok || isJSONNull(value) {
		return "", nil
	}
	var state string
	if err := json.Unmarshal(value, &state); err != nil {
		return "", fmt.Errorf("invalid_migration_state: %w", err)
	}
	return MigrationState(state), nil
}

func typedPeerFromRaw(peer map[string]json.RawMessage) (SSHPeer, error) {
	data, err := json.Marshal(peer)
	if err != nil {
		return SSHPeer{}, err
	}
	var typed SSHPeer
	if err := json.Unmarshal(data, &typed); err != nil {
		return SSHPeer{}, err
	}
	return typed, nil
}

func sshPeerConfigReadResultFromRaw(doc *configDocument, rawPeer map[string]json.RawMessage) (SSHPeerConfigReadResult, error) {
	if doc == nil {
		return SSHPeerConfigReadResult{}, fmt.Errorf("missing config document")
	}
	return SSHPeerConfigReadResult{
		Peer:           redactRawPeer(rawPeer),
		ConfigVersion:  copyIntPtr(doc.Config.ConfigVersion),
		ConfigRevision: copyUint64Ptr(doc.Config.ConfigRevision),
		RevisionState:  doc.RevisionState,
	}, nil
}

func redactRawPeer(rawPeer map[string]json.RawMessage) map[string]any {
	out := map[string]any{}
	for key, value := range rawPeer {
		var decoded any
		decoder := json.NewDecoder(bytes.NewReader(value))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded); err != nil {
			continue
		}
		redacted, ok := redactDecodedPeerField(key, decoded, true)
		if !ok {
			continue
		}
		out[key] = redacted
	}
	return out
}

func redactDecodedPeerField(key string, value any, topLevelPeerField bool) (any, bool) {
	if secretLikePeerField(key) {
		return nil, false
	}
	if strings.EqualFold(key, "proof") {
		return redactProofValue(value)
	}
	if topLevelPeerField && !safeTopLevelPeerScalarField(key) && !peerContainerValue(value) {
		return nil, false
	}
	return redactDecodedPeerValue(value), true
}

func redactDecodedPeerValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, nested := range typed {
			redacted, ok := redactDecodedPeerField(key, nested, false)
			if !ok {
				continue
			}
			out[key] = redacted
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, nested := range typed {
			out = append(out, redactDecodedPeerValue(nested))
		}
		return out
	default:
		return value
	}
}

func scrubSecretLikeRawFields(raw map[string]json.RawMessage, preserveKeys map[string]struct{}) (bool, error) {
	changed := false
	for key, value := range raw {
		if _, preserve := preserveKeys[key]; preserve {
			continue
		}
		if secretLikePeerField(key) {
			delete(raw, key)
			changed = true
			continue
		}
		scrubbed, valueChanged, err := scrubSecretLikeRawValue(value)
		if err != nil {
			return false, err
		}
		if valueChanged {
			raw[key] = scrubbed
			changed = true
		}
	}
	return changed, nil
}

func scrubSecretLikeRawValue(raw json.RawMessage) (json.RawMessage, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw, false, nil
	}
	switch trimmed[0] {
	case '{':
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return nil, false, fmt.Errorf("invalid_ssh_peer_secret_scrub: %w", err)
		}
		if object == nil {
			return raw, false, nil
		}
		changed, err := scrubSecretLikeRawFields(object, nil)
		if err != nil {
			return nil, false, err
		}
		if !changed {
			return raw, false, nil
		}
		out, err := json.Marshal(object)
		if err != nil {
			return nil, false, err
		}
		return out, true, nil
	case '[':
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, false, fmt.Errorf("invalid_ssh_peer_secret_scrub: %w", err)
		}
		changed := false
		for i, value := range values {
			scrubbed, nestedChanged, err := scrubSecretLikeRawValue(value)
			if err != nil {
				return nil, false, err
			}
			if nestedChanged {
				values[i] = scrubbed
				changed = true
			}
		}
		if !changed {
			return raw, false, nil
		}
		out, err := json.Marshal(values)
		if err != nil {
			return nil, false, err
		}
		return out, true, nil
	default:
		return raw, false, nil
	}
}

func safeTopLevelPeerScalarField(field string) bool {
	switch field {
	case "id", "enabled", "accept", "connect", "persistent", "on_demand",
		"ssh_host", "ssh_user", "ssh_port", "install_path", "gateway_path", "migration_state":
		return true
	default:
		return false
	}
}

func peerContainerValue(value any) bool {
	switch value.(type) {
	case map[string]any, []any:
		return true
	default:
		return false
	}
}

func redactProofValue(value any) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return redactDecodedPeerValue(typed), true
	case []any:
		out := make([]any, 0, len(typed))
		for _, nested := range typed {
			switch nested.(type) {
			case map[string]any, []any:
				out = append(out, redactDecodedPeerValue(nested))
			}
		}
		return out, true
	default:
		return nil, false
	}
}

func secretLikePeerField(field string) bool {
	lower := strings.ToLower(field)
	if lower == "proof" {
		return false
	}
	// Response redaction preserves known peer scalars and unknown containers, but
	// strips known/pattern-matched secret names at every depth before recursing.
	switch lower {
	case "shared_key", "private_key", "private_key_path", "sync_key", "sync_key_path", "accept_proof", "connect_proof":
		return true
	}
	return strings.Contains(lower, "shared_key") ||
		strings.Contains(lower, "secret") ||
		strings.Contains(lower, "token") ||
		strings.Contains(lower, "seed") ||
		strings.Contains(lower, "password") ||
		strings.Contains(lower, "credential") ||
		strings.Contains(lower, "private") ||
		strings.Contains(lower, "hmac") ||
		strings.Contains(lower, "nonce") ||
		strings.Contains(lower, "encrypted") ||
		strings.Contains(lower, "clipboard") ||
		strings.Contains(lower, "signed_frame")
}

func cloneRawMap(in map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(in))
	for key, value := range in {
		copyValue := make([]byte, len(value))
		copy(copyValue, value)
		out[key] = copyValue
	}
	return out
}

func setRawPointer[T any](raw map[string]json.RawMessage, key string, value *T) {
	if value == nil {
		return
	}
	setRaw(raw, key, *value)
}

func marshalConfigDocumentPreservingRawSSH(doc *configDocument, cfg Config, raw map[string]json.RawMessage, nextRevision uint64) ([]byte, error) {
	out := cloneRawMap(raw)
	applyConfigScalars(out, doc, cfg, nextRevision)
	if cfg.SSH == nil {
		delete(out, "ssh")
	} else if _, ok := out["ssh"]; !ok {
		setRaw(out, "ssh", cfg.SSH)
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
