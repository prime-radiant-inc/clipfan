package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

// rosterEndpoint is everything mesh-heal needs to reach one host over the user's
// regular SSH and run roster-read on it. Shared by host-key trust and roster
// discovery.
type rosterEndpoint struct {
	ID          string
	SSHUser     string
	SSHHost     string
	SSHPort     int
	InstallPath string
}

// knownHostLine is a parsed known_hosts entry reduced to the fields that decide
// a host-key trust conflict: an optional @marker, the key type, and the key
// blob. The host token is dropped on purpose — ssh-keygen -F has already
// filtered these lines to the host we asked about, hashed (|1|...) entries
// included.
type knownHostLine struct {
	Marker  string
	KeyType string
	Key     string
}

// scannedHostKey is one ssh-keyscan host key, carrying both the comparison
// fields and the validated, ready-to-write known_hosts line.
type scannedHostKey struct {
	KeyType string
	Key     string
	Line    string
}

// parseKnownHostLine parses one known_hosts line. ok is false for blank lines,
// comments, and lines too short to carry a key. Ports the Swift knownHostEntry.
func parseKnownHostLine(line string) (knownHostLine, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return knownHostLine{}, false
	}
	fields := strings.Fields(trimmed)
	hostIndex := 0
	marker := ""
	if strings.HasPrefix(fields[0], "@") {
		marker = fields[0]
		hostIndex = 1
	}
	if len(fields) < hostIndex+3 {
		return knownHostLine{}, false
	}
	return knownHostLine{
		Marker:  marker,
		KeyType: fields[hostIndex+1],
		Key:     fields[hostIndex+2],
	}, true
}

// reconcileTrustedHostKeys decides which scanned host-key lines must be appended
// to the regular known_hosts to trust a host, given the entries ssh-keygen -F
// already returned for it. It is a faithful port of the Swift conflict loop:
//   - a scanned key type already present with the SAME key  -> already trusted (skip)
//   - a scanned key type present with a DIFFERENT key       -> conflict (error)
//   - an existing entry for that key type carrying a marker -> conflict (error)
//   - a key type not present at all                         -> append it
//
// Divergent keys for one key type within a single scan are also a conflict.
func reconcileTrustedHostKeys(existing []knownHostLine, scanned []scannedHostKey) ([]string, error) {
	var appendLines []string
	scannedByType := make(map[string]string, len(scanned))
	for _, s := range scanned {
		if prev, ok := scannedByType[s.KeyType]; ok {
			if prev != s.Key {
				return nil, fmt.Errorf("ssh_known_hosts_conflict: keyscan returned divergent %s keys", s.KeyType)
			}
			continue
		}
		scannedByType[s.KeyType] = s.Key

		trusted := false
		for _, e := range existing {
			if e.KeyType != s.KeyType {
				continue
			}
			if e.Marker != "" {
				return nil, fmt.Errorf("ssh_known_hosts_conflict: marked %s entry %q", s.KeyType, e.Marker)
			}
			if e.Key == s.Key {
				trusted = true
				break
			}
			return nil, fmt.Errorf("ssh_known_hosts_conflict: existing %s key differs", s.KeyType)
		}
		if !trusted {
			appendLines = append(appendLines, s.Line)
		}
	}
	return appendLines, nil
}

// sshExitCoder matches both *exec.ExitError (the real runner) and test fakes, so
// trust can treat ssh-keygen -F's exit status 1 ("no matching host found") as an
// empty result rather than a command failure.
type sshExitCoder interface{ ExitCode() int }

