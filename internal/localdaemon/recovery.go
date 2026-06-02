package localdaemon

import "github.com/prime-radiant-inc/clipfan/internal/config"

type RepairPath string

const (
	RepairPathSignedListener           RepairPath = "signed_listener_repair"
	RepairPathOfflineListener          RepairPath = "offline_listener_repair"
	RepairPathConfirmedLocalFleetReset RepairPath = "confirmed_local_fleet_reset"
)

type RecoveryOptions struct {
	ClientSupportsHKDF             bool
	ValidSharedKey                 bool
	UnsafeListener                 bool
	SignedListenerRepairAvailable  bool
	OfflineListenerRepairAvailable bool
	LocalFleetResetAvailable       bool
}

type RecoveryPlan struct {
	StartupPlan
	RepairPaths []RepairPath
	Recoverable bool
	Blocked     bool
}

func PlanRecovery(cfg *config.Config, opts RecoveryOptions) RecoveryPlan {
	startup := PlanStartup(cfg, StartupOptions{ClientSupportsHKDF: opts.ClientSupportsHKDF})
	plan := RecoveryPlan{StartupPlan: startup}
	if cfg == nil || cfg.ConfigVersion == nil || *cfg.ConfigVersion < 2 {
		plan.Recoverable = true
		return plan
	}
	if opts.ClientSupportsHKDF {
		plan.Recoverable = true
		return plan
	}

	if opts.ValidSharedKey && opts.SignedListenerRepairAvailable {
		plan.RepairPaths = append(plan.RepairPaths, RepairPathSignedListener)
	}
	if len(plan.RepairPaths) == 0 && opts.UnsafeListener && opts.OfflineListenerRepairAvailable {
		plan.RepairPaths = append(plan.RepairPaths, RepairPathOfflineListener)
	}
	if len(plan.RepairPaths) == 0 && !opts.ValidSharedKey && !opts.UnsafeListener && opts.LocalFleetResetAvailable {
		plan.RepairPaths = append(plan.RepairPaths, RepairPathConfirmedLocalFleetReset)
	}

	plan.Recoverable = len(plan.RepairPaths) > 0
	plan.Blocked = !plan.Recoverable
	return plan
}
