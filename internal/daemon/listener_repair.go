package daemon

import (
	"bytes"
	"errors"
	"strings"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
)

func (d *Daemon) listenerRepairStatusHandler() (any, *transport.HandlerError) {
	status, err := config.ReadListenerRepairStatus(d.configPath)
	if err != nil {
		return nil, listenerRepairHandlerError(err)
	}
	return status, nil
}

func (d *Daemon) listenerRepairPatchHandler(body []byte) (any, *transport.HandlerError) {
	req, err := config.DecodeListenerRepairRequest(bytes.NewReader(body))
	if err != nil {
		return nil, listenerRepairHandlerError(err)
	}
	status, err := config.RepairListener(d.configPath, req)
	if err != nil {
		return nil, listenerRepairHandlerError(err)
	}
	return status, nil
}

func listenerRepairHandlerError(err error) *transport.HandlerError {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, config.ErrConfigRevisionConflict):
		return &transport.HandlerError{Status: 409, Code: "config_revision_conflict"}
	case errors.Is(err, config.ErrConfigV2WritesDisabled):
		return &transport.HandlerError{Status: 503, Code: "config_v2_writes_disabled"}
	case errors.Is(err, config.ErrConfigFileUnsafe):
		return &transport.HandlerError{Status: 409, Code: "config_file_unsafe"}
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "listener_repair_field_not_allowed"):
		return &transport.HandlerError{Status: 400, Code: "unknown_field"}
	case strings.Contains(text, "missing_listener_repair_field"),
		strings.Contains(text, "malformed_listener_repair_request"),
		strings.Contains(text, "invalid_listener_repair_field"),
		strings.Contains(text, "invalid_listener_repair_port"),
		strings.Contains(text, "invalid_expected_config_revision"):
		return &transport.HandlerError{Status: 400, Code: "bad_request"}
	case strings.Contains(text, "invalid_listener_repair_request"):
		return &transport.HandlerError{Status: 400, Code: "invalid_listener_repair_request"}
	case strings.Contains(text, "listener_repair_not_required"):
		return &transport.HandlerError{Status: 409, Code: "listener_repair_not_required"}
	default:
		return &transport.HandlerError{Status: 500, Code: "listener_repair_failed"}
	}
}