// lookupExistingRegularHostKeys runs ssh-keygen -F to find the regular
// known_hosts entries for token, resolving hashed (|1|...) entries the way
// OpenSSH does. Exit status 1 means no match. The caller must ensure the file
// exists (ssh-keygen errors on a missing file).
func lookupExistingRegularHostKeys(ctx context.Context, runner sshprovision.CommandRunner, token, knownHostsPath string) ([]knownHostLine, error) {
	output, err := runner.Run(ctx, sshprovision.SSHCommand{
		Args: []string{"ssh-keygen", "-F", token, "-f", knownHostsPath},
	})
	if err != nil {
		var coder sshExitCoder
		if errors.As(err, &coder) && coder.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	// Fail closed: a truncated lookup could hide a conflicting existing key and
	// let reconcile append a divergent one. Sibling keyscan paths reject the same way.
	if output.StdoutTruncated {
		return nil, fmt.Errorf("ssh_keygen_lookup_output_truncated: %s", token)
	}
	var entries []knownHostLine
	for _, raw := range strings.Split(string(output.Stdout), "\n") {
		if entry, ok := parseKnownHostLine(raw); ok {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// trustScannedHostKeys upserts the host keys from ssh-keyscan output into the
// orchestrator's regular known_hosts so StrictHostKeyChecking=yes admin SSH to
// the host succeeds. It is hashed-aware (via ssh-keygen -F), refuses to
// overwrite a conflicting pin, and forces 0600 on write (user files are often
// 0644).
//
// host/port are the keyscan target resolved by ssh -G. The key is pinned under
// that resolved token, which is exactly the name OpenSSH uses for the
// known_hosts lookup on the subsequent admin SSH: that SSH reads ~/.ssh/config
// (it does NOT pass -F /dev/null) and so applies the same HostName resolution
// ssh -G reported. For mesh-heal's raw-IP endpoints the resolved and original
// hosts are identical anyway.
//
// The existence check (ssh-keygen -F) and the append are run together under the
// known_hosts lock so a concurrent trust of the same host cannot slip a
// divergent key past the conflict check.
func trustScannedHostKeys(ctx context.Context, runner sshprovision.CommandRunner, host string, port int, keyscanOutput, regularKnownHostsPath string) error {
	token, err := sshprovision.KnownHostsPattern(host, port)
	if err != nil {
		return err
	}
	var scanned []scannedHostKey
	for _, raw := range strings.Split(keyscanOutput, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pin, err := sshprovision.ParseKnownHostScanLine(host, port, line)
		if err != nil {
			continue
		}
		scanned = append(scanned, scannedHostKey{
			KeyType: pin.KeyType,
			Key:     pin.PublicKey,
			Line:    pin.Line(),
		})
	}
	if len(scanned) == 0 {
		return fmt.Errorf("ssh_keyscan_no_matching_host_key: %s", token)
	}

	return sshprovision.WithKnownHostsLock(regularKnownHostsPath, func() error {
		var existing []knownHostLine
		if _, statErr := os.Stat(regularKnownHostsPath); statErr == nil {
			entries, err := lookupExistingRegularHostKeys(ctx, runner, token, regularKnownHostsPath)
			if err != nil {
				return err
			}
			existing = entries
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		appendLines, err := reconcileTrustedHostKeys(existing, scanned)
		if err != nil {
			return err
		}
		return sshprovision.AppendKnownHostsLinesLocked(regularKnownHostsPath, appendLines)
	})
}

// trustEndpoint resolves a host's keyscan target (ssh -G), ssh-keyscans it, and
// trusts the result into the regular known_hosts. mesh-heal calls this before
// running roster-read over the user's regular, strict-host-key-checked SSH.
func trustEndpoint(ctx context.Context, runner sshprovision.CommandRunner, ep rosterEndpoint, regularKnownHostsPath string) error {
	target, err := resolveSSHProvisionDirectKeyscanTarget(ctx, runner, sshprovision.DirectPairHost{
		ID:      ep.ID,
		SSHUser: ep.SSHUser,
		SSHHost: ep.SSHHost,
		SSHPort: ep.SSHPort,
	})
	if err != nil {
		return err
	}
	scanCmd, err := sshprovision.SSHKeyscanCommand(sshprovision.SSHKeyscanSpec{
		Host:           target.Host,
		Port:           target.Port,
		TimeoutSeconds: 5,
	})
	if err != nil {
		return err
	}
	output, err := runner.Run(ctx, scanCmd)
	if err != nil {
		return err
	}
	if output.StdoutTruncated || output.StderrTruncated {
		return fmt.Errorf("ssh_keyscan_output_truncated: %s", ep.ID)
	}
	return trustScannedHostKeys(ctx, runner, target.Host, target.Port, string(output.Stdout), regularKnownHostsPath)
}
