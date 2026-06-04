package sshprovision

import (
	"testing"
)

func TestDirectPairPlanChoosesDeterministicConnector(t *testing.T) {
	t.Parallel()

	plan, err := BuildDirectPairPlan(DirectPairPlanInput{
		Local:  planHost("mac-b", "mac-b.tailnet", "jesse", 22),
		Remote: planHost("linux-a", "linux-a.tailnet", "jesse", 22),
	})
	if err != nil {
		t.Fatalf("BuildDirectPairPlan() error = %v", err)
	}
	if plan.ConnectHostID != "linux-a" || plan.AcceptHostID != "mac-b" {
		t.Fatalf("connect/accept = %s/%s, want linux-a/mac-b", plan.ConnectHostID, plan.AcceptHostID)
	}
	if plan.PairID != "linux-a--mac-b" {
		t.Fatalf("PairID = %q", plan.PairID)
	}
}

func TestDirectPairPlanOrdersFailClosedStepsBeforeConfigWrites(t *testing.T) {
	t.Parallel()

	plan, err := BuildDirectPairPlan(DirectPairPlanInput{
		Local:  planHost("mac-a", "mac-a.tailnet", "jesse", 22),
		Remote: planHost("linux-b", "linux-b.tailnet", "jesse", 2200),
	})
	if err != nil {
		t.Fatalf("BuildDirectPairPlan() error = %v", err)
	}

	wantSteps := []DirectPairStepKind{
		StepConfirmHostKey,
		StepUpsertKnownHostPin,
		StepEnsureConnectorSyncKey,
		StepInstallAcceptorAuthorizedKey,
		StepProbeForcedCommand,
		StepWriteConnectorPeerConfig,
		StepWriteAcceptorPeerConfig,
		StepPatchDirectionalProofs,
		StepTransitionSSHMaterialStaged,
		StepTransitionSSHKeysReady,
	}
	if len(plan.Steps) != len(wantSteps) {
		t.Fatalf("steps length = %d, want %d: %#v", len(plan.Steps), len(wantSteps), plan.Steps)
	}
	for i, want := range wantSteps {
		if plan.Steps[i].Kind != want {
			t.Fatalf("step[%d] kind = %q, want %q", i, plan.Steps[i].Kind, want)
		}
	}
	for i, step := range plan.Steps {
		if step.ConfigWrite && i < 5 {
			t.Fatalf("step[%d] %s writes config before material/probe steps", i, step.Kind)
		}
	}
}

func TestDirectPairPlanBuildsConfigWriteIntents(t *testing.T) {
	t.Parallel()

	plan, err := BuildDirectPairPlan(DirectPairPlanInput{
		Local:  planHost("mac-a", "mac-a.tailnet", "jesse", 22),
		Remote: planHost("linux-b", "linux-b.tailnet", "jesse", 2200),
	})
	if err != nil {
		t.Fatalf("BuildDirectPairPlan() error = %v", err)
	}

	if len(plan.ConfigWrites) != 2 {
		t.Fatalf("ConfigWrites length = %d, want 2", len(plan.ConfigWrites))
	}
	connector := plan.ConfigWrites[0]
	if connector.TargetHostID != "linux-b" || connector.PeerID != "mac-a" {
		t.Fatalf("connector write = %#v", connector)
	}
	if !connector.Enabled || !connector.Connect || !connector.Accept || !connector.Persistent || connector.OnDemand {
		t.Fatalf("connector booleans = %#v", connector)
	}
	if connector.SSHHost != "mac-a.tailnet" || connector.SSHUser != "jesse" || connector.SSHPort != 22 {
		t.Fatalf("connector locator = %#v", connector)
	}
	if connector.InstallPath != "/Users/jesse/.local/bin/clipfan" || connector.GatewayPath != "/Users/jesse/.local/bin/clipfan" {
		t.Fatalf("connector paths = %#v", connector)
	}
	if connector.MigrationState != "loopback_unprovisioned" {
		t.Fatalf("connector MigrationState = %q", connector.MigrationState)
	}

	acceptor := plan.ConfigWrites[1]
	if acceptor.TargetHostID != "mac-a" || acceptor.PeerID != "linux-b" {
		t.Fatalf("acceptor write = %#v", acceptor)
	}
	if !acceptor.Enabled || !acceptor.Connect || !acceptor.Accept || !acceptor.Persistent || acceptor.OnDemand {
		t.Fatalf("acceptor booleans = %#v", acceptor)
	}
	if acceptor.SSHHost != "linux-b.tailnet" || acceptor.SSHUser != "jesse" || acceptor.SSHPort != 2200 {
		t.Fatalf("acceptor locator = %#v", acceptor)
	}
	if acceptor.InstallPath != "/home/jesse/.local/bin/clipfan" || acceptor.GatewayPath != "/home/jesse/.local/bin/clipfan" {
		t.Fatalf("acceptor paths = %#v", acceptor)
	}
	if acceptor.TargetGatewayPath != "/Users/jesse/.local/bin/clipfan" {
		t.Fatalf("acceptor target gateway path = %#v", acceptor)
	}
	if acceptor.MigrationState != "loopback_unprovisioned" {
		t.Fatalf("acceptor MigrationState = %q", acceptor.MigrationState)
	}
}

