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

func (d *Daemon) sshPeerConfigProofPatchHandler(peerID string, body []byte) (any, *transport.HandlerError) {
	req, err := config.DecodeSSHPeerProofPatchRequest(bytes.NewReader(body))
	if err != nil {
		return nil, sshPeerConfigHandlerError(err)
	}
	status, err := config.PatchSSHPeerProof(d.configPath, peerID, req)
	if err != nil {
		return nil, sshPeerConfigHandlerError(err)
	}
	return status, nil
}

func (d *Daemon) sshPeerConfigTransitionHandler(peerID string, body []byte) (any, *transport.HandlerError) {
	req, err := config.DecodeSSHPeerTransitionRequest(bytes.NewReader(body))
	if err != nil {
		return nil, sshPeerConfigHandlerError(err)
	}
	status, err := config.TransitionSSHPeer(d.configPath, peerID, req)
	if err != nil {
		return nil, sshPeerConfigHandlerError(err)
	}
	return status, nil
}

func (d *Daemon) sshPeerConfigDisableHandler(peerID string, body []byte) (any, *transport.HandlerError) {
	req, err := config.DecodeSSHPeerDisableRequest(bytes.NewReader(body))
	if err != nil {
		return nil, sshPeerConfigHandlerError(err)
	}
	status, err := config.DisableSSHPeer(d.configPath, peerID, req)
	if err != nil {
		return nil, sshPeerConfigHandlerError(err)
	}
	return status, nil
}

func (d *Daemon) sshPeerConfigDeleteHandler(peerID string, body []byte) (any, *transport.HandlerError) {
	req, err := config.DecodeSSHPeerDeleteRequest(bytes.NewReader(body))
	if err != nil {
		return nil, sshPeerConfigHandlerError(err)
	}
	status, err := config.DeleteSSHPeer(d.configPath, peerID, req)
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
		"missing_ssh_peer_proof_patch_field",
		"missing_ssh_peer_transition_field",
		"missing_ssh_peer_disable_field",
		"missing_ssh_peer_delete_field",
		"malformed_ssh_peer_upsert_request",
		"malformed_ssh_peer_proof_patch_request",
		"malformed_ssh_peer_transition_request",
		"malformed_ssh_peer_disable_request",
		"malformed_ssh_peer_delete_request",
		"invalid_ssh_peer_upsert_field",
		"invalid_ssh_peer_proof_patch_field",
		"invalid_ssh_peer_transition_field",
		"invalid_ssh_peer_disable_field",
		"invalid_ssh_peer_delete_field",
		"ssh_peer_proof_patch_empty",
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
	case hasErrorPrefix(text, "proof_mismatch"):
		return &transport.HandlerError{Status: 409, Code: "proof_mismatch"}
	case hasErrorPrefix(text, "ssh_peer_transition_state_mismatch"):
		return &transport.HandlerError{Status: 409, Code: "ssh_peer_transition_state_mismatch"}
	case hasErrorPrefix(text, "ssh_peer_transition_not_allowed"):
		return &transport.HandlerError{Status: 400, Code: "ssh_peer_transition_not_allowed"}
	case hasErrorPrefix(text,
		"invalid_ssh_peer_transition_state",
		"invalid_ssh_peer_transition_reason",
		"invalid_ssh_peer_transition_failed_phase",
		"ssh_peer_transition_absence_proof_failed_phase_mismatch"):
		return &transport.HandlerError{Status: 400, Code: "invalid_ssh_peer_transition"}
	case hasErrorPrefix(text, "ssh_peer_transition_requires_"):
		return &transport.HandlerError{Status: 400, Code: "invalid_ssh_peer_transition"}
	case hasErrorPrefix(text, "invalid_ssh_peer_disable_state"):
		return &transport.HandlerError{Status: 400, Code: "invalid_ssh_peer_disable"}
	case hasErrorPrefix(text, "invalid_ssh_peer_delete_state"):
		return &transport.HandlerError{Status: 400, Code: "invalid_ssh_peer_delete"}
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
		"invalid_ssh_peer_proof",
		"invalid_ssh_peer_migration_log",
		"invalid_ssh_peer_audit_log",
		"invalid_ssh_peer_remediation",
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
