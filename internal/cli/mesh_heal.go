package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
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

// meshHealFailure is one edge (or restart) mesh-heal could not complete.
type meshHealFailure struct {
	Edge   string `json:"edge"`
	Reason string `json:"reason"`
}

// MeshHealReport is the result of a heal run: edges provisioned, edges already
// healthy and skipped, edges that failed, hosts restarted, and hosts that could
// not be reached. It contains only host ids and edge labels — no secrets.
type MeshHealReport struct {
	Healed      []string          `json:"healed"`
	Skipped     []string          `json:"skipped"`
	Failed      []meshHealFailure `json:"failed"`
	Restarted   []string          `json:"restarted"`
	Unreachable []unreachableHost `json:"unreachable"`
}

// meshHealOptions is the test/production seam. Production leaves the fields nil
// (real runner, config, environment, and pair provisioner driver); tests inject
// fakes.
type meshHealOptions struct {
	Runner            sshprovision.CommandRunner
	ConfigV2WriteGate func() bool
	SharedKey         func() (string, error)
	LoadConfig        func() (*config.Config, error)
	SelfEnv           func() rosterReadEnv
	Driver            sshprovision.DirectPairProvisionDriver
}

// RunMeshHeal is the public entry point for `clipfan mesh-heal`.
func RunMeshHeal(args []string, stdout io.Writer, stderr io.Writer) error {
	return runMeshHeal(args, stdout, stderr, meshHealOptions{})
}

func runMeshHeal(args []string, stdout io.Writer, stderr io.Writer, opts meshHealOptions) error {
	fs := flag.NewFlagSet("mesh-heal", flag.ContinueOnError)
	fs.SetOutput(stderr)
	regularKnownHosts := fs.String("regular-known-hosts", defaultRegularKnownHostsPath(), "known_hosts for regular SSH")
	trustKeyscan := fs.Bool("trust-keyscan", false, "trust ssh-keyscan output for host-key pins")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected mesh-heal argument")
	}
	if !*trustKeyscan {
		return fmt.Errorf("trust_keyscan_required")
	}
	if err := config.ValidateSSHExecutablePath(*regularKnownHosts); err != nil {
		return fmt.Errorf("invalid regular known_hosts path: %w", err)
	}
	if !sshProvisionDirectConfigV2WritesEnabled(opts.ConfigV2WriteGate) {
		return config.ErrConfigV2WritesDisabled
	}

	loadConfig := opts.LoadConfig
	if loadConfig == nil {
		loadConfig = config.Load
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	env, err := meshHealSelfEnv(opts.SelfEnv)
	if err != nil {
		return err
	}
	localReport := buildRosterReadReport(cfg, env)
	localID := localReport.Origin
	if localID == "" {
		return fmt.Errorf("mesh_heal_missing_local_hostname")
	}
	sharedKey, err := sshProvisionDirectSharedKey(opts.SharedKey)
	if err != nil {
		return err
	}
	runner := opts.Runner
	if runner == nil {
		runner = sshprovision.ExecCommandRunner{MaxOutputBytes: 64 * 1024}
	}
	ctx := context.Background()

	// Discover the rest of the mesh (trust then roster-read each host; never SSH
	// to self — the local report comes from config).
	discovery := discoverRoster(ctx, seedEndpointsFromConfig(cfg), localID,
		func(ctx context.Context, ep rosterEndpoint) error {
			return trustEndpoint(ctx, runner, ep, *regularKnownHosts)
		},
		func(ctx context.Context, ep rosterEndpoint) (RosterReadReport, error) {
			return readRosterEndpoint(ctx, runner, ep, *regularKnownHosts)
		},
	)
	discovery.Reports[localID] = localReport

	report := MeshHealReport{
		Healed:      []string{},
		Skipped:     []string{},
		Failed:      []meshHealFailure{},
		Restarted:   []string{},
		Unreachable: discovery.Unreachable,
	}

	// Observe the local host's own address from its peers so local<->remote edges
	// can be provisioned. If unobservable, local edges needing repair surface as
	// failures below rather than aborting the whole heal.
	if selfAddr, selfErr := discoverSelfAddress(ctx, runner, observerEndpoints(discovery.Endpoints, localID), *regularKnownHosts); selfErr == nil {
		if ep, ok := localEndpoint(discovery.Reports, localID, selfAddr, localReport.InstallPath); ok {
			discovery.Endpoints[localID] = ep
		}
	}

	// Resolve + keyscan every reachable host (resilient: per-host failures kept).
	hosts, configPaths := buildProvisionHosts(discovery.Reports, discovery.Endpoints)
	preps, prepErrs := prepHosts(ctx, runner, hosts)

	driver := opts.Driver
	if driver == nil {
		driver = sshprovision.RegularSSHProvisionDriver{
			Runner:                runner,
			RegularKnownHostsPath: *regularKnownHosts,
			ConfirmedHostKeyLines: confirmedLines(preps),
			ProvisionHosts:        provisionHostsFromPreps(preps),
			ConfigPathByHostID:    configPaths,
		}
	}
	provisioner := sshprovision.NewDirectPairProvisionerWithConfigV2WriteGate(driver, opts.ConfigV2WriteGate)

	// Heal each edge: skip the healthy, provision the rest, capture failures.
	changed := map[string]bool{}
	for _, edge := range enumerateMeshEdges(discovery.Reports) {
		label := edge.A + "<->" + edge.B
		if edgeHealthyFromReports(discovery.Reports, edge.A, edge.B) {
			report.Skipped = append(report.Skipped, label)
			continue
		}
		localPrep, okA := preps[edge.A]
		remotePrep, okB := preps[edge.B]
		if !okA || !okB {
			report.Failed = append(report.Failed, meshHealFailure{Edge: label, Reason: prepFailureReason(edge, preps, prepErrs)})
			continue
		}
		if _, err := provisioner.Provision(ctx, sshprovision.DirectPairProvisionInput{
			Local:     localPrep.Host,
			Remote:    remotePrep.Host,
			SharedKey: sharedKey,
		}); err != nil {
			report.Failed = append(report.Failed, meshHealFailure{Edge: label, Reason: err.Error()})
			continue
		}
		report.Healed = append(report.Healed, label)
		changed[edge.A] = true
		changed[edge.B] = true
	}

	// Restart only the hosts whose config we changed.
	for _, id := range sortedBoolKeys(changed) {
		prep, ok := preps[id]
		if !ok {
			continue
		}
		rep := discovery.Reports[id]
		if err := restartDaemon(ctx, runner, prep.Host.AdminHost, *regularKnownHosts, rep.UID, rep.InstallPath); err != nil {
			report.Failed = append(report.Failed, meshHealFailure{Edge: "restart:" + id, Reason: err.Error()})
			continue
		}
		report.Restarted = append(report.Restarted, id)
	}

	return json.NewEncoder(stdout).Encode(report)
}

