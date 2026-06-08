package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

type fakeObserveRunner struct {
	byHost map[string]string // observer host -> snippet stdout
	fail   map[string]bool
	calls  []string
}

func (r *fakeObserveRunner) Run(_ context.Context, command sshprovision.SSHCommand) (sshprovision.CommandOutput, error) {
	target := command.Args[len(command.Args)-2]
	host := target
	if i := strings.Index(target, "@"); i >= 0 {
		host = target[i+1:]
	}
	r.calls = append(r.calls, host)
	if r.fail[host] {
		return sshprovision.CommandOutput{}, errors.New("ssh failed")
	}
	return sshprovision.CommandOutput{Stdout: []byte(r.byHost[host])}, nil
}

func TestObservedSelfAddressSnippetMatchesSwift(t *testing.T) {
	want := `v=${SSH_CONNECTION:-$SSH_CLIENT}; v=${v%% *}; test -n "$v" || exit 44; printf '%s\n' "$v"`
	if observedSelfAddressSnippet != want {
		t.Fatalf("snippet drifted from Swift:\n got: %s\nwant: %s", observedSelfAddressSnippet, want)
	}
}

func TestObserveSelfAddressReturnsValidatedAddress(t *testing.T) {
	runner := &fakeObserveRunner{byHost: map[string]string{"obs.example": "100.114.54.38\n"}}
	addr, err := observeSelfAddress(context.Background(), runner, rosterEndpoint{
		ID: "obs", SSHUser: "jesse", SSHHost: "obs.example", SSHPort: 22,
	}, "/Users/jesse/.ssh/known_hosts")
	if err != nil {
		t.Fatalf("observeSelfAddress: %v", err)
	}
	if addr != "100.114.54.38" {
		t.Fatalf("addr = %q", addr)
	}
}

func TestObserveSelfAddressRejectsMultiline(t *testing.T) {
	runner := &fakeObserveRunner{byHost: map[string]string{"obs.example": "1.2.3.4\n5.6.7.8\n"}}
	if _, err := observeSelfAddress(context.Background(), runner, rosterEndpoint{ID: "obs", SSHUser: "jesse", SSHHost: "obs.example", SSHPort: 22}, "/kh"); err == nil {
		t.Fatalf("expected error for multiline observation")
	}
}

func TestObserveSelfAddressRejectsInjection(t *testing.T) {
	runner := &fakeObserveRunner{byHost: map[string]string{"obs.example": "1.2.3.4; rm -rf /\n"}}
	if _, err := observeSelfAddress(context.Background(), runner, rosterEndpoint{ID: "obs", SSHUser: "jesse", SSHHost: "obs.example", SSHPort: 22}, "/kh"); err == nil {
		t.Fatalf("expected error for metachar address")
	}
}

func TestDiscoverSelfAddressTriesObserversInTurn(t *testing.T) {
	runner := &fakeObserveRunner{
		byHost: map[string]string{"live.example": "10.0.0.5\n"},
		fail:   map[string]bool{"dead.example": true},
	}
	addr, err := discoverSelfAddress(context.Background(), runner, []rosterEndpoint{
		{ID: "dead", SSHUser: "jesse", SSHHost: "dead.example", SSHPort: 22},
		{ID: "live", SSHUser: "jesse", SSHHost: "live.example", SSHPort: 22},
	}, "/kh")
	if err != nil {
		t.Fatalf("discoverSelfAddress: %v", err)
	}
	if addr != "10.0.0.5" {
		t.Fatalf("addr = %q (should skip the dead observer)", addr)
	}
}

func TestDiscoverSelfAddressPrefersTailscale(t *testing.T) {
	runner := &fakeObserveRunner{
		byHost: map[string]string{"lan.example": "192.168.1.5\n", "ts.example": "100.114.54.38\n"},
	}
	addr, err := discoverSelfAddress(context.Background(), runner, []rosterEndpoint{
		{ID: "lan", SSHUser: "jesse", SSHHost: "lan.example", SSHPort: 22},
		{ID: "ts", SSHUser: "jesse", SSHHost: "ts.example", SSHPort: 22},
	}, "/kh")
	if err != nil {
		t.Fatalf("discoverSelfAddress: %v", err)
	}
	if addr != "100.114.54.38" {
		t.Fatalf("addr = %q, want the Tailscale 100.x form", addr)
	}
}

func TestDiscoverSelfAddressNoObservers(t *testing.T) {
	if _, err := discoverSelfAddress(context.Background(), &fakeObserveRunner{}, nil, "/kh"); err == nil {
		t.Fatalf("expected error with no observers")
	}
}

func TestValidateMeshSSHHost(t *testing.T) {
	valid := []string{"100.114.54.38", "host.example", "fe80::1", "2001:db8::1"}
	invalid := []string{"", "-bad", "a b", "a;b", "a$b", "a@b", "1.2.3.4:22", "has:colon:notip", "a|b", "a`b"}
	for _, v := range valid {
		if err := validateMeshSSHHost(v); err != nil {
			t.Errorf("validateMeshSSHHost(%q) = %v, want nil", v, err)
		}
	}
	for _, v := range invalid {
		if err := validateMeshSSHHost(v); err == nil {
			t.Errorf("validateMeshSSHHost(%q) = nil, want error", v)
		}
	}
}
