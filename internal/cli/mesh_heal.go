package cli

import (
	"sort"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

// meshEdge is an undirected mesh link between two host ids (A < B).
type meshEdge struct {
	A string
	B string
}

// reportPeerView projects one host's roster-read view of a given peer into an
// edgePeerView for change-detection. A host with no entry for the peer yields a
// not-found view, which edgeIsHealthy treats as unhealthy.
func reportPeerView(report RosterReadReport, peerID string) edgePeerView {
	for _, p := range report.Peers {
		if p.ID == peerID {
			return edgePeerView{
				Found:          true,
				Enabled:        p.Enabled,
				Accept:         p.Accept,
				Connect:        p.Connect,
				MigrationState: p.MigrationState,
				AcceptKeyID:    p.AcceptKeyID,
				ConnectKeyID:   p.ConnectKeyID,
			}
		}
	}
	return edgePeerView{}
}

// edgeHealthyFromReports decides whether the edge between a and b needs no repair
// using each end's own roster-read report (a's view of b and b's view of a) —
// no extra SSH, since discovery already collected both reports.
func edgeHealthyFromReports(reports map[string]RosterReadReport, a, b string) bool {
	return edgeIsHealthy(reportPeerView(reports[a], b), reportPeerView(reports[b], a))
}

// enumerateMeshEdges lists every undirected pair of discovered hosts, ordered by
// id so a heal run is deterministic.
func enumerateMeshEdges(reports map[string]RosterReadReport) []meshEdge {
	ids := sortedReportIDs(reports)
	var edges []meshEdge
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			edges = append(edges, meshEdge{A: ids[i], B: ids[j]})
		}
	}
	return edges
}

func sortedReportIDs(reports map[string]RosterReadReport) []string {
	ids := make([]string, 0, len(reports))
	for id := range reports {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// seedEndpointsFromConfig turns the local config's SSH peers into the discovery
// seed — the remote hosts mesh-heal starts the roster walk from.
func seedEndpointsFromConfig(cfg *config.Config) []rosterEndpoint {
	if cfg == nil || cfg.SSH == nil {
		return nil
	}
	eps := make([]rosterEndpoint, 0, len(cfg.SSH.Peers))
	for _, p := range cfg.SSH.Peers {
		eps = append(eps, rosterEndpoint{
			ID:          p.ID,
			SSHUser:     p.SSHUser,
			SSHHost:     p.SSHHost,
			SSHPort:     p.SSHPort,
			InstallPath: p.InstallPath,
		})
	}
	return eps
}

// buildProvisionHosts constructs the per-host provisioning inputs from each
// host's roster-read report (paths, install/gateway) and the endpoint used to
// reach it (ssh locator). A host with no endpoint (e.g. the local host when its
// self-address could not be observed) is skipped — its edges cannot be
// provisioned this run. Returns the hosts and the per-id config paths the driver
// needs.
func buildProvisionHosts(reports map[string]RosterReadReport, endpoints map[string]rosterEndpoint) ([]sshprovision.DirectPairProvisionHost, map[string]string) {
	ids := sortedReportIDs(reports)
	hosts := make([]sshprovision.DirectPairProvisionHost, 0, len(ids))
	configPaths := make(map[string]string, len(ids))
	for _, id := range ids {
		ep, ok := endpoints[id]
		if !ok {
			continue
		}
		report := reports[id]
		hosts = append(hosts, sshprovision.DirectPairProvisionHost{
			Host: sshprovision.DirectPairHost{
				ID:          id,
				SSHHost:     ep.SSHHost,
				SSHUser:     ep.SSHUser,
				SSHPort:     ep.SSHPort,
				InstallPath: report.InstallPath,
				GatewayPath: report.GatewayPath,
			},
			KnownHostsPath: report.KnownHostsPath,
			SyncKeyPath:    report.SyncKeyPath,
		})
		configPaths[id] = report.ConfigPath
	}
	return hosts, configPaths
}