func TestDirectMeshPlanBuildsThreeHostFullMesh(t *testing.T) {
	t.Parallel()

	mesh, err := BuildDirectMeshPlan([]DirectPairHost{
		planHost("mac-a", "mac-a.tailnet", "jesse", 22),
		planHost("linux-b", "linux-b.tailnet", "jesse", 22),
		planHost("linux-c", "linux-c.tailnet", "jesse", 22),
	})
	if err != nil {
		t.Fatalf("BuildDirectMeshPlan() error = %v", err)
	}
	if len(mesh.Pairs) != 3 {
		t.Fatalf("pairs length = %d, want 3", len(mesh.Pairs))
	}
	wantIDs := []string{"linux-b--linux-c", "linux-b--mac-a", "linux-c--mac-a"}
	for i, want := range wantIDs {
		if mesh.Pairs[i].PairID != want {
			t.Fatalf("pair[%d] id = %q, want %q", i, mesh.Pairs[i].PairID, want)
		}
	}
}

func TestDirectPairPlanRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		input DirectPairPlanInput
	}{
		{name: "same host", input: DirectPairPlanInput{Local: planHost("mac-a", "mac-a.tailnet", "jesse", 22), Remote: planHost("mac-a", "mac-a.tailnet", "jesse", 22)}},
		{name: "missing host", input: DirectPairPlanInput{Local: planHost("mac-a", "", "jesse", 22), Remote: planHost("linux-b", "linux-b.tailnet", "jesse", 22)}},
		{name: "invalid user", input: DirectPairPlanInput{Local: planHost("mac-a", "mac-a.tailnet", "-bad", 22), Remote: planHost("linux-b", "linux-b.tailnet", "jesse", 22)}},
		{name: "invalid port", input: DirectPairPlanInput{Local: planHost("mac-a", "mac-a.tailnet", "jesse", 0), Remote: planHost("linux-b", "linux-b.tailnet", "jesse", 22)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := BuildDirectPairPlan(tc.input); err == nil {
				t.Fatal("BuildDirectPairPlan() error = nil, want error")
			}
		})
	}
}

func planHost(id, host, user string, port int) DirectPairHost {
	installPath := "/home/jesse/.local/bin/clipfan"
	if id == "mac-a" || id == "mac-b" {
		installPath = "/Users/jesse/.local/bin/clipfan"
	}
	return DirectPairHost{
		ID:          id,
		SSHHost:     host,
		SSHUser:     user,
		SSHPort:     port,
		InstallPath: installPath,
		GatewayPath: installPath,
	}
}
