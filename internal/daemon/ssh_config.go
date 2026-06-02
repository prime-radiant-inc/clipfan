package daemon

import (
	"bytes"
	"errors"
	"strings"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
)

func (d *Daemon) sshPeerConfigReadHandler(peerID string) (any, *transport.HandlerError) {
	status, err := config.ReadSSHPeer(d.configPath, peerID)
	if err != nil {
		return nil, sshPeerConfigHandlerError(err)
	}
	return status, nil
}

func (d *Daemon) sshPeerConfigPutHandler(peerID string, body []byte) (any, *transport.HandlerError) {
	req, err := config.DecodeSSHPeerUpsertRequest(bytes.NewReader(body))
	if err != nil {
		return nil, sshPeerConfigHandlerError(err)
	}
	status, err := config.UpsertSSHPeer(d.configPath, peerID, req)
	if err != nil {
		return nil, sshPeerConfigHandlerError(err)
	}
	return status, nil
}

func sshPeerConfigHandlerError(err error) *transport.HandlerError {
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
	case hasErrorPrefix(text, "ssh_peer_not_found"):
		return &transport.HandlerError{Status: 404, Code: "ssh_peer_not_found"}
	case hasErrorPrefix(text,
		"unknown_field",
		"missing_ssh_peer_upsert_field",
		"malformed_ssh_peer_upsert_request",
		"invalid_ssh_peer_upsert_field",
		"invalid_expected_config_revision"):
		return &transport.HandlerError{Status: 400, Code: "bad_request"}
	case hasErrorPrefix(text, "invalid_ssh_peer_id"):
		return &transport.HandlerError{Status: 400, Code: "invalid_ssh_peer_id"}
	case hasErrorPrefix(text, "ssh_peer_secret_field_not_allowed"):
		return &transport.HandlerError{Status: 400, Code: "secret_field_not_allowed"}
	case hasErrorPrefix(text, "ssh_peer_id_mismatch"):
		return &transport.HandlerError{Status: 400, Code: "ssh_peer_id_mismatch"}
	case hasErrorPrefix(text, "ssh_peer_migration_state_change_not_allowed"):
		return &transport.HandlerError{Status: 409, Code: "ssh_peer_migration_state_change_not_allowed"}
	case hasErrorPrefix(text, "ssh_peer_create_requires_loopback_unprovisioned"):
		return &transport.HandlerError{Status: 400, Code: "ssh_peer_create_requires_loopback_unprovisioned"}
	case hasErrorPrefix(text, "ssh_peer_create_requires_"):
		return &transport.HandlerError{Status: 400, Code: "invalid_ssh_peer_config"}
	case hasErrorPrefix(text,
		"duplicate_ssh_peer_id",
		"duplicate_ssh_target",
		"ssh_peer_id_is_local_host"):
		return &transport.HandlerError{Status: 409, Code: "ssh_peer_config_conflict"}
	case hasErrorPrefix(text,
		"invalid_ssh_host",
		"invalid_ssh_port",
		"invalid_ssh_user",
		"invalid_install_path",
		"invalid_gateway_path",
		"missing_connect_locator",
		"invalid_accept_only_peer_outbound_mode",
		"invalid_migration_state",
		"invalid_sync_key",
		"invalid_known_hosts",
		"invalid_ssh_max_sessions",
		"invalid_ssh_max_sessions_per_peer",
		"invalid_ssh_log_limit_bytes",
		"invalid_accept_proof",
		"invalid_connect_proof"):
		return &transport.HandlerError{Status: 400, Code: "invalid_ssh_peer_config"}
	default:
		return &transport.HandlerError{Status: 500, Code: "ssh_peer_config_failed"}
	}
}

func hasErrorPrefix(text string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasSuffix(prefix, "_") && strings.HasPrefix(text, prefix) {
			return true
		}
		if text == prefix || strings.HasPrefix(text, prefix+":") {
			return true
		}
	}
	return false
}
