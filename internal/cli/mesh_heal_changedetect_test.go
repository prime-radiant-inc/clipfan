package cli

import (
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

func healthyEdgePeerView() edgePeerView {
	return edgePeerView{
		Found:          true,
		Enabled:        true,
		Accept:         true,
		Connect:        true,
		MigrationState: config.MigrationStateSSHKeysReady,
		AcceptKeyID:    "accept-kid",
		ConnectKeyID:   "connect-kid",
	}
}

func TestEdgeIsHealthy(t *testing.T) {
	tests := []struct {
		name    string
		degrade func(*edgePeerView) // applied to ONE end; nil means leave both fully ready
		healthy bool
	}{
		{"both ends fully ready", nil, true},
		{"peer not found", func(v *edgePeerView) { v.Found = false }, false},
		{"peer disabled", func(v *edgePeerView) { v.Enabled = false }, false},
		{"accept off", func(v *edgePeerView) { v.Accept = false }, false},
		{"connect off", func(v *edgePeerView) { v.Connect = false }, false},
		{"state material staged", func(v *edgePeerView) { v.MigrationState = config.MigrationStateSSHMaterialStaged }, false},
		{"state provision failed", func(v *edgePeerView) { v.MigrationState = config.MigrationStateProvisionFailed }, false},
		{"empty accept key id", func(v *edgePeerView) { v.AcceptKeyID = "" }, false},
		{"empty connect key id", func(v *edgePeerView) { v.ConnectKeyID = "" }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.degrade == nil {
				if !edgeIsHealthy(healthyEdgePeerView(), healthyEdgePeerView()) {
					t.Fatalf("two fully-ready ends should be healthy")
				}
				return
			}
			// An edge is unhealthy if EITHER end is degraded; degrade each end
			// in turn to prove the predicate checks both, not just the first arg.
			for _, brokenEnd := range []string{"a", "b"} {
				a := healthyEdgePeerView()
				b := healthyEdgePeerView()
				if brokenEnd == "a" {
					tt.degrade(&a)
				} else {
					tt.degrade(&b)
				}
				if got := edgeIsHealthy(a, b); got != tt.healthy {
					t.Fatalf("edgeIsHealthy(%s degraded) = %v, want %v (a=%+v b=%+v)", brokenEnd, got, tt.healthy, a, b)
				}
			}
		})
	}
}
