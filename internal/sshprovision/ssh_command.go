package sshprovision

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

var ErrInvalidPinnedSSHCommand = errors.New("invalid_pinned_ssh_command")

type PinnedSSHCommand struct {
	User           string
	Host           string
	Port           int
	PrivateKeyPath string
	KnownHostsPath string
}

type SSHCommand struct {
	Args []string
}

func PinnedSSHProbeCommand(spec PinnedSSHCommand) (SSHCommand, error) {
	normalized, err := normalizePinnedSSHCommand(spec)
	if err != nil {
		return SSHCommand{}, err
	}
	return SSHCommand{Args: []string{
		"ssh",
		"-F", "/dev/null",
		"-o", "BatchMode=yes",
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=" + normalized.KnownHostsPath,
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "ProxyCommand=none",
		"-o", "ProxyJump=none",
		"-o", "PermitLocalCommand=no",
		"-o", "RequestTTY=no",
		"-o", "ClearAllForwardings=yes",
		"-o", "LogLevel=ERROR",
		"-i", normalized.PrivateKeyPath,
		"-p", strconv.Itoa(normalized.Port),
		normalized.User + "@" + normalized.Host,
		SSHGatewayProbeCommand,
	}}, nil
}

func normalizePinnedSSHCommand(spec PinnedSSHCommand) (PinnedSSHCommand, error) {
	if err := config.ValidateSSHUser(spec.User); err != nil {
		return PinnedSSHCommand{}, fmt.Errorf("%w: invalid user: %v", ErrInvalidPinnedSSHCommand, err)
	}
	host, err := config.CanonicalSSHHost(spec.Host)
	if err != nil {
		return PinnedSSHCommand{}, fmt.Errorf("%w: invalid host: %v", ErrInvalidPinnedSSHCommand, err)
	}
	if spec.Port < 1 || spec.Port > 65535 {
		return PinnedSSHCommand{}, fmt.Errorf("%w: invalid port %d", ErrInvalidPinnedSSHCommand, spec.Port)
	}
	if err := config.ValidateSSHExecutablePath(spec.PrivateKeyPath); err != nil {
		return PinnedSSHCommand{}, fmt.Errorf("%w: invalid private key path: %v", ErrInvalidPinnedSSHCommand, err)
	}
	if err := config.ValidateSSHExecutablePath(spec.KnownHostsPath); err != nil {
		return PinnedSSHCommand{}, fmt.Errorf("%w: invalid known hosts path: %v", ErrInvalidPinnedSSHCommand, err)
	}
	spec.Host = host
	return spec, nil
}
