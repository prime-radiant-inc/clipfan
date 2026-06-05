package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
)

type HostRemoveRequest struct {
	ExpectedRevisionState  RevisionState `json:"expected_revision_state"`
	ExpectedConfigRevision *uint64       `json:"expected_config_revision,omitempty"`
	Reason                 string        `json:"reason"`
	LogID                  string        `json:"log_id"`
}

type HostRemoveResult struct {
	HostID            string         `json:"host_id"`
	RemovedStaticPeer bool           `json:"removed_static_peer"`
	RemovedSSHPeer    bool           `json:"removed_ssh_peer"`
	ConfigRevision    *uint64        `json:"config_revision,omitempty"`
	RevisionState     RevisionState  `json:"revision_state"`
	ConfigVersion     *int           `json:"config_version,omitempty"`
	SSHCleanupStatus  map[string]any `json:"ssh_cleanup_status,omitempty"`
}

func DecodeHostRemoveRequest(r io.Reader) (HostRemoveRequest, error) {
	decoder := json.NewDecoder(r)
	var raw map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return HostRemoveRequest{}, fmt.Errorf("malformed_host_remove_request: %w", err)
	}
	if raw == nil {
		return HostRemoveRequest{}, fmt.Errorf("malformed_host_remove_request: expected object")
	}
	if err := rejectTrailingJSON(decoder, "malformed_host_remove_request"); err != nil {
		return HostRemoveRequest{}, err
	}
	for field := range raw {
		switch field {
		case "expected_revision_state", "expected_config_revision", "reason", "log_id":
		default:
			return HostRemoveRequest{}, fmt.Errorf("unknown_field: %s", field)
		}
	}
	state, err := decodeRequiredHostRemoveRevisionState(raw)
	if err != nil {
		return HostRemoveRequest{}, err
	}
	revision, err := decodeHostRemoveExpectedRevision(raw, state)
	if err != nil {
		return HostRemoveRequest{}, err
	}
	reason, err := decodeRequiredHostRemoveStringField(raw, "reason")
	if err != nil {
		return HostRemoveRequest{}, err
	}
	if !stableSSHPeerReasonPattern.MatchString(reason) {
		return HostRemoveRequest{}, fmt.Errorf("invalid_host_remove_field: reason")
	}
	logID, err := decodeRequiredHostRemoveStringField(raw, "log_id")
	if err != nil {
		return HostRemoveRequest{}, err
	}
	return HostRemoveRequest{
		ExpectedRevisionState:  state,
		ExpectedConfigRevision: revision,
		Reason:                 reason,
		LogID:                  logID,
	}, nil
}

func ValidateHostRemoveTarget(host string) error {
	if err := ValidateHostID(host); err == nil {
		return nil
	}
	if _, err := CanonicalSSHHost(host); err == nil {
		return nil
	}
	return fmt.Errorf("invalid_host_remove_id: invalid host")
}

func RemoveHost(path string, hostID string, req HostRemoveRequest) (HostRemoveResult, error) {
	return removeHostWithGate(path, releaseflags.ConfigV2WriteEnabled, hostID, req)
}

func removeHostWithGate(path string, gateEnabled bool, hostID string, req HostRemoveRequest) (HostRemoveResult, error) {
	var result HostRemoveResult
	if err := ValidateHostRemoveTarget(hostID); err != nil {
		return result, err
	}
	if err := validateHostRemoveRequest(req); err != nil {
		return result, err
	}
	expected := RevisionExpectation{
		State:    req.ExpectedRevisionState,
		Revision: copyUint64Ptr(req.ExpectedConfigRevision),
	}

	err := updateRemoveHostConfigRaw(path, gateEnabled, expected, hostID, req, &result)
	if err != nil {
		return HostRemoveResult{}, err
	}
	doc, err := readConfigDocumentLocked(path)
	if err != nil {
		return HostRemoveResult{}, err
	}
	result.HostID = hostID
	result.RevisionState = doc.RevisionState
	result.ConfigRevision = copyUint64Ptr(doc.ConfigRevision)
	result.ConfigVersion = copyIntPtr(doc.Config.ConfigVersion)
	return result, nil
}

func updateRemoveHostConfigRaw(path string, gateEnabled bool, expected RevisionExpectation, hostID string, req HostRemoveRequest, result *HostRemoveResult) error {
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

		cfg := doc.Config
		cfg.StaticPeers = append([]string(nil), doc.Config.StaticPeers...)
		cfg.SSH = cloneSSHConfig(doc.Config.SSH)
		raw := cloneRawMap(doc.raw)

		removedStatic := removeHostFromStaticPeers(&cfg, hostID)
		removedSSH, cleanupStatus, err := removeHostFromSSHPeers(&cfg, raw, hostID, req)
		if err != nil {
			return err
		}
		if !removedStatic && !removedSSH {
			return fmt.Errorf("host_not_found: %s", hostID)
		}
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

		if result != nil {
			result.HostID = hostID
			result.RemovedStaticPeer = removedStatic
			result.RemovedSSHPeer = removedSSH
			result.SSHCleanupStatus = cleanupStatus
		}
		return nil
	})
}

