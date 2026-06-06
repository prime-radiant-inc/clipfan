package sshprovision

import (
	"fmt"
	"strings"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

type SSHServerMode string

const (
	SSHServerModeOpenSSH      SSHServerMode = ""
	SSHServerModeTailscaleSSH SSHServerMode = "tailscale_ssh"
)

func NormalizeSSHServerMode(value string) (SSHServerMode, error) {
	switch SSHServerMode(strings.TrimSpace(value)) {
	case SSHServerModeOpenSSH:
		return SSHServerModeOpenSSH, nil
	case SSHServerModeTailscaleSSH:
		return SSHServerModeTailscaleSSH, nil
	default:
		return "", fmt.Errorf("invalid_ssh_server_mode: %s", value)
	}
}

func ProofVerifiedByForSSHServerMode(mode SSHServerMode) string {
	if mode == SSHServerModeTailscaleSSH {
		return config.ProofVerifiedByTailscaleSSH
	}
	return config.ProofVerifiedByRegularSSH
}
