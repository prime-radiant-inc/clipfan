package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
)

type ListenerRepairStatus struct {
	Listen                string        `json:"listen"`
	Port                  int           `json:"port"`
	PreviousListen        *string       `json:"previous_listen,omitempty"`
	ConfiguredListen      string        `json:"configured_listen"`
	EffectiveRepairListen string        `json:"effective_repair_listen"`
	ParseError            string        `json:"parse_error"`
	SafeMode              bool          `json:"safe_mode"`
	ConfigVersion         *int          `json:"config_version"`
	ConfigRevision        *uint64       `json:"config_revision"`
	RevisionState         RevisionState `json:"revision_state"`
}

type ListenerRepairRequest struct {
	ExpectedConfigRevision *uint64       `json:"expected_config_revision"`
	ExpectedRevisionState  RevisionState `json:"expected_revision_state"`
	Listen                 string        `json:"listen"`
	Port                   int           `json:"port"`
	PreviousListen         *string       `json:"previous_listen,omitempty"`
}

func ReadListenerRepairStatus(path string) (ListenerRepairStatus, error) {
	doc, err := readConfigDocumentLocked(path)
	if err != nil {
		return ListenerRepairStatus{}, err
	}
	return listenerRepairStatusFromDocument(doc)
}

func DecodeListenerRepairRequest(r io.Reader) (ListenerRepairRequest, error) {
	decoder := json.NewDecoder(r)
	decoder.UseNumber()

	var raw map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return ListenerRepairRequest{}, fmt.Errorf("malformed_listener_repair_request: %w", err)
	}
	if raw == nil {
		return ListenerRepairRequest{}, fmt.Errorf("malformed_listener_repair_request: expected object")
	}
	if err := rejectTrailingJSON(decoder, "malformed_listener_repair_request"); err != nil {
		return ListenerRepairRequest{}, err
	}

	allowed := map[string]bool{
		"expected_config_revision": true,
		"expected_revision_state":  true,
		"listen":                   true,
		"port":                     true,
		"previous_listen":          true,
	}
	for field := range raw {
		if !allowed[field] {
			return ListenerRepairRequest{}, fmt.Errorf("listener_repair_field_not_allowed: %s", field)
		}
	}

	var req ListenerRepairRequest
	var revisionState string
	if err := decodeRequiredString(raw, "expected_revision_state", &revisionState); err != nil {
		return ListenerRepairRequest{}, err
	}
	req.ExpectedRevisionState = RevisionState(revisionState)
	if err := decodeRequiredString(raw, "listen", &req.Listen); err != nil {
		return ListenerRepairRequest{}, err
	}
	port, err := decodeRequiredPort(raw, "port")
	if err != nil {
		return ListenerRepairRequest{}, err
	}
	req.Port = port

	if _, ok := raw["expected_config_revision"]; !ok {
		return ListenerRepairRequest{}, fmt.Errorf("missing_listener_repair_field: expected_config_revision")
	}
	revision, err := decodeOptionalRevision(raw, "expected_config_revision")
	if err != nil {
		return ListenerRepairRequest{}, err
	}
	req.ExpectedConfigRevision = revision
	previous, err := decodeOptionalString(raw, "previous_listen")
	if err != nil {
		return ListenerRepairRequest{}, err
	}
	req.PreviousListen = previous
	if _, err := req.revisionExpectation(); err != nil {
		return ListenerRepairRequest{}, err
	}
	return req, nil
}

func RepairListener(path string, req ListenerRepairRequest) (ListenerRepairStatus, error) {
	return repairListenerWithGate(path, releaseflags.ConfigV2WriteEnabled, req)
}

func RepairListenerWithBackup(path string, req ListenerRepairRequest, backupPath string) (ListenerRepairStatus, error) {
	return repairListenerWithBackupAndGate(path, releaseflags.ConfigV2WriteEnabled, req, backupPath)
}

func repairListenerWithGate(path string, gateEnabled bool, req ListenerRepairRequest) (ListenerRepairStatus, error) {
	return repairListenerWithBackupAndGate(path, gateEnabled, req, "")
}

func repairListenerWithBackupAndGate(path string, gateEnabled bool, req ListenerRepairRequest, backupPath string) (ListenerRepairStatus, error) {
	expected, err := req.revisionExpectation()
	if err != nil {
		return ListenerRepairStatus{}, err
	}
	err = updateConfigV2ScopedRawWithBackup(path, gateEnabled, expected, backupPath, func(cfg *Config, raw map[string]json.RawMessage) error {
		plan := PlanListener(*cfg, true)
		if !plan.SafeMode {
			return fmt.Errorf("listener_repair_not_required")
		}
		if req.Listen != plan.EffectiveRepairListen {
			return fmt.Errorf("invalid_listener_repair_request: listen must be %q", plan.EffectiveRepairListen)
		}
		if req.Listen != DefaultListen(true, req.Port) {
			return fmt.Errorf("invalid_listener_repair_request: port does not match listen")
		}

		previous := plan.ConfiguredListen
		if req.PreviousListen != nil {
			if *req.PreviousListen != plan.ConfiguredListen {
				return fmt.Errorf("invalid_listener_repair_request: previous_listen must be %q", plan.ConfiguredListen)
			}
			previous = *req.PreviousListen
		}
		if previous == "" {
			return fmt.Errorf("invalid_listener_repair_request: previous_listen is empty")
		}

		if expected.State != RevisionStateVersioned {
			pruneRawForListenerRepairPromotion(raw)
			cfg.Transport = ""
			cfg.SSH = nil
		}
		cfg.Listen = plan.EffectiveRepairListen
		cfg.Port = req.Port
		setRaw(raw, "previous_listen", previous)
		return nil
	})
	if err != nil {
		return ListenerRepairStatus{}, err
	}
	return ReadListenerRepairStatus(path)
}

