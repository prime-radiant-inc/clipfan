package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

// meshKeyscanTimeoutSeconds bounds each candidate probe so an unreachable address
// (e.g. a Tailscale CGNAT address from a different tailnet) fails fast.
const meshKeyscanTimeoutSeconds = 5

// hostAddressCandidates orders a host's addresses for cross-tailnet fallback: the
// resolved primary address first — so a working Tailscale address is preferred —
// then the host's reported LAN candidates. Deduped; the primary is never repeated.
func hostAddressCandidates(primary string, report RosterReadReport) []string {
	out := []string{primary}
	seen := map[string]bool{primary: true}
	for _, addr := range report.LocalAddresses {
		if addr == "" || seen[addr] {
			continue
		}
		seen[addr] = true
		out = append(out, addr)
	}
	return out
}

// keyscanHostKeyFromPeer runs ssh-keyscan ON peer against candidate:port and returns
// the host keys peer observed there. Running it on the peer (not the orchestrator)
// tests reachability from the vantage that matters — the host that will dial this
// address in the provisioned edge.
func keyscanHostKeyFromPeer(ctx context.Context, runner sshprovision.CommandRunner, peer rosterEndpoint, candidate string, port int, regularKnownHostsPath string) ([]sshprovision.KnownHostPin, error) {
	if err := validateMeshSSHHost(candidate); err != nil {
		return nil, err
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid candidate port %d", port)
	}
	script := fmt.Sprintf("ssh-keyscan -T %d -p %d %s", meshKeyscanTimeoutSeconds, port, candidate)
	command, err := sshprovision.RegularSSHShellCommand(sshprovision.RegularSSHShellSpec{
		User:           peer.SSHUser,
		Host:           peer.SSHHost,
		Port:           peer.SSHPort,
		KnownHostsPath: regularKnownHostsPath,
		Script:         script,
	})
	if err != nil {
		return nil, err
	}
	output, err := runner.Run(ctx, command)
	if err != nil {
		return nil, err
	}
	if output.StdoutTruncated {
		return nil, fmt.Errorf("mesh_keyscan_output_truncated")
	}
	var pins []sshprovision.KnownHostPin
	for _, raw := range strings.Split(string(output.Stdout), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pin, err := sshprovision.ParseKnownHostScanLine(candidate, port, line)
		if err != nil {
			continue
		}
		pins = append(pins, pin)
	}
	return pins, nil
}

// candidateVerifiedFromPeer is true when peer can reach candidate:port AND the host
// there presents expected's host key — so a shared/duplicated address (e.g. a docker
// bridge IP that resolves to the peer's own sshd) is rejected, not falsely selected.
func candidateVerifiedFromPeer(ctx context.Context, runner sshprovision.CommandRunner, peer rosterEndpoint, candidate string, port int, expected sshprovision.KnownHostPin, regularKnownHostsPath string) bool {
	pins, err := keyscanHostKeyFromPeer(ctx, runner, peer, candidate, port, regularKnownHostsPath)
	if err != nil {
		return false
	}
	for _, pin := range pins {
		if pin.KeyType == expected.KeyType && pin.PublicKey == expected.PublicKey {
			return true
		}
	}
	return false
}

// chooseConnectAddress picks a host's connect address: the candidate verified
// reachable from the most of its unhealthy-edge peers, preferring the primary on ties
// (so a working Tailscale address is kept). Returns the primary when nothing verifies.
// candidates[0] is the resolved primary; port and expected are the host's resolved SSH
// port and its own host-key pin.
func chooseConnectAddress(ctx context.Context, runner sshprovision.CommandRunner, candidates []string, peers []rosterEndpoint, port int, expected sshprovision.KnownHostPin, regularKnownHostsPath string) string {
	primary := ""
	if len(candidates) > 0 {
		primary = candidates[0]
	}
	if len(peers) == 0 {
		return primary
	}
	best := primary
	bestCount := -1
	for _, candidate := range candidates {
		count := 0
		for _, peer := range peers {
			if candidateVerifiedFromPeer(ctx, runner, peer, candidate, port, expected, regularKnownHostsPath) {
				count++
			}
		}
		if count > bestCount {
			bestCount = count
			best = candidate
		}
		if bestCount == len(peers) {
			break // full coverage; no candidate can do better
		}
	}
	if bestCount <= 0 {
		return primary
	}
	return best
}

// relabelHostKeyLine re-labels a host-key line scanned at resolvedHost:port to addr,
// so the provisioner's ConfirmHostKey accepts it for the chosen connect address. The
// key is host-wide, so the same key under a new host token is correct.
func relabelHostKeyLine(resolvedHost string, port int, primaryLine, addr string) (string, error) {
	pin, err := sshprovision.ParseKnownHostScanLine(resolvedHost, port, primaryLine)
	if err != nil {
		return "", err
	}
	relabeled, err := sshprovision.NewKnownHostPin(addr, port, pin.KeyType, pin.PublicKey)
	if err != nil {
		return "", err
	}
	return relabeled.Line(), nil
}

// selectMeshConnectAddresses switches a host's connect address to a LAN fallback when
// its primary address is unreachable from the peers it must mesh with, mutating preps
// in place (Host.SSHHost + the re-labeled host-key line). AdminHost is left on the
// primary, so orchestrator->host admin SSH is unchanged; only the peer-to-peer dial
// address moves. Hosts with no unhealthy edges are untouched.
func selectMeshConnectAddresses(ctx context.Context, runner sshprovision.CommandRunner, preps map[string]hostPrep, reports map[string]RosterReadReport, unhealthyPeers map[string][]rosterEndpoint, regularKnownHostsPath string) {
	ids := make([]string, 0, len(unhealthyPeers))
	for id := range unhealthyPeers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		peers := unhealthyPeers[id]
		prep, ok := preps[id]
		if len(peers) == 0 || !ok {
			continue
		}
		primary := prep.Host.Host.SSHHost
		port := prep.Host.Host.SSHPort
		expected, err := sshprovision.ParseKnownHostScanLine(primary, port, prep.HostKeyLine)
		if err != nil {
			continue // can't verify identity without the host's own key; keep primary
		}
		candidates := hostAddressCandidates(primary, reports[id])
		chosen := chooseConnectAddress(ctx, runner, candidates, peers, port, expected, regularKnownHostsPath)
		if chosen == primary {
			continue // primary kept
		}
		relabeled, err := relabelHostKeyLine(primary, port, prep.HostKeyLine, chosen)
		if err != nil {
			continue // keep primary if the host key can't be re-labeled
		}
		prep.Host.Host.SSHHost = chosen
		prep.HostKeyLine = relabeled
		preps[id] = prep
	}
}