func meshHealSelfEnv(inject func() rosterReadEnv) (rosterReadEnv, error) {
	if inject != nil {
		return inject(), nil
	}
	self, err := os.Executable()
	if err != nil {
		return rosterReadEnv{}, err
	}
	return rosterReadEnv{
		GOOS:           runtime.GOOS,
		UID:            os.Getuid(),
		SelfBinaryPath: self,
		ConfigPath:     config.Path(),
	}, nil
}

// observerEndpoints returns the reachable remote endpoints (excluding the local
// host) that can observe the local host's address, ordered for determinism.
func observerEndpoints(endpoints map[string]rosterEndpoint, excludeID string) []rosterEndpoint {
	ids := make([]string, 0, len(endpoints))
	for id := range endpoints {
		if id != excludeID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	obs := make([]rosterEndpoint, 0, len(ids))
	for _, id := range ids {
		obs = append(obs, endpoints[id])
	}
	return obs
}

// localEndpoint builds the local host's provisioning endpoint: the observed
// self-address for the host, and the user/port a peer records for the local host
// (what the mesh actually uses to reach it). ok is false if no peer records the
// local host (so its user is unknown and its edges cannot be provisioned).
func localEndpoint(reports map[string]RosterReadReport, localID, selfAddr, installPath string) (rosterEndpoint, bool) {
	for _, id := range sortedReportIDs(reports) {
		if id == localID {
			continue
		}
		for _, p := range reports[id].Peers {
			if p.ID != localID || p.SSHUser == "" {
				continue
			}
			port := p.SSHPort
			if port == 0 {
				port = 22
			}
			return rosterEndpoint{ID: localID, SSHUser: p.SSHUser, SSHHost: selfAddr, SSHPort: port, InstallPath: installPath}, true
		}
	}
	return rosterEndpoint{}, false
}

func confirmedLines(preps map[string]hostPrep) map[string]string {
	out := make(map[string]string, len(preps))
	for id, p := range preps {
		out[id] = p.HostKeyLine
	}
	return out
}

func provisionHostsFromPreps(preps map[string]hostPrep) map[string]sshprovision.DirectPairProvisionHost {
	out := make(map[string]sshprovision.DirectPairProvisionHost, len(preps))
	for id, p := range preps {
		out[id] = p.Host
	}
	return out
}

func prepFailureReason(edge meshEdge, preps map[string]hostPrep, prepErrs map[string]error) string {
	var reasons []string
	for _, id := range []string{edge.A, edge.B} {
		if _, ok := preps[id]; ok {
			continue
		}
		if err := prepErrs[id]; err != nil {
			reasons = append(reasons, id+": "+err.Error())
		} else {
			reasons = append(reasons, id+": no reachable endpoint")
		}
	}
	return joinReasons(reasons)
}

func joinReasons(reasons []string) string {
	out := ""
	for i, r := range reasons {
		if i > 0 {
			out += "; "
		}
		out += r
	}
	return out
}

func sortedBoolKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
