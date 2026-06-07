package cli

import (
	"context"

	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

// hostPrep is one host's resolved provisioning inputs: the host with its Host
// endpoint resolved (ssh -G) and AdminHost holding the original admin endpoint,
// its detected server mode, plus the confirmed sync host-key pin line. mesh-heal
// feeds these into the pair provisioner without the provisioner re-keyscanning.
type hostPrep struct {
	Host        sshprovision.DirectPairProvisionHost
	HostKeyLine string
}

// prepHosts resolves and keyscans every host for mesh-heal, capturing per-host
// failures in errs rather than aborting the whole run — one unreachable host
// must not block healing the rest. Returns the successful preps keyed by host id
// and the failures keyed by host id.
func prepHosts(ctx context.Context, runner sshprovision.CommandRunner, hosts []sshprovision.DirectPairProvisionHost) (map[string]hostPrep, map[string]error) {
	preps := make(map[string]hostPrep, len(hosts))
	errs := map[string]error{}
	for _, host := range hosts {
		resolved, line, err := prepHost(ctx, runner, host)
		if err != nil {
			errs[host.Host.ID] = err
			continue
		}
		preps[resolved.Host.ID] = hostPrep{Host: resolved, HostKeyLine: line}
	}
	return preps, errs
}
