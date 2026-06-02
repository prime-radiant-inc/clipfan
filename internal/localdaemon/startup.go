package localdaemon

import (
	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
)

const (
	StartupDiagnosticConfigV2RequiresHKDFClient = "config_v2_requires_hkdf_client"
)

type StartupPlan struct {
	AuthVersion            string
	AllowRawSignedRequests bool
	AllowWholeConfigWrite  bool
	Diagnostic             string
}

type StartupOptions struct {
	ClientSupportsHKDF bool
}

func PlanStartup(cfg *config.Config, opts StartupOptions) StartupPlan {
	if cfg != nil && cfg.ConfigVersion != nil && *cfg.ConfigVersion == 2 {
		if !opts.ClientSupportsHKDF {
			return StartupPlan{
				AllowRawSignedRequests: false,
				AllowWholeConfigWrite:  false,
				Diagnostic:             StartupDiagnosticConfigV2RequiresHKDFClient,
			}
		}
		return StartupPlan{
			AuthVersion:            transport.AuthVersionRequestHMAC,
			AllowRawSignedRequests: false,
			AllowWholeConfigWrite:  false,
		}
	}
	return StartupPlan{
		AllowRawSignedRequests: true,
		AllowWholeConfigWrite:  true,
	}
}
