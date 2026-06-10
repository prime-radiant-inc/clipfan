package cli

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

func TestHostAddressCandidatesOrdersPrimaryFirstAndDedups(t *testing.T) {
	got := hostAddressCandidates("10.0.0.1", RosterReadReport{
		LocalAddresses: []string{"10.0.0.1", "192.168.1.5", "192.168.1.5", ""},
	})
	want := []string{"10.0.0.1", "192.168.1.5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func TestHostAddressCandidatesDropsOversizedLANCandidateList(t *testing.T) {
	var local []string
	for i := 0; i < maxMeshLANAddressCandidates+1; i++ {
		local = append(local, "10.0."+strconv.Itoa(i)+".1")
	}
	got := hostAddressCandidates("100.64.0.5", RosterReadReport{LocalAddresses: local})
	want := []string{"100.64.0.5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

// TestRelabelHostKeyLine guards the load-bearing fix: a host-key line scanned at the
// primary address must be re-labeled to the chosen connect address, because
// ParseKnownHostScanLine (which Provision uses) rejects a line whose host token
// doesn't match. The same host-wide key under the new label is accepted.
func TestRelabelHostKeyLine(t *testing.T) {
	key := testDirectProvisionPublicKey("acceptor")
	primaryLine := "100.64.0.1 ssh-ed25519 " + key

	relabeled, err := relabelHostKeyLine("100.64.0.1", 22, primaryLine, "192.168.1.5")
	if err != nil {
		t.Fatalf("relabelHostKeyLine() error = %v", err)
	}
	if !strings.HasPrefix(relabeled, "192.168.1.5 ") || !strings.Contains(relabeled, key) {
		t.Fatalf("relabeled = %q (want host 192.168.1.5 with the same key)", relabeled)
	}
	if _, err := sshprovision.ParseKnownHostScanLine("192.168.1.5", 22, relabeled); err != nil {
		t.Fatalf("re-labeled line rejected at the new address: %v", err)
	}
	// The un-relabeled primary line MUST be rejected at the new address — this is the
	// failure mode the re-label exists to prevent.
	if _, err := sshprovision.ParseKnownHostScanLine("192.168.1.5", 22, primaryLine); err == nil {
		t.Fatal("expected the primary-labeled line to be rejected at the new address")
	}
}

func TestChooseConnectAddressKeepsReachablePrimary(t *testing.T) {
	key := testDirectProvisionPublicKey("acceptor")
	expected, err := sshprovision.NewKnownHostPin("100.64.0.5", 22, "ssh-ed25519", key)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeMeshRunner{keyscanFromPeer: map[string]map[string]string{
		"peerhost": {
			"100.64.0.5":  "100.64.0.5 ssh-ed25519 " + key,
			"192.168.1.5": "192.168.1.5 ssh-ed25519 " + key,
		},
	}}
	peer := rosterEndpoint{ID: "P", SSHUser: "jesse", SSHHost: "peerhost", SSHPort: 22}
	got := chooseConnectAddress(context.Background(), runner,
		[]string{"100.64.0.5", "192.168.1.5"}, []rosterEndpoint{peer}, 22, expected, "/kh")
	if got != "100.64.0.5" {
		t.Fatalf("chose %q, want the primary (kept when it verifies)", got)
	}
}

// TestChooseConnectAddressPrefersVerifiedLANAndRejectsCollision is the core /par
// scenario: the primary is unreachable, a shared docker/bridge IP answers with the
// WRONG host key (a collision), and only the real LAN address presents the
// acceptor's key. Identity verification must skip the collision and pick the LAN.
func TestChooseConnectAddressPrefersVerifiedLANAndRejectsCollision(t *testing.T) {
	key := testDirectProvisionPublicKey("acceptor")
	collision := testDirectProvisionPublicKey("mac-a.key") // a genuinely different host key
	if collision == key {
		t.Fatal("test fixture bug: collision key must differ from the acceptor key")
	}
	expected, err := sshprovision.NewKnownHostPin("100.64.0.5", 22, "ssh-ed25519", key)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeMeshRunner{keyscanFromPeer: map[string]map[string]string{
		"peerhost": {
			"100.64.0.5":  "",                                    // Tailscale primary: unreachable cross-tailnet
			"172.17.0.1":  "172.17.0.1 ssh-ed25519 " + collision, // docker bridge: wrong host -> reject
			"192.168.1.5": "192.168.1.5 ssh-ed25519 " + key,      // real LAN: acceptor's key -> select
		},
	}}
	peer := rosterEndpoint{ID: "P", SSHUser: "jesse", SSHHost: "peerhost", SSHPort: 22}
	got := chooseConnectAddress(context.Background(), runner,
		[]string{"100.64.0.5", "172.17.0.1", "192.168.1.5"}, []rosterEndpoint{peer}, 22, expected, "/kh")
	if got != "192.168.1.5" {
		t.Fatalf("chose %q, want 192.168.1.5 (identity-verified LAN, collision rejected)", got)
	}
}

func TestChooseConnectAddressFallsBackToPrimaryWhenNothingVerifies(t *testing.T) {
	expected, err := sshprovision.NewKnownHostPin("100.64.0.5", 22, "ssh-ed25519", testDirectProvisionPublicKey("acceptor"))
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeMeshRunner{keyscanFromPeer: map[string]map[string]string{"peerhost": {}}} // all unreachable
	peer := rosterEndpoint{ID: "P", SSHUser: "jesse", SSHHost: "peerhost", SSHPort: 22}
	got := chooseConnectAddress(context.Background(), runner,
		[]string{"100.64.0.5", "192.168.1.5"}, []rosterEndpoint{peer}, 22, expected, "/kh")
	if got != "100.64.0.5" {
		t.Fatalf("chose %q, want primary fallback when nothing verifies", got)
	}
}

// TestRunMeshHealFallsBackToLANForCrossTailnetEdge is the end-to-end proof: a
// local<->remote edge where each end's Tailscale address is unreachable from the
// other, but both share a LAN. mesh-heal must provision the edge using each host's
// LAN address (verified by host key), including the LOCAL host's LAN address.
func TestRunMeshHealFallsBackToLANForCrossTailnetEdge(t *testing.T) {
	const (
		rPrimary = "100.64.0.5" // remote Tailscale (unreachable from local)
		rLAN     = "192.168.1.5"
		lPrimary = "100.64.0.9" // local Tailscale (unreachable from remote)
		lLAN     = "192.168.1.9"
	)
	cfg := &config.Config{
		Hostname:  "L",
		SharedKey: testDirectProvisionSharedKey,
		SSH: &config.SSHConfig{
			KnownHosts: "/l/kh", SyncKey: "/l/sk",
			Peers: []config.SSHPeer{{
				ID: "R", SSHUser: "jesse", SSHHost: rPrimary, SSHPort: 22, InstallPath: "/r/clipfan",
				Enabled: true, Accept: true, Connect: true,
				MigrationState: "ssh_material_staged", // not ready -> L<->R unhealthy
				Proof:          config.SSHProof{AcceptKeyID: "ak-R", ConnectKeyID: "ck-R"},
			}},
		},
	}
	rReport, _ := json.Marshal(RosterReadReport{
		Origin: "R", Platform: "linux", UID: 1000,
		ConfigPath: "/r/config.json", KnownHostsPath: "/r/kh", SyncKeyPath: "/r/sk",
		InstallPath: "/r/clipfan", GatewayPath: "/r/clipfan",
		LocalAddresses: []string{rLAN},
		Peers:          []RosterReadPeer{healthyMeshPeer("L", "jesse", lPrimary, "/l/clipfan")},
	})
	runner := &fakeMeshRunner{
		reports:  map[string]string{rPrimary: string(rReport)},
		selfAddr: lPrimary,
		keyscanFromPeer: map[string]map[string]string{
			// L probes R's candidates: Tailscale unreachable, LAN presents R's key.
			lPrimary: {
				rPrimary: "",
				rLAN:     rLAN + " ssh-ed25519 " + testDirectProvisionPublicKey(rPrimary),
			},
			// R probes L's candidates: Tailscale unreachable, LAN presents L's key.
			rPrimary: {
				lPrimary: "",
				lLAN:     lLAN + " ssh-ed25519 " + testDirectProvisionPublicKey(lPrimary),
			},
		},
	}
	env := rosterReadEnv{GOOS: "darwin", UID: 501, SelfBinaryPath: "/l/clipfan", ConfigPath: "/l/config.json", LocalAddresses: []string{lLAN}}

	args, opts, _ := meshHealTestOptions(t, runner, cfg, env)
	driver := &fakeMeshDriver{}
	opts.Driver = driver
	var out, errBuf strings.Builder
	if err := runMeshHeal(args, &out, &errBuf, opts); err != nil {
		t.Fatalf("runMeshHeal: %v (stderr=%s)", err, errBuf.String())
	}

	var report MeshHealReport
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatalf("decode: %v (%s)", err, out.String())
	}
	if len(report.Healed) != 1 || report.Healed[0] != "L<->R" {
		t.Fatalf("healed = %+v (failed=%+v)", report.Healed, report.Failed)
	}
	// Provision must have used each host's LAN address — including the LOCAL host's.
	confirmed := strings.Join(driver.confirmedHosts, ",")
	if !strings.Contains(confirmed, "R@"+rLAN) {
		t.Fatalf("remote not provisioned at its LAN address: %v", driver.confirmedHosts)
	}
	if !strings.Contains(confirmed, "L@"+lLAN) {
		t.Fatalf("local not provisioned at its LAN address: %v", driver.confirmedHosts)
	}
}
