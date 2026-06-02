package localdaemon

import (
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
)

func TestPlanStartupKeepsPreV2RawCompatibility(t *testing.T) {
	plan := PlanStartup(&config.Config{SharedKey: "k"}, StartupOptions{ClientSupportsHKDF: false})

	if !plan.AllowRawSignedRequests {
		t.Fatal("pre-v2 plan should allow raw signed requests")
	}
	if !plan.AllowWholeConfigWrite {
		t.Fatal("pre-v2 plan should allow legacy whole-config write path")
	}
	if plan.Diagnostic != "" || plan.AuthVersion != "" {
		t.Fatalf("pre-v2 plan = %+v", plan)
	}
}

func TestPlanStartupConfigV2OldClientFailsBeforeRawWrites(t *testing.T) {
	version := 2
	plan := PlanStartup(&config.Config{ConfigVersion: &version, SharedKey: "k"}, StartupOptions{ClientSupportsHKDF: false})

	if plan.AllowRawSignedRequests || plan.AllowWholeConfigWrite {
		t.Fatalf("config-v2 old-client plan allowed raw write path: %+v", plan)
	}
	if plan.Diagnostic != StartupDiagnosticConfigV2RequiresHKDFClient {
		t.Fatalf("diagnostic = %q", plan.Diagnostic)
	}
}

func TestPlanStartupConfigV2NewClientUsesHKDFOnly(t *testing.T) {
	version := 2
	plan := PlanStartup(&config.Config{ConfigVersion: &version, SharedKey: "k"}, StartupOptions{ClientSupportsHKDF: true})

	if plan.AuthVersion != transport.AuthVersionRequestHMAC {
		t.Fatalf("auth version = %q", plan.AuthVersion)
	}
	if plan.AllowRawSignedRequests || plan.AllowWholeConfigWrite || plan.Diagnostic != "" {
		t.Fatalf("config-v2 new-client plan = %+v", plan)
	}
}