func listenerRepairStatusFromDocument(doc *configDocument) (ListenerRepairStatus, error) {
	if doc == nil {
		return ListenerRepairStatus{}, fmt.Errorf("missing config document")
	}
	plan := PlanListener(doc.Config, true)
	previous, err := previousListenFromRaw(doc.raw)
	if err != nil {
		return ListenerRepairStatus{}, err
	}
	return ListenerRepairStatus{
		Listen:                doc.Config.Listen,
		Port:                  doc.Config.Port,
		PreviousListen:        previous,
		ConfiguredListen:      plan.ConfiguredListen,
		EffectiveRepairListen: plan.EffectiveRepairListen,
		ParseError:            plan.ParseError,
		SafeMode:              plan.SafeMode,
		ConfigVersion:         copyIntPtr(doc.Config.ConfigVersion),
		ConfigRevision:        copyUint64Ptr(doc.Config.ConfigRevision),
		RevisionState:         doc.RevisionState,
	}, nil
}

func previousListenFromRaw(raw map[string]json.RawMessage) (*string, error) {
	value, ok := raw["previous_listen"]
	if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return nil, nil
	}
	var previous string
	if err := json.Unmarshal(value, &previous); err != nil {
		return nil, fmt.Errorf("invalid_previous_listen: %w", err)
	}
	return &previous, nil
}

func pruneRawForListenerRepairPromotion(raw map[string]json.RawMessage) {
	for key := range raw {
		if !listenerRepairPromotionFieldAllowed(key) {
			delete(raw, key)
		}
	}
}

func listenerRepairPromotionFieldAllowed(key string) bool {
	switch key {
	case "config_version",
		"config_revision",
		"listen",
		"port",
		"previous_listen",
		"shared_key",
		"discovery",
		"static_peers",
		"hostname",
		"max_history":
		return true
	default:
		return false
	}
}

func (r ListenerRepairRequest) revisionExpectation() (RevisionExpectation, error) {
	expected := RevisionExpectation{
		State:    r.ExpectedRevisionState,
		Revision: copyUint64Ptr(r.ExpectedConfigRevision),
	}
	switch r.ExpectedRevisionState {
	case RevisionStatePreV2, RevisionStateMissingRevision:
		if r.ExpectedConfigRevision != nil {
			return RevisionExpectation{}, ErrConfigRevisionConflict
		}
	case RevisionStateVersioned:
		if r.ExpectedConfigRevision == nil || *r.ExpectedConfigRevision == 0 {
			return RevisionExpectation{}, ErrConfigRevisionConflict
		}
	default:
		return RevisionExpectation{}, ErrConfigRevisionConflict
	}
	return expected, nil
}

func rejectTrailingJSON(decoder *json.Decoder, code string) error {
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return fmt.Errorf("%s: %w", code, err)
		}
		return fmt.Errorf("%s: trailing data", code)
	}
	return nil
}

func decodeRequiredString(raw map[string]json.RawMessage, field string, out *string) error {
	value, ok := raw[field]
	if !ok {
		return fmt.Errorf("missing_listener_repair_field: %s", field)
	}
	if err := json.Unmarshal(value, out); err != nil {
		return fmt.Errorf("invalid_listener_repair_field: %s", field)
	}
	return nil
}

func decodeOptionalString(raw map[string]json.RawMessage, field string) (*string, error) {
	value, ok := raw[field]
	if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return nil, nil
	}
	var out string
	if err := json.Unmarshal(value, &out); err != nil {
		return nil, fmt.Errorf("invalid_listener_repair_field: %s", field)
	}
	return &out, nil
}

func decodeRequiredPort(raw map[string]json.RawMessage, field string) (int, error) {
	value, ok := raw[field]
	if !ok {
		return 0, fmt.Errorf("missing_listener_repair_field: %s", field)
	}
	port, err := parseJSONUint(value, "invalid_listener_repair_port")
	if err != nil {
		return 0, err
	}
	if port == 0 || port > 65535 {
		return 0, fmt.Errorf("invalid_listener_repair_port: must be 1-65535")
	}
	return int(port), nil
}

func decodeOptionalRevision(raw map[string]json.RawMessage, field string) (*uint64, error) {
	value, ok := raw[field]
	if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return nil, nil
	}
	revision, err := parseJSONUint(value, "invalid_expected_config_revision")
	if err != nil {
		return nil, err
	}
	if revision == 0 {
		return nil, fmt.Errorf("invalid_expected_config_revision: must be >= 1")
	}
	return &revision, nil
}

func copyIntPtr(v *int) *int {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

func copyUint64Ptr(v *uint64) *uint64 {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}
