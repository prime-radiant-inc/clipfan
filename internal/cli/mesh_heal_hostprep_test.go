package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

type fakePrepRunner struct {
	resolveTo       map[string]string // SSHHost -> resolved hostname (ssh -G rewrite)
	failKeyscanHost string
}

func (r *fakePrepRunner) Run(_ context.Context, command sshprovision.SSHCommand) (sshprovision.CommandOutput, error) {
	if command.Args[0] == "ssh" && len(command.Args) > 1 && command.Args[1] == "-G" {
		host := command.Args[len(command.Args)-1]
		resolved := host
		if r.resolveTo[host] != "" {
			resolved = r.resolveTo[host]
		}
		return sshprovision.CommandOutput{Stdout: []byte("hostname " + resolved + "\nport 22\n")}, nil
	}
	if command.Args[0] == "ssh-keyscan" {
		host := command.Args[len(command.Args)-1]
		if host == r.failKeyscanHost {
			return sshprovision.CommandOutput{}, errors.New("keyscan refused")
		}
		return sshprovision.CommandOutput{Stdout: []byte(host + " ssh-ed25519 " + testDirectProvisionPublicKey(host))}, nil
	}
	return sshprovision.CommandOutput{}, errors.New("unexpected: " + strings.Join(command.Args, " "))
}

func TestPrepHostsResilientWithAdminResolvedSplit(t *testing.T) {
	hosts := []sshprovision.DirectPairProvisionHost{
		{Host: sshprovision.DirectPairHost{ID: "alpha", SSHUser: "jesse", SSHHost: "alpha.example", SSHPort: 22, InstallPath: "/clipfan", GatewayPath: "/clipfan"}},
		{Host: sshprovision.DirectPairHost{ID: "bravo", SSHUser: "jesse", SSHHost: "bravo.example", SSHPort: 22, InstallPath: "/clipfan", GatewayPath: "/clipfan"}},
	}
	// alpha's ssh -G rewrites the host (proving the admin/resolved split);
	// bravo's keyscan fails (proving one host's failure doesn't sink the others).
	runner := &fakePrepRunner{
		resolveTo:       map[string]string{"alpha.example": "alpha-real.example"},
		failKeyscanHost: "bravo.example",
	}

	preps, errs := prepHosts(context.Background(), runner, hosts)

	if len(preps) != 1 {
		t.Fatalf("preps = %+v", preps)
	}
	prep, ok := preps["alpha"]
	if !ok {
		t.Fatalf("alpha missing: %+v", preps)
	}
	if prep.Host.Host.SSHHost != "alpha-real.example" {
		t.Fatalf("alpha resolved host = %q, want alpha-real.example", prep.Host.Host.SSHHost)
	}
	if prep.Host.AdminHost.SSHHost != "alpha.example" {
		t.Fatalf("alpha admin host = %q, want alpha.example", prep.Host.AdminHost.SSHHost)
	}
	if !strings.Contains(prep.HostKeyLine, "alpha-real.example ssh-ed25519 ") {
		t.Fatalf("alpha sync pin line = %q", prep.HostKeyLine)
	}

	if _, ok := preps["bravo"]; ok {
		t.Fatalf("bravo should not be in preps")
	}
	if _, ok := errs["bravo"]; !ok {
		t.Fatalf("bravo should have an error: %+v", errs)
	}
	if _, ok := errs["alpha"]; ok {
		t.Fatalf("alpha should not have an error: %+v", errs)
	}
}
