package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

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

func UpsertSSHPeer(path string, peerID string, req SSHPeerUpsertRequest) (SSHPeerConfigReadResult, error) {
	return upsertSSHPeerWithGate(path, releaseflags.ConfigV2WriteEnabled, peerID, req)
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

func updateSSHPeerConfigRaw(path string, gateEnabled bool, expected RevisionExpectation, peerID string, req SSHPeerUpsertRequest, result *SSHPeerConfigReadResult) error {
	if !gateEnabled {
		return ErrConfigV2WritesDisabled
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

		updatedPeer, err := applySSHPeerUpsert(&cfg, raw, peerID, req)
		if err != nil {
			return err
		}
		if err := NormalizeLocalSSHPaths(&cfg); err != nil {
			return err
		}
		if err := persistNormalizedLocalSSHPaths(raw, &cfg); err != nil {
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
	index := -1
	var peer map[string]json.RawMessage
	for i, candidate := range peers {
		id, err := rawPeerID(candidate)
		if err != nil {
			return nil, err
		}
		if id == peerID {
			index = i
			peer = cloneRawMap(candidate)
			break
		}
	}
	creating := index == -1
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

	if cfg.SSH == nil {
		cfg.SSH = &SSHConfig{}
	}
	cfg.SSH.Peers = make([]SSHPeer, 0, len(peers))
	for _, rawPeer := range peers {
		typed, err := typedPeerFromRaw(rawPeer)
		if err != nil {
			return nil, err
		}
		cfg.SSH.Peers = append(cfg.SSH.Peers, typed)
	}
	setRaw(sshRaw, "peers", peers)
	setRaw(raw, "ssh", sshRaw)
	return peer, nil
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
