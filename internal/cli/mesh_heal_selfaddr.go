package cli

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

// observedSelfAddressSnippet is the zsh/sh-safe one-liner an observer peer runs
// to report the address it saw the local host connect from (SSH_CONNECTION's
// client field, SSH_CLIENT as a fallback). Pinned verbatim to the Swift
// installer so the two provisioners observe identically.
const observedSelfAddressSnippet = `v=${SSH_CONNECTION:-$SSH_CLIENT}; v=${v%% *}; test -n "$v" || exit 44; printf '%s\n' "$v"`

// tailscaleCGNAT is Tailscale's 100.64.0.0/10 address range; the local host's
// Tailscale address is preferred for self-addressing (it matches how mesh peers
// reach each other).
var tailscaleCGNAT = mustCIDR("100.64.0.0/10")

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// observeSelfAddress runs the snippet on one observer peer over the user's
// regular SSH and returns the validated address that peer saw the local host
// connect from. A non-singular or invalid observation is rejected.
func observeSelfAddress(ctx context.Context, runner sshprovision.CommandRunner, observer rosterEndpoint, regularKnownHostsPath string) (string, error) {
	command, err := sshprovision.RegularSSHShellCommand(sshprovision.RegularSSHShellSpec{
		User:           observer.SSHUser,
		Host:           observer.SSHHost,
		Port:           observer.SSHPort,
		KnownHostsPath: regularKnownHostsPath,
		Script:         observedSelfAddressSnippet,
	})
	if err != nil {
		return "", err
	}
	output, err := runner.Run(ctx, command)
	if err != nil {
		return "", err
	}
	if output.StdoutTruncated {
		return "", fmt.Errorf("observed_self_address_truncated")
	}
	var lines []string
	for _, l := range strings.Split(string(output.Stdout), "\n") {
		if trimmed := strings.TrimSpace(l); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	if len(lines) != 1 {
		return "", fmt.Errorf("invalid_remote_observed_callback_host")
	}
	if err := validateMeshSSHHost(lines[0]); err != nil {
		return "", fmt.Errorf("invalid_remote_observed_callback_host: %w", err)
	}
	return lines[0], nil
}

// discoverSelfAddress observes the local host's address from its peers, trying
// observers in turn so one dead peer doesn't abort self-addressing. A Tailscale
// (100.x) observation is preferred; otherwise the first valid one is used.
func discoverSelfAddress(ctx context.Context, runner sshprovision.CommandRunner, observers []rosterEndpoint, regularKnownHostsPath string) (string, error) {
	first := ""
	var lastErr error
	for _, observer := range observers {
		addr, err := observeSelfAddress(ctx, runner, observer, regularKnownHostsPath)
		if err != nil {
			lastErr = err
			continue
		}
		if ip := net.ParseIP(addr); ip != nil && tailscaleCGNAT.Contains(ip) {
			return addr, nil
		}
		if first == "" {
			first = addr
		}
	}
	if first != "" {
		return first, nil
	}
	if lastErr != nil {
		return "", fmt.Errorf("self_address_unobservable: %w", lastErr)
	}
	return "", fmt.Errorf("self_address_unobservable: no observers")
}

// meshSSHHostInvalidChars are the characters the self-address must not contain,
// ported from the Swift validatePrivateDirectMeshSSHHost: whitespace plus shell
// metacharacters. A bare IP or hostname is fine; an address with a colon must be
// a raw IPv6 literal.
const meshSSHHostInvalidChars = " \t\r\n@/\\\"'`$;&|<>(){}[]*?!%"

func validateMeshSSHHost(value string) error {
	if value == "" || strings.HasPrefix(value, "-") {
		return fmt.Errorf("invalid_mesh_ssh_host")
	}
	if strings.ContainsAny(value, meshSSHHostInvalidChars) {
		return fmt.Errorf("invalid_mesh_ssh_host")
	}
	if strings.Contains(value, ":") {
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() != nil {
			return fmt.Errorf("invalid_mesh_ssh_host: colon but not an IPv6 literal")
		}
	}
	return nil
}
