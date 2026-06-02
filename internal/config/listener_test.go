package config

import "testing"

func TestPlanListenerCurrentPublicGateLeavesConfiguredListenAlone(t *testing.T) {
	plan := PlanListener(Config{Listen: "0.0.0.0:9000", Port: 7853}, false)

	if plan.SafeMode {
		t.Fatal("current public gate unexpectedly classified safe mode")
	}
	if plan.BindListen != "0.0.0.0:9000" {
		t.Fatalf("BindListen = %q, want configured public listen", plan.BindListen)
	}
	if !plan.PeerSyncStarted {
		t.Fatal("PeerSyncStarted = false, want true for current public gate")
	}
}

func TestPlanListenerNormalizesLoopbackListensWhenBoundaryEnabled(t *testing.T) {
	for _, listen := range []string{"localhost:9001", "127.0.0.2:9001", "[::1]:9001"} {
		t.Run(listen, func(t *testing.T) {
			plan := PlanListener(Config{Listen: listen, Port: 7853}, true)
			if plan.SafeMode {
				t.Fatalf("SafeMode = true for loopback listen %q", listen)
			}
			if plan.BindListen != "127.0.0.1:9001" {
				t.Fatalf("BindListen = %q, want canonical loopback", plan.BindListen)
			}
			if !plan.PeerSyncStarted {
				t.Fatal("PeerSyncStarted = false, want true for loopback listen")
			}
		})
	}
}

func TestPlanListenerTreatsExactLegacyGeneratedDefaultsAsNormalLoopback(t *testing.T) {
	for _, listen := range []string{":7853", "0.0.0.0:7853", "[::]:7853"} {
		t.Run(listen, func(t *testing.T) {
			plan := PlanListener(Config{Listen: listen, Port: 7853}, true)
			if plan.SafeMode {
				t.Fatalf("SafeMode = true for generated default %q", listen)
			}
			if plan.BindListen != "127.0.0.1:7853" {
				t.Fatalf("BindListen = %q, want canonical generated loopback", plan.BindListen)
			}
		})
	}
}

func TestPlanListenerClassifiesExplicitNonLoopbackAsSafeMode(t *testing.T) {
	cases := []struct {
		listen string
		bind   string
	}{
		{listen: ":9000", bind: "127.0.0.1:9000"},
		{listen: "0.0.0.0:9000", bind: "127.0.0.1:9000"},
		{listen: "[::]:9000", bind: "127.0.0.1:9000"},
		{listen: "203.0.113.10:9000", bind: "127.0.0.1:9000"},
	}
	for _, tc := range cases {
		t.Run(tc.listen, func(t *testing.T) {
			plan := PlanListener(Config{Listen: tc.listen, Port: 7853}, true)
			if !plan.SafeMode {
				t.Fatalf("SafeMode = false for explicit non-loopback %q", tc.listen)
			}
			if plan.BindListen != tc.bind || plan.EffectiveRepairListen != tc.bind {
				t.Fatalf("bind/repair = %q/%q, want %q", plan.BindListen, plan.EffectiveRepairListen, tc.bind)
			}
			if plan.PeerSyncStarted {
				t.Fatal("PeerSyncStarted = true, want false in safe mode")
			}
		})
	}
}

func TestPlanListenerDerivesSafeModePortFromConfigWhenListenCannotParse(t *testing.T) {
	plan := PlanListener(Config{Listen: "bad-listen", Port: 49125}, true)

	if !plan.SafeMode {
		t.Fatal("SafeMode = false for malformed listen")
	}
	if plan.BindListen != "127.0.0.1:49125" {
		t.Fatalf("BindListen = %q, want config port fallback", plan.BindListen)
	}
}

func TestPlanListenerReportsInvalidListenPortWhenNoPortCanBeDerived(t *testing.T) {
	plan := PlanListener(Config{Listen: "bad-listen", Port: 70000}, true)

	if !plan.SafeMode {
		t.Fatal("SafeMode = false for malformed listen")
	}
	if plan.BindListen != "127.0.0.1:7853" {
		t.Fatalf("BindListen = %q, want default repair port", plan.BindListen)
	}
	if plan.ParseError != "invalid_listen_port" {
		t.Fatalf("ParseError = %q, want invalid_listen_port", plan.ParseError)
	}
}