func removeHostFromStaticPeers(cfg *Config, hostID string) bool {
	if cfg == nil || len(cfg.StaticPeers) == 0 {
		return false
	}
	kept := cfg.StaticPeers[:0]
	removed := false
	for _, peer := range cfg.StaticPeers {
		if hostRemoveHostsMatch(peer, hostID) {
			removed = true
			continue
		}
		kept = append(kept, peer)
	}
	cfg.StaticPeers = kept
	return removed
}

func removeHostFromSSHPeers(cfg *Config, raw map[string]json.RawMessage, hostID string, req HostRemoveRequest) (bool, map[string]any, error) {
	_, peers, err := rawSSHPeers(raw)
	if err != nil {
		return false, nil, err
	}
	var matchingIDs []string
	for _, peer := range peers {
		id, err := rawPeerID(peer)
		if err != nil {
			return false, nil, err
		}
		if hostRemoveHostsMatch(id, hostID) {
			matchingIDs = append(matchingIDs, id)
		}
	}
	var cleanupStatus map[string]any
	for _, id := range matchingIDs {
		responsePeer, err := applySSHPeerDelete(cfg, raw, id, SSHPeerDeleteRequest{
			ExpectedConfigRevision: req.ExpectedConfigRevision,
			Reason:                 req.Reason,
			LogID:                  req.LogID,
		})
		if err != nil {
			return false, nil, err
		}
		cleanupStatus, _ = rawMapFieldAsAny(responsePeer, "cleanup_status")
	}
	return len(matchingIDs) > 0, cleanupStatus, nil
}

func rawMapFieldAsAny(raw map[string]json.RawMessage, key string) (map[string]any, bool) {
	value, ok := raw[key]
	if !ok || isJSONNull(value) {
		return nil, false
	}
	var out map[string]any
	if err := json.Unmarshal(value, &out); err != nil {
		return nil, false
	}
	return out, true
}

func validateHostRemoveRequest(req HostRemoveRequest) error {
	if !validRevisionState(req.ExpectedRevisionState) {
		return fmt.Errorf("invalid_host_remove_field: expected_revision_state")
	}
	switch req.ExpectedRevisionState {
	case RevisionStateVersioned:
		if req.ExpectedConfigRevision == nil || *req.ExpectedConfigRevision == 0 {
			return ErrConfigRevisionConflict
		}
	case RevisionStatePreV2, RevisionStateMissingRevision:
		if req.ExpectedConfigRevision != nil {
			return ErrConfigRevisionConflict
		}
	}
	if err := validateStableSSHPeerReason("remove", req.Reason); err != nil {
		return err
	}
	if strings.TrimSpace(req.LogID) == "" {
		return fmt.Errorf("missing_host_remove_field: log_id")
	}
	return nil
}

func decodeRequiredHostRemoveRevisionState(raw map[string]json.RawMessage) (RevisionState, error) {
	value, ok := raw["expected_revision_state"]
	if !ok || isJSONNull(value) {
		return "", fmt.Errorf("missing_host_remove_field: expected_revision_state")
	}
	var state RevisionState
	if err := json.Unmarshal(value, &state); err != nil {
		return "", fmt.Errorf("invalid_host_remove_field: expected_revision_state")
	}
	if !validRevisionState(state) {
		return "", fmt.Errorf("invalid_host_remove_field: expected_revision_state")
	}
	return state, nil
}

func decodeHostRemoveExpectedRevision(raw map[string]json.RawMessage, state RevisionState) (*uint64, error) {
	value, ok := raw["expected_config_revision"]
	if !ok || isJSONNull(value) {
		if state == RevisionStateVersioned {
			return nil, ErrConfigRevisionConflict
		}
		return nil, nil
	}
	revision, err := parseJSONUint(value, "invalid_expected_config_revision")
	if err != nil {
		return nil, err
	}
	if revision == 0 {
		return nil, ErrConfigRevisionConflict
	}
	if state != RevisionStateVersioned {
		return nil, ErrConfigRevisionConflict
	}
	return &revision, nil
}

func decodeRequiredHostRemoveStringField(raw map[string]json.RawMessage, field string) (string, error) {
	value, ok := raw[field]
	if !ok || isJSONNull(value) {
		return "", fmt.Errorf("missing_host_remove_field: %s", field)
	}
	var out string
	if err := json.Unmarshal(value, &out); err != nil {
		return "", fmt.Errorf("invalid_host_remove_field: %s", field)
	}
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("missing_host_remove_field: %s", field)
	}
	return out, nil
}

func validRevisionState(state RevisionState) bool {
	switch state {
	case RevisionStatePreV2, RevisionStateMissingRevision, RevisionStateVersioned:
		return true
	default:
		return false
	}
}

func hostRemoveHostsMatch(a, b string) bool {
	na, nb := normalizeHostRemoveName(a), normalizeHostRemoveName(b)
	return na != "" && na == nb
}

func normalizeHostRemoveName(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil {
		return strings.ToLower(ip.String())
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	host = strings.TrimSuffix(host, ".local")
	return host
}
