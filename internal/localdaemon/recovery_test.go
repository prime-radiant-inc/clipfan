package localdaemon

import (
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

func TestPlanRecoveryConfigV2OldClientSignedRepairIsNotTrap(t *testing.T) {
	plan := PlanRecovery(versionedConfig(2), RecoveryOptions{
		ClientSupportsHKDF:            false,
		ValidSharedKey:                true,
		SignedListenerRepairAvailable: true,
	})

	assertOldClientBlockedFromRawWrites(t, plan)
	if !plan.Recoverable || plan.Blocked {
		t.Fatalf("signed repair plan recoverable=%v blocked=%v", plan.Recoverable, plan.Blocked)
	}
	assertRepairPaths(t, plan, []RepairPath{RepairPathSignedListener})
}

func TestPlanRecoveryConfigV2OldClientOfflineRepairIsNotTrap(t *testing.T) {
	plan := PlanRecovery(versionedConfig(2), RecoveryOptions{
		ClientSupportsHKDF:             false,
		ValidSharedKey:                 false,
		OfflineListenerRepairAvailable: true,
	})

	assertOldClientBlockedFromRawWrites(t, plan)
	if !plan.Recoverable || plan.Blocked {
		t.Fatalf("offline repair plan recoverable=%v blocked=%v", plan.Recoverable, plan.Blocked)
	}
	assertRepairPaths(t, plan, []RepairPath{RepairPathOfflineListener})
}

func TestPlanRecoveryConfigV2OldClientFallsBackToOfflineWhenSignedRepairUnavailable(t *testing.T) {
	plan := PlanRecovery(versionedConfig(2), RecoveryOptions{
		ClientSupportsHKDF:             false,
		ValidSharedKey:                 true,
		OfflineListenerRepairAvailable: true,
	})

	assertOldClientBlockedFromRawWrites(t, plan)
	if !plan.Recoverable || plan.Blocked {
		t.Fatalf("offline fallback plan recoverable=%v blocked=%v", plan.Recoverable, plan.Blocked)
	}
	assertRepairPaths(t, plan, []RepairPath{RepairPathOfflineListener})
}

func TestPlanRecoveryConfigV2OldClientWithoutRepairPathIsBlockedNotRaw(t *testing.T) {
	plan := PlanRecovery(versionedConfig(2), RecoveryOptions{
		ClientSupportsHKDF: false,
		ValidSharedKey:     true,
	})

	assertOldClientBlockedFromRawWrites(t, plan)
	if plan.Recoverable || !plan.Blocked {
		t.Fatalf("missing repair plan recoverable=%v blocked=%v", plan.Recoverable, plan.Blocked)
	}
	assertRepairPaths(t, plan, nil)
}

func TestPlanRecoveryPreV2StaysLegacyCompatible(t *testing.T) {
	plan := PlanRecovery(&config.Config{SharedKey: "k"}, RecoveryOptions{ClientSupportsHKDF: false})

	if !plan.AllowRawSignedRequests || !plan.AllowWholeConfigWrite || !plan.Recoverable || plan.Blocked {
		t.Fatalf("pre-v2 plan = %+v", plan)
	}
	if plan.Diagnostic != "" || len(plan.RepairPaths) != 0 {
		t.Fatalf("pre-v2 diagnostics/paths = %+v", plan)
	}
}

func assertOldClientBlockedFromRawWrites(t *testing.T, plan RecoveryPlan) {
	t.Helper()
	if plan.AllowRawSignedRequests || plan.AllowWholeConfigWrite {
		t.Fatalf("old-client config-v2 plan allowed raw writes: %+v", plan)
	}
	if plan.Diagnostic != StartupDiagnosticConfigV2RequiresHKDFClient {
		t.Fatalf("diagnostic = %q, want %q", plan.Diagnostic, StartupDiagnosticConfigV2RequiresHKDFClient)
	}
}

func assertRepairPaths(t *testing.T, plan RecoveryPlan, want []RepairPath) {
	t.Helper()
	if len(plan.RepairPaths) != len(want) {
		t.Fatalf("repair paths = %#v, want %#v", plan.RepairPaths, want)
	}
	for i := range want {
		if plan.RepairPaths[i] != want[i] {
			t.Fatalf("repair paths = %#v, want %#v", plan.RepairPaths, want)
		}
	}
}

func versionedConfig(version int) *config.Config {
	return &config.Config{ConfigVersion: &version, SharedKey: "k"}
}
