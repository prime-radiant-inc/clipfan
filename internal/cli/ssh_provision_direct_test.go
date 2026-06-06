package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

const (
	testDirectProvisionEd25519Key      = "AAAAC3NzaC1lZDI1NTE5AAAAIC6JxQKUfHw2JMc2+5ZUTc5xI8QX1sGm8c5C7h4eY7p1"
	testDirectProvisionOtherEd25519Key = "AAAAC3NzaC1lZDI1NTE5AAAAIHP7O1LPaDr6RfFdqHtKc9m8gw98RK54GpcfwoAK2JhH"
	testDirectProvisionThirdEd25519Key = "AAAAC3NzaC1lZDI1NTE5AAAAIHRoaXJkLXRlc3QtZWQyNTUxOS1wdWJsaWMta2V5ISEh"
	testDirectProvisionRSAKey          = "AAAAB3NzaC1yc2EAAAADAQABAAABAQC7kMUR5W3sljGXhgmwsMOFGv17tZuxKQnF4k8sJgMhaY20"
	testDirectProvisionEd25519KeyID    = "626e58c17d770373"
	testDirectProvisionSharedKey       = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="
)

func TestRunSSHProvisionDirectBuildsThreeHostMesh(t *testing.T) {
	t.Parallel()

	runner := &fakeDirectProvisionRunner{}
	var stdout, stderr bytes.Buffer

	err := runSSHProvisionDirect([]string{
		"--trust-keyscan",
		"--regular-known-hosts", "/Users/jesse/.ssh/known_hosts",
		"--host", "id=mac-a,ssh=mac-a.tailnet,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519",
		"--host", "id=linux-b,ssh=linux-b.tailnet,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/Application Support/Clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519",
		"--host", "id=linux-c,ssh=linux-c.tailnet,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/.config/clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519",
	}, &stdout, &stderr, sshProvisionDirectOptions{
		Runner:            runner,
		ConfigV2WriteGate: func() bool { return true },
		SharedKey:         func() (string, error) { return testDirectProvisionSharedKey, nil },
	})
	if err != nil {
		t.Fatalf("runSSHProvisionDirect() error = %v stderr=%q", err, stderr.String())
	}

	var payload struct {
		Status string `json:"status"`
		Pairs  []struct {
			PairID string `json:"pair_id"`
		} `json:"pairs"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "ok" {
		t.Fatalf("status = %q", payload.Status)
	}
	gotPairs := make([]string, 0, len(payload.Pairs))
	for _, pair := range payload.Pairs {
		gotPairs = append(gotPairs, pair.PairID)
	}
	wantPairs := []string{"linux-b--mac-a", "linux-c--mac-a", "linux-b--linux-c"}
	if !reflect.DeepEqual(gotPairs, wantPairs) {
		t.Fatalf("pairs:\n got %#v\nwant %#v", gotPairs, wantPairs)
	}
	wantKeyscans := []string{"keyscan:mac-a.tailnet:22", "keyscan:linux-b.tailnet:22", "keyscan:linux-c.tailnet:22"}
	if !reflect.DeepEqual(runner.keyscans, wantKeyscans) {
		t.Fatalf("keyscans:\n got %#v\nwant %#v", runner.keyscans, wantKeyscans)
	}
	wantApplies := []string{
		"linux-b:stage", "mac-a:stage", "linux-b:ready", "mac-a:ready",
		"linux-c:stage", "mac-a:stage", "linux-c:ready", "mac-a:ready",
		"linux-b:stage", "linux-c:stage", "linux-b:ready", "linux-c:ready",
	}
	if !reflect.DeepEqual(runner.configApplies, wantApplies) {
		t.Fatalf("config applies:\n got %#v\nwant %#v", runner.configApplies, wantApplies)
	}
	if len(runner.configApplySharedKeys) != len(wantApplies) {
		t.Fatalf("shared-key payload count = %d, want %d", len(runner.configApplySharedKeys), len(wantApplies))
	}
	for i, got := range runner.configApplySharedKeys {
		if got != testDirectProvisionSharedKey {
			t.Fatalf("shared key payload %d = %q, want fleet key", i, got)
		}
	}
}

func TestRunSSHProvisionDirectResolvesSSHConfigHostNameForRuntimeTarget(t *testing.T) {
	t.Parallel()

	runner := &fakeDirectProvisionRunner{sshConfigOutputs: map[string]string{
		"jesse@mac-a":   "hostname 100.64.0.10\nport 22\n",
		"jesse@linux-b": "hostname linux-b\nport 22\n",
	}}
	var stdout, stderr bytes.Buffer

	err := runSSHProvisionDirect([]string{
		"--trust-keyscan",
		"--regular-known-hosts", "/Users/jesse/.ssh/known_hosts",
		"--host", "id=mac-a,ssh=mac-a,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519",
		"--host", "id=linux-b,ssh=linux-b,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/Application Support/Clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519",
	}, &stdout, &stderr, sshProvisionDirectOptions{
		Runner:            runner,
		ConfigV2WriteGate: func() bool { return true },
		SharedKey:         func() (string, error) { return testDirectProvisionSharedKey, nil },
	})
	if err != nil {
		t.Fatalf("runSSHProvisionDirect() error = %v stderr=%q", err, stderr.String())
	}
	wantKeyscans := []string{"keyscan:100.64.0.10:22", "keyscan:linux-b:22"}
	if !reflect.DeepEqual(runner.keyscans, wantKeyscans) {
		t.Fatalf("keyscans:\n got %#v\nwant %#v", runner.keyscans, wantKeyscans)
	}
	if !containsString(runner.regularTargets, "jesse@mac-a") {
		t.Fatalf("regular targets = %#v, want admin alias jesse@mac-a", runner.regularTargets)
	}
	if !containsString(runner.configPeerEndpoints, "mac-a=100.64.0.10:22") {
		t.Fatalf("config peer endpoints = %#v, want resolved runtime host", runner.configPeerEndpoints)
	}
}

func TestRunSSHProvisionDirectUsesSSHConfigBeforeKeyscan(t *testing.T) {
	t.Parallel()

	runner := &fakeDirectProvisionRunner{sshConfigOutputs: map[string]string{
		"jesse@mac-a":   "hostname mac-a.\nport 22\n",
		"jesse@linux-b": "hostname linux-b.\nport 22\n",
	}}
	var stdout, stderr bytes.Buffer

	err := runSSHProvisionDirect([]string{
		"--trust-keyscan",
		"--regular-known-hosts", "/Users/jesse/.ssh/known_hosts",
		"--host", "id=mac-a,ssh=mac-a,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519",
		"--host", "id=linux-b,ssh=linux-b,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/Application Support/Clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519",
	}, &stdout, &stderr, sshProvisionDirectOptions{
		Runner:            runner,
		ConfigV2WriteGate: func() bool { return true },
		SharedKey:         func() (string, error) { return testDirectProvisionSharedKey, nil },
	})
	if err != nil {
		t.Fatalf("runSSHProvisionDirect() error = %v stderr=%q", err, stderr.String())
	}

	wantConfigLookups := []string{"config:jesse@mac-a:22", "config:jesse@linux-b:22"}
	if !reflect.DeepEqual(runner.configLookups, wantConfigLookups) {
		t.Fatalf("config lookups:\n got %#v\nwant %#v", runner.configLookups, wantConfigLookups)
	}
	wantKeyscans := []string{"keyscan:mac-a:22", "keyscan:linux-b:22"}
	if !reflect.DeepEqual(runner.keyscans, wantKeyscans) {
		t.Fatalf("keyscans:\n got %#v\nwant %#v", runner.keyscans, wantKeyscans)
	}
}

func TestRunSSHProvisionDirectRejectsUnsupportedSSHConfigProxyBeforeKeyscan(t *testing.T) {
	t.Parallel()

	runner := &fakeDirectProvisionRunner{sshConfigOutputs: map[string]string{
		"jesse@mac-a": "hostname mac-a\nport 22\nproxyjump bastion\n",
	}}
	var stdout, stderr bytes.Buffer

	err := runSSHProvisionDirect([]string{
		"--trust-keyscan",
		"--regular-known-hosts", "/Users/jesse/.ssh/known_hosts",
		"--host", "id=mac-a,ssh=mac-a,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519",
		"--host", "id=linux-b,ssh=linux-b,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/Application Support/Clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519",
	}, &stdout, &stderr, sshProvisionDirectOptions{
		Runner:            runner,
		ConfigV2WriteGate: func() bool { return true },
		SharedKey:         func() (string, error) { return testDirectProvisionSharedKey, nil },
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported_ssh_config_for_keyscan") {
		t.Fatalf("runSSHProvisionDirect() error = %v, want unsupported_ssh_config_for_keyscan", err)
	}
	if len(runner.keyscans) != 0 {
		t.Fatalf("keyscans = %#v, want none", runner.keyscans)
	}
}

func TestRunSSHProvisionDirectAllowsDefaultSSHConfigHostKeyAlias(t *testing.T) {
	t.Parallel()

	runner := &fakeDirectProvisionRunner{sshConfigOutputs: map[string]string{
		"jesse@mac-a":   "hostname mac-a\nport 22\nhostkeyalias none\n",
		"jesse@linux-b": "hostname linux-b\nport 22\n",
	}}
	var stdout, stderr bytes.Buffer

	err := runSSHProvisionDirect([]string{
		"--trust-keyscan",
		"--regular-known-hosts", "/Users/jesse/.ssh/known_hosts",
		"--host", "id=mac-a,ssh=mac-a,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519",
		"--host", "id=linux-b,ssh=linux-b,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/Application Support/Clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519",
	}, &stdout, &stderr, sshProvisionDirectOptions{
		Runner:            runner,
		ConfigV2WriteGate: func() bool { return true },
		SharedKey:         func() (string, error) { return testDirectProvisionSharedKey, nil },
	})
	if err != nil {
		t.Fatalf("runSSHProvisionDirect() error = %v stderr=%q", err, stderr.String())
	}
	wantKeyscans := []string{"keyscan:mac-a:22", "keyscan:linux-b:22"}
	if !reflect.DeepEqual(runner.keyscans, wantKeyscans) {
		t.Fatalf("keyscans:\n got %#v\nwant %#v", runner.keyscans, wantKeyscans)
	}
}

func TestRunSSHProvisionDirectRejectsSSHConfigHostKeyAliasBeforeKeyscan(t *testing.T) {
	t.Parallel()

	runner := &fakeDirectProvisionRunner{sshConfigOutputs: map[string]string{
		"jesse@mac-a": "hostname mac-a\nport 22\nhostkeyalias fleet-alias\n",
	}}
	var stdout, stderr bytes.Buffer

	err := runSSHProvisionDirect([]string{
		"--trust-keyscan",
		"--regular-known-hosts", "/Users/jesse/.ssh/known_hosts",
		"--host", "id=mac-a,ssh=mac-a,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519",
		"--host", "id=linux-b,ssh=linux-b,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/Application Support/Clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519",
	}, &stdout, &stderr, sshProvisionDirectOptions{
		Runner:            runner,
		ConfigV2WriteGate: func() bool { return true },
		SharedKey:         func() (string, error) { return testDirectProvisionSharedKey, nil },
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported_ssh_config_for_keyscan") {
		t.Fatalf("runSSHProvisionDirect() error = %v, want unsupported_ssh_config_for_keyscan", err)
	}
	if len(runner.keyscans) != 0 {
		t.Fatalf("keyscans = %#v, want none", runner.keyscans)
	}
}

func TestRunSSHProvisionDirectResolvesSSHConfigPortForRuntimeTarget(t *testing.T) {
	t.Parallel()

	runner := &fakeDirectProvisionRunner{sshConfigOutputs: map[string]string{
		"jesse@mac-a":   "hostname mac-a\nport 2222\n",
		"jesse@linux-b": "hostname linux-b\nport 22\n",
	}}
	var stdout, stderr bytes.Buffer

	err := runSSHProvisionDirect([]string{
		"--trust-keyscan",
		"--regular-known-hosts", "/Users/jesse/.ssh/known_hosts",
		"--host", "id=mac-a,ssh=mac-a,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519",
		"--host", "id=linux-b,ssh=linux-b,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/Application Support/Clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519",
	}, &stdout, &stderr, sshProvisionDirectOptions{
		Runner:            runner,
		ConfigV2WriteGate: func() bool { return true },
		SharedKey:         func() (string, error) { return testDirectProvisionSharedKey, nil },
	})
	if err != nil {
		t.Fatalf("runSSHProvisionDirect() error = %v stderr=%q", err, stderr.String())
	}
	wantKeyscans := []string{"keyscan:mac-a:2222", "keyscan:linux-b:22"}
	if !reflect.DeepEqual(runner.keyscans, wantKeyscans) {
		t.Fatalf("keyscans:\n got %#v\nwant %#v", runner.keyscans, wantKeyscans)
	}
	if !containsString(runner.regularTargets, "jesse@mac-a") {
		t.Fatalf("regular targets = %#v, want admin alias jesse@mac-a", runner.regularTargets)
	}
	if !containsString(runner.configPeerEndpoints, "mac-a=mac-a:2222") {
		t.Fatalf("config peer endpoints = %#v, want resolved runtime port", runner.configPeerEndpoints)
	}
}

func TestSelectSSHProvisionDirectHostKeyLinePrefersEd25519OverRSA(t *testing.T) {
	t.Parallel()

	line, err := selectSSHProvisionDirectHostKeyLine(
		sshprovision.DirectPairHost{ID: "magic-kingdom", SSHHost: "magic-kingdom", SSHPort: 22},
		sshProvisionDirectKeyscanTarget{Host: "magic-kingdom", Port: 22},
		strings.Join([]string{
			"# magic-kingdom:22 SSH-2.0-OpenSSH",
			"magic-kingdom ssh-rsa " + testDirectProvisionRSAKey,
			"magic-kingdom ssh-ed25519 " + testDirectProvisionEd25519Key,
			"",
		}, "\n"),
	)
	if err != nil {
		t.Fatalf("selectSSHProvisionDirectHostKeyLine() error = %v", err)
	}
	want := "magic-kingdom ssh-ed25519 " + testDirectProvisionEd25519Key
	if line != want {
		t.Fatalf("selected host key line = %q, want %q", line, want)
	}
}

func TestRunSSHProvisionDirectFailsClosedWhenConfigV2WritesDisabled(t *testing.T) {
	t.Parallel()

	runner := &fakeDirectProvisionRunner{}

	err := runSSHProvisionDirect([]string{
		"--trust-keyscan",
		"--regular-known-hosts", "/Users/jesse/.ssh/known_hosts",
		"--host", "id=mac-a,ssh=mac-a.tailnet,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519",
		"--host", "id=linux-b,ssh=linux-b.tailnet,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/.config/clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519",
	}, &bytes.Buffer{}, &bytes.Buffer{}, sshProvisionDirectOptions{
		Runner:            runner,
		ConfigV2WriteGate: func() bool { return false },
	})
	if !errors.Is(err, config.ErrConfigV2WritesDisabled) {
		t.Fatalf("runSSHProvisionDirect() error = %v, want ErrConfigV2WritesDisabled", err)
	}
	if len(runner.keyscans) != 0 {
		t.Fatalf("keyscans = %#v, want none", runner.keyscans)
	}
	if len(runner.configApplies) != 0 {
		t.Fatalf("config applies = %#v, want none", runner.configApplies)
	}
}

func TestRunSSHProvisionDirectRejectsInvalidLocalSharedKeyBeforeKeyscan(t *testing.T) {
	t.Parallel()

	runner := &fakeDirectProvisionRunner{}

	err := runSSHProvisionDirect([]string{
		"--trust-keyscan",
		"--regular-known-hosts", "/Users/jesse/.ssh/known_hosts",
		"--host", "id=mac-a,ssh=mac-a.tailnet,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519",
		"--host", "id=linux-b,ssh=linux-b.tailnet,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/Application Support/Clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519",
	}, &bytes.Buffer{}, &bytes.Buffer{}, sshProvisionDirectOptions{
		Runner:            runner,
		ConfigV2WriteGate: func() bool { return true },
		SharedKey:         func() (string, error) { return "not-base64", nil },
	})
	if err == nil || err.Error() != "invalid_local_shared_key" {
		t.Fatalf("runSSHProvisionDirect() error = %v, want invalid_local_shared_key", err)
	}
	if len(runner.keyscans) != 0 {
		t.Fatalf("keyscans = %#v, want none", runner.keyscans)
	}
	if len(runner.configApplies) != 0 {
		t.Fatalf("config applies = %#v, want none", runner.configApplies)
	}
}

func TestRunSSHProvisionDirectRequiresExplicitKeyscanTrust(t *testing.T) {
	t.Parallel()

	err := runSSHProvisionDirect([]string{
		"--host", "id=mac-a,ssh=mac-a.tailnet,user=jesse,port=22,install=/Users/jesse/.local/bin/clipfan,config=/Users/jesse/.config/clipfan/config.json,known_hosts=/Users/jesse/.config/clipfan/ssh/known_hosts,sync_key=/Users/jesse/.config/clipfan/ssh/sync_ed25519",
		"--host", "id=linux-b,ssh=linux-b.tailnet,user=jesse,port=22,install=/home/jesse/.local/bin/clipfan,config=/home/jesse/.config/clipfan/config.json,known_hosts=/home/jesse/.config/clipfan/ssh/known_hosts,sync_key=/home/jesse/.config/clipfan/ssh/sync_ed25519",
	}, &bytes.Buffer{}, &bytes.Buffer{}, sshProvisionDirectOptions{
		Runner:            &fakeDirectProvisionRunner{},
		ConfigV2WriteGate: func() bool { return true },
	})
	if err == nil || !strings.Contains(err.Error(), "trust_keyscan_required") {
		t.Fatalf("runSSHProvisionDirect() error = %v, want trust_keyscan_required", err)
	}
}

func TestRunSSHApplyDirectConfigAppliesTargetHostOnly(t *testing.T) {
	t.Parallel()

	plan, err := sshprovision.BuildDirectPairPlan(sshprovision.DirectPairPlanInput{
		Local: sshprovision.DirectPairHost{
			ID:          "mac-a",
			SSHHost:     "mac-a.tailnet",
			SSHUser:     "jesse",
			SSHPort:     22,
			InstallPath: "/Users/jesse/.local/bin/clipfan",
			GatewayPath: "/Users/jesse/.local/bin/clipfan",
		},
		Remote: sshprovision.DirectPairHost{
			ID:          "linux-b",
			SSHHost:     "linux-b.tailnet",
			SSHUser:     "jesse",
			SSHPort:     22,
			InstallPath: "/home/jesse/.local/bin/clipfan",
			GatewayPath: "/home/jesse/.local/bin/clipfan",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := SSHApplyDirectConfigPayload{
		HostID:     "linux-b",
		ConfigPath: writeDirectApplyConfigForTest(t, "linux-b"),
		Phase:      "stage",
		Mutation: sshprovision.DirectPairConfigMutation{
			Plan:   plan,
			Writes: append([]sshprovision.DirectPairConfigWrite(nil), plan.ConfigWrites...),
			SyncKeys: map[string]sshprovision.SyncKeyMaterial{
				"linux-b": {PrivateKeyPath: "/home/jesse/.config/clipfan/ssh/sync_ed25519", PublicKey: testDirectProvisionEd25519Key, KeyID: testDirectProvisionEd25519KeyID},
				"mac-a":   {PrivateKeyPath: "/Users/jesse/.config/clipfan/ssh/sync_ed25519", PublicKey: testDirectProvisionOtherEd25519Key, KeyID: "1892e27b582e5293"},
			},
			SharedKey: testDirectProvisionSharedKey,
			KnownHostsPaths: map[string]string{
				"linux-b": "/home/jesse/.config/clipfan/ssh/known_hosts",
				"mac-a":   "/Users/jesse/.config/clipfan/ssh/known_hosts",
			},
		},
	}
	encoded, err := encodeSSHApplyDirectConfigPayloadForTest(payload)
	if err != nil {
		t.Fatal(err)
	}
	ops := newFakeDirectProvisionConfigOps()
	var stdout bytes.Buffer
	if err := runSSHApplyDirectConfigWithStdin([]string{"--payload-stdin"}, strings.NewReader(encoded), &stdout, &bytes.Buffer{}, ops); err != nil {
		t.Fatalf("runSSHApplyDirectConfig() error = %v", err)
	}
	configPath := payload.ConfigPath
	wantCalls := []string{
		"read:" + configPath,
		"local:" + configPath,
		"upsert:" + configPath + ":mac-a",
		"proof:" + configPath + ":mac-a",
		"transition:" + configPath + ":mac-a:loopback_unprovisioned->ssh_material_staged",
	}
	if !reflect.DeepEqual(ops.calls, wantCalls) {
		t.Fatalf("calls:\n got %#v\nwant %#v", ops.calls, wantCalls)
	}

	payload.Phase = "ready"
	encoded, err = encodeSSHApplyDirectConfigPayloadForTest(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := runSSHApplyDirectConfigWithStdin([]string{"--payload-stdin"}, strings.NewReader(encoded), &stdout, &bytes.Buffer{}, ops); err != nil {
		t.Fatalf("ready runSSHApplyDirectConfig() error = %v", err)
	}
	if got := ops.calls[len(ops.calls)-2:]; !reflect.DeepEqual(got, []string{
		"read:" + configPath,
		"transition:" + configPath + ":mac-a:ssh_material_staged->ssh_keys_ready",
	}) {
		t.Fatalf("ready calls:\n got %#v", got)
	}
}

func TestRunSSHApplyDirectConfigRejectsPayloadBase64Argv(t *testing.T) {
	t.Parallel()

	err := runSSHApplyDirectConfigWithStdin([]string{"--payload-base64", "secret-bearing-payload"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, newFakeDirectProvisionConfigOps())
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("runSSHApplyDirectConfigWithStdin() error = %v, want undefined payload-base64 flag", err)
	}
}

func TestRunSSHApplyDirectConfigReadsPayloadFromStdin(t *testing.T) {
	t.Parallel()

	plan, err := sshprovision.BuildDirectPairPlan(sshprovision.DirectPairPlanInput{
		Local: sshprovision.DirectPairHost{
			ID:          "mac-a",
			SSHHost:     "mac-a.tailnet",
			SSHUser:     "jesse",
			SSHPort:     22,
			InstallPath: "/Users/jesse/.local/bin/clipfan",
			GatewayPath: "/Users/jesse/.local/bin/clipfan",
		},
		Remote: sshprovision.DirectPairHost{
			ID:          "linux-b",
			SSHHost:     "linux-b.tailnet",
			SSHUser:     "jesse",
			SSHPort:     22,
			InstallPath: "/home/jesse/.local/bin/clipfan",
			GatewayPath: "/home/jesse/.local/bin/clipfan",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	configPath := writeDirectApplyConfigForTest(t, "linux-b")
	payload := SSHApplyDirectConfigPayload{
		HostID:     "linux-b",
		ConfigPath: configPath,
		Phase:      "stage",
		Mutation: sshprovision.DirectPairConfigMutation{
			Plan:      plan,
			Writes:    append([]sshprovision.DirectPairConfigWrite(nil), plan.ConfigWrites...),
			SharedKey: testDirectProvisionSharedKey,
			SyncKeys: map[string]sshprovision.SyncKeyMaterial{
				"linux-b": {PrivateKeyPath: "/home/jesse/.config/clipfan/ssh/sync_ed25519", PublicKey: testDirectProvisionEd25519Key, KeyID: testDirectProvisionEd25519KeyID},
				"mac-a":   {PrivateKeyPath: "/Users/jesse/.config/clipfan/ssh/sync_ed25519", PublicKey: testDirectProvisionOtherEd25519Key, KeyID: "1892e27b582e5293"},
			},
			KnownHostsPaths: map[string]string{
				"linux-b": "/home/jesse/.config/clipfan/ssh/known_hosts",
				"mac-a":   "/Users/jesse/.config/clipfan/ssh/known_hosts",
			},
		},
	}
	encoded, err := encodeSSHApplyDirectConfigPayloadForTest(payload)
	if err != nil {
		t.Fatal(err)
	}
	ops := newFakeDirectProvisionConfigOps()
	var stdout bytes.Buffer
	if err := runSSHApplyDirectConfigWithStdin([]string{"--payload-stdin"}, strings.NewReader(encoded+"\n"), &stdout, &bytes.Buffer{}, ops); err != nil {
		t.Fatalf("runSSHApplyDirectConfigWithStdin() error = %v", err)
	}
	wantCalls := []string{
		"read:" + configPath,
		"local:" + configPath,
		"upsert:" + configPath + ":mac-a",
		"proof:" + configPath + ":mac-a",
		"transition:" + configPath + ":mac-a:loopback_unprovisioned->ssh_material_staged",
	}
	if !reflect.DeepEqual(ops.calls, wantCalls) {
		t.Fatalf("calls:\n got %#v\nwant %#v", ops.calls, wantCalls)
	}
}

func TestRunSSHApplyDirectConfigMigratesExistingStaticConfigOnDisk(t *testing.T) {
	if !releaseflags.ConfigV2WriteEnabled {
		t.Skip("requires generated ConfigV2WriteEnabled=true profile")
	}

	for _, tc := range []struct {
		name string
		body string
	}{
		{
			name: "pre-v2 missing host id",
			body: `{"shared_key":"k","discovery":"static","static_peers":["old"],"future_top":{"keep":true}}`,
		},
		{
			name: "v2 with stale static peers",
			body: `{"config_version":2,"config_revision":7,"shared_key":"k","hostname":"linux-b","discovery":"tailscale","static_peers":["old"],"future_top":{"keep":true}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configPath := writeDirectApplyConfigBodyForTest(t, tc.body)
			payload := directApplyPayloadForTest(t, "linux-b", configPath, "stage")
			encoded, err := encodeSSHApplyDirectConfigPayloadForTest(payload)
			if err != nil {
				t.Fatal(err)
			}
			var stdout bytes.Buffer
			if err := runSSHApplyDirectConfigWithStdin([]string{"--payload-stdin"}, strings.NewReader(encoded), &stdout, &bytes.Buffer{}, nil); err != nil {
				t.Fatalf("runSSHApplyDirectConfigWithStdin() error = %v", err)
			}

			after := readCLIJSONMap(t, configPath)
			assertDirectApplyMigratedSSHConfig(t, after, "linux-b", "mac-a")
			if !reflect.DeepEqual(after["future_top"], map[string]any{"keep": true}) {
				t.Fatalf("future_top = %#v, want preserved", after["future_top"])
			}
		})
	}
}

func TestRunSSHApplyDirectConfigRejectsHostMismatchBeforeMutation(t *testing.T) {
	t.Parallel()

	plan, err := sshprovision.BuildDirectPairPlan(sshprovision.DirectPairPlanInput{
		Local: sshprovision.DirectPairHost{
			ID:          "mac-a",
			SSHHost:     "mac-a.tailnet",
			SSHUser:     "jesse",
			SSHPort:     22,
			InstallPath: "/Users/jesse/.local/bin/clipfan",
			GatewayPath: "/Users/jesse/.local/bin/clipfan",
		},
		Remote: sshprovision.DirectPairHost{
			ID:          "linux-b",
			SSHHost:     "linux-b.tailnet",
			SSHUser:     "jesse",
			SSHPort:     22,
			InstallPath: "/home/jesse/.local/bin/clipfan",
			GatewayPath: "/home/jesse/.local/bin/clipfan",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := SSHApplyDirectConfigPayload{
		HostID:     "linux-b",
		ConfigPath: writeDirectApplyConfigForTest(t, "mac-a"),
		Phase:      "stage",
		Mutation: sshprovision.DirectPairConfigMutation{
			Plan:   plan,
			Writes: append([]sshprovision.DirectPairConfigWrite(nil), plan.ConfigWrites...),
			SyncKeys: map[string]sshprovision.SyncKeyMaterial{
				"linux-b": {PrivateKeyPath: "/home/jesse/.config/clipfan/ssh/sync_ed25519", PublicKey: testDirectProvisionEd25519Key, KeyID: testDirectProvisionEd25519KeyID},
				"mac-a":   {PrivateKeyPath: "/Users/jesse/.config/clipfan/ssh/sync_ed25519", PublicKey: testDirectProvisionOtherEd25519Key, KeyID: "1892e27b582e5293"},
			},
			SharedKey: testDirectProvisionSharedKey,
			KnownHostsPaths: map[string]string{
				"linux-b": "/home/jesse/.config/clipfan/ssh/known_hosts",
				"mac-a":   "/Users/jesse/.config/clipfan/ssh/known_hosts",
			},
		},
	}
	encoded, err := encodeSSHApplyDirectConfigPayloadForTest(payload)
	if err != nil {
		t.Fatal(err)
	}
	ops := newFakeDirectProvisionConfigOps()
	err = runSSHApplyDirectConfigWithStdin([]string{"--payload-stdin"}, strings.NewReader(encoded), &bytes.Buffer{}, &bytes.Buffer{}, ops)
	if !errors.Is(err, config.ErrHostIDMismatch) {
		t.Fatalf("runSSHApplyDirectConfig() error = %v, want ErrHostIDMismatch", err)
	}
	if len(ops.calls) != 0 {
		t.Fatalf("calls = %#v, want none", ops.calls)
	}
}

type fakeDirectProvisionRunner struct {
	keyscans              []string
	configLookups         []string
	sshConfigOutputs      map[string]string
	regularTargets        []string
	configApplies         []string
	configApplySharedKeys []string
	configPeerEndpoints   []string
}

func (r *fakeDirectProvisionRunner) Run(_ context.Context, command sshprovision.SSHCommand) (sshprovision.CommandOutput, error) {
	if len(command.Args) == 0 {
		return sshprovision.CommandOutput{}, errors.New("empty command")
	}
	if command.Args[0] == "ssh" && len(command.Args) > 1 && command.Args[1] == "-G" {
		host := command.Args[len(command.Args)-1]
		port := "22"
		user := ""
		for i := range command.Args {
			if command.Args[i] == "-p" && i+1 < len(command.Args) {
				port = command.Args[i+1]
			}
			if command.Args[i] == "-l" && i+1 < len(command.Args) {
				user = command.Args[i+1]
			}
		}
		lookup := host
		if user != "" {
			lookup = user + "@" + host
		}
		r.configLookups = append(r.configLookups, "config:"+lookup+":"+port)
		if r.sshConfigOutputs != nil && r.sshConfigOutputs[lookup] != "" {
			return sshprovision.CommandOutput{Stdout: []byte(r.sshConfigOutputs[lookup])}, nil
		}
		if r.sshConfigOutputs != nil && r.sshConfigOutputs[host] != "" {
			return sshprovision.CommandOutput{Stdout: []byte(r.sshConfigOutputs[host])}, nil
		}
		return sshprovision.CommandOutput{Stdout: []byte("hostname " + host + "\nport " + port + "\n")}, nil
	}
	if command.Args[0] == "ssh-keyscan" {
		host := command.Args[len(command.Args)-1]
		port := ""
		for i := range command.Args {
			if command.Args[i] == "-p" && i+1 < len(command.Args) {
				port = command.Args[i+1]
			}
		}
		r.keyscans = append(r.keyscans, "keyscan:"+host+":"+port)
		scanHost := host
		if port != "" && port != "22" {
			scanHost = "[" + host + "]:" + port
		}
		return sshprovision.CommandOutput{Stdout: []byte(scanHost + " ssh-ed25519 " + testDirectProvisionPublicKey(host))}, nil
	}
	remote := command.Args[len(command.Args)-1]
	if len(command.Args) >= 2 {
		r.regularTargets = append(r.regularTargets, command.Args[len(command.Args)-2])
	}
	switch {
	case strings.Contains(remote, "ssh-install-known-host"):
		return sshprovision.CommandOutput{Stdout: []byte(`{"status":"ok"}`)}, nil
	case strings.Contains(remote, "ssh-ensure-sync-key"):
		hostID, keyPath := remoteQuotedArgValue(remote, "--host-id"), remoteQuotedArgValue(remote, "--key-path")
		publicKey := testDirectProvisionPublicKey(hostID + ".key")
		keyID := testDirectProvisionKeyID(hostID)
		return sshprovision.CommandOutput{Stdout: []byte(`{"status":"ok","changed":true,"host_id":"` + hostID + `","key_id":"` + keyID + `","public_key":"` + publicKey + `","private_key_path":"` + keyPath + `"}`)}, nil
	case strings.Contains(remote, "ssh-install-authorized-key"):
		peerID, keyID := remoteQuotedArgValue(remote, "--peer"), remoteQuotedArgValue(remote, "--key-id")
		return sshprovision.CommandOutput{Stdout: []byte(`{"status":"ok","changed":true,"peer_id":"` + peerID + `","key_id":"` + keyID + `"}`)}, nil
	case strings.Contains(remote, "ssh-run-probe"):
		peerID, keyID := remoteQuotedArgValue(remote, "--expect-peer"), remoteQuotedArgValue(remote, "--expect-key-id")
		return sshprovision.CommandOutput{Stdout: []byte(`{"status":"ok","peer_id":"` + peerID + `","key_id":"` + keyID + `"}`)}, nil
	case strings.Contains(remote, "ssh-apply-direct-config"):
		if !strings.Contains(remote, "--payload-stdin") {
			return sshprovision.CommandOutput{}, errors.New("direct config payload not passed over stdin")
		}
		if strings.Contains(remote, "--payload-base64") || strings.Contains(remote, testDirectProvisionSharedKey) {
			return sshprovision.CommandOutput{}, errors.New("direct config secret leaked in remote argv")
		}
		payload := strings.TrimSpace(string(command.Stdin))
		if payload == "" {
			return sshprovision.CommandOutput{}, errors.New("missing direct config stdin payload")
		}
		var decoded struct {
			HostID     string `json:"host_id"`
			ConfigPath string `json:"config_path"`
			Phase      string `json:"phase"`
			Mutation   struct {
				SharedKey string `json:"shared_key"`
				Writes    []struct {
					PeerID  string
					SSHHost string
					SSHPort int
				} `json:"writes"`
			} `json:"mutation"`
		}
		data, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return sshprovision.CommandOutput{}, err
		}
		if err := json.Unmarshal(data, &decoded); err != nil {
			return sshprovision.CommandOutput{}, err
		}
		if decoded.HostID == "linux-b" && decoded.ConfigPath != "/home/jesse/Application Support/Clipfan/config.json" {
			return sshprovision.CommandOutput{}, errors.New("linux-b config path lost: " + decoded.ConfigPath)
		}
		r.configApplies = append(r.configApplies, decoded.HostID+":"+decoded.Phase)
		r.configApplySharedKeys = append(r.configApplySharedKeys, decoded.Mutation.SharedKey)
		for _, write := range decoded.Mutation.Writes {
			r.configPeerEndpoints = append(r.configPeerEndpoints, write.PeerID+"="+write.SSHHost+":"+fmt.Sprint(write.SSHPort))
		}
		return sshprovision.CommandOutput{Stdout: []byte(`{"status":"ok"}`)}, nil
	default:
		return sshprovision.CommandOutput{}, errors.New("unexpected command: " + strings.Join(command.Args, " "))
	}
}

type fakeDirectProvisionConfigOps struct {
	revisions          map[string]uint64
	calls              []string
	transitionsToReady []string
}

func newFakeDirectProvisionConfigOps() *fakeDirectProvisionConfigOps {
	return &fakeDirectProvisionConfigOps{revisions: map[string]uint64{}}
}

func (f *fakeDirectProvisionConfigOps) ReadConfigRevision(path string) (config.ConfigRevisionStatus, error) {
	f.calls = append(f.calls, "read:"+path)
	if f.revisions[path] == 0 {
		f.revisions[path] = 7
	}
	return f.status(path), nil
}

func (f *fakeDirectProvisionConfigOps) EnsureConfigV2Revision(path string, status config.ConfigRevisionStatus) (config.ConfigRevisionStatus, error) {
	f.calls = append(f.calls, "ensure:"+path)
	f.revisions[path] = 1
	return f.status(path), nil
}

func (f *fakeDirectProvisionConfigOps) UpdateSSHLocalMaterial(path string, req config.SSHLocalMaterialUpdateRequest) (config.ConfigRevisionStatus, error) {
	f.calls = append(f.calls, "local:"+path)
	return f.bump(path), nil
}

func (f *fakeDirectProvisionConfigOps) UpsertSSHPeer(path string, peerID string, req config.SSHPeerUpsertRequest) (config.SSHPeerConfigReadResult, error) {
	f.calls = append(f.calls, "upsert:"+path+":"+peerID)
	status := f.bump(path)
	return config.SSHPeerConfigReadResult{ConfigVersion: status.ConfigVersion, ConfigRevision: status.ConfigRevision, RevisionState: status.RevisionState}, nil
}

func (f *fakeDirectProvisionConfigOps) PatchSSHPeerProof(path string, peerID string, req config.SSHPeerProofPatchRequest) (config.SSHPeerConfigReadResult, error) {
	f.calls = append(f.calls, "proof:"+path+":"+peerID)
	status := f.bump(path)
	return config.SSHPeerConfigReadResult{ConfigVersion: status.ConfigVersion, ConfigRevision: status.ConfigRevision, RevisionState: status.RevisionState}, nil
}

func (f *fakeDirectProvisionConfigOps) TransitionSSHPeer(path string, peerID string, req config.SSHPeerTransitionRequest) (config.SSHPeerConfigReadResult, error) {
	f.calls = append(f.calls, "transition:"+path+":"+peerID+":"+string(req.FromState)+"->"+string(req.ToState))
	if req.ToState == config.MigrationStateSSHKeysReady {
		f.transitionsToReady = append(f.transitionsToReady, path+":"+peerID)
	}
	status := f.bump(path)
	return config.SSHPeerConfigReadResult{ConfigVersion: status.ConfigVersion, ConfigRevision: status.ConfigRevision, RevisionState: status.RevisionState}, nil
}

func (f *fakeDirectProvisionConfigOps) status(path string) config.ConfigRevisionStatus {
	version := 2
	revision := f.revisions[path]
	return config.ConfigRevisionStatus{ConfigVersion: &version, ConfigRevision: &revision, RevisionState: config.RevisionStateVersioned}
}

func (f *fakeDirectProvisionConfigOps) bump(path string) config.ConfigRevisionStatus {
	if f.revisions[path] == 0 {
		f.revisions[path] = 7
	}
	f.revisions[path]++
	return f.status(path)
}

func remoteQuotedArgValue(command string, flag string) string {
	needle := "'" + flag + "' '"
	start := strings.Index(command, needle)
	if start < 0 {
		return ""
	}
	start += len(needle)
	end := strings.Index(command[start:], "'")
	if end < 0 {
		return ""
	}
	return command[start : start+end]
}

func testDirectProvisionPublicKey(seed string) string {
	switch seed {
	case "mac-a.key":
		return testDirectProvisionOtherEd25519Key
	case "linux-c.key":
		return testDirectProvisionThirdEd25519Key
	default:
		return testDirectProvisionEd25519Key
	}
}

func testDirectProvisionKeyID(hostID string) string {
	switch hostID {
	case "mac-a":
		return "1892e27b582e5293"
	case "linux-c":
		return "8f9ff3acc94ccf7b"
	default:
		return testDirectProvisionEd25519KeyID
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func encodeSSHApplyDirectConfigPayloadForTest(payload SSHApplyDirectConfigPayload) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func directApplyPayloadForTest(t *testing.T, hostID string, configPath string, phase string) SSHApplyDirectConfigPayload {
	t.Helper()
	plan, err := sshprovision.BuildDirectPairPlan(sshprovision.DirectPairPlanInput{
		Local: sshprovision.DirectPairHost{
			ID:          "mac-a",
			SSHHost:     "mac-a.tailnet",
			SSHUser:     "jesse",
			SSHPort:     22,
			InstallPath: "/Users/jesse/.local/bin/clipfan",
			GatewayPath: "/Users/jesse/.local/bin/clipfan",
		},
		Remote: sshprovision.DirectPairHost{
			ID:          "linux-b",
			SSHHost:     "linux-b.tailnet",
			SSHUser:     "jesse",
			SSHPort:     22,
			InstallPath: "/home/jesse/.local/bin/clipfan",
			GatewayPath: "/home/jesse/.local/bin/clipfan",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return SSHApplyDirectConfigPayload{
		HostID:     hostID,
		ConfigPath: configPath,
		Phase:      phase,
		Mutation: sshprovision.DirectPairConfigMutation{
			Plan:      plan,
			Writes:    append([]sshprovision.DirectPairConfigWrite(nil), plan.ConfigWrites...),
			SharedKey: testDirectProvisionSharedKey,
			SyncKeys: map[string]sshprovision.SyncKeyMaterial{
				"linux-b": {PrivateKeyPath: "/home/jesse/.config/clipfan/ssh/sync_ed25519", PublicKey: testDirectProvisionEd25519Key, KeyID: testDirectProvisionEd25519KeyID},
				"mac-a":   {PrivateKeyPath: "/Users/jesse/.config/clipfan/ssh/sync_ed25519", PublicKey: testDirectProvisionOtherEd25519Key, KeyID: "1892e27b582e5293"},
			},
			KnownHostsPaths: map[string]string{
				"linux-b": "/home/jesse/.config/clipfan/ssh/known_hosts",
				"mac-a":   "/Users/jesse/.config/clipfan/ssh/known_hosts",
			},
		},
	}
}

func writeDirectApplyConfigForTest(t *testing.T, hostID string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Clipfan Config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	body := `{"config_version":2,"config_revision":7,"shared_key":"k","hostname":"` + hostID + `","transport":"ssh","max_history":50}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeDirectApplyConfigBodyForTest(t *testing.T, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Clipfan Config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertDirectApplyMigratedSSHConfig(t *testing.T, after map[string]any, hostID string, peerID string) {
	t.Helper()
	if after["config_version"] != float64(2) {
		t.Fatalf("config_version = %#v, want 2", after["config_version"])
	}
	if _, ok := after["config_revision"].(float64); !ok {
		t.Fatalf("config_revision = %#v, want number", after["config_revision"])
	}
	if after["hostname"] != hostID {
		t.Fatalf("hostname = %#v, want %s", after["hostname"], hostID)
	}
	if after["transport"] != config.TransportSSH {
		t.Fatalf("transport = %#v, want ssh", after["transport"])
	}
	if after["discovery"] != "static" {
		t.Fatalf("discovery = %#v, want static", after["discovery"])
	}
	if _, ok := after["static_peers"]; ok {
		t.Fatalf("static_peers survived direct apply migration: %#v", after["static_peers"])
	}
	ssh, ok := after["ssh"].(map[string]any)
	if !ok {
		t.Fatalf("ssh = %#v, want object", after["ssh"])
	}
	if ssh["sync_key"] == "" || ssh["known_hosts"] == "" {
		t.Fatalf("ssh local material missing: %#v", ssh)
	}
	peer := directApplySSHPeerByID(t, ssh, peerID)
	if peer["migration_state"] != string(config.MigrationStateSSHMaterialStaged) {
		t.Fatalf("peer migration_state = %#v, want staged", peer["migration_state"])
	}
}

func directApplySSHPeerByID(t *testing.T, ssh map[string]any, peerID string) map[string]any {
	t.Helper()
	peers, ok := ssh["peers"].([]any)
	if !ok {
		t.Fatalf("ssh.peers = %#v, want array", ssh["peers"])
	}
	for _, item := range peers {
		peer, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("ssh peer = %#v, want object", item)
		}
		if peer["id"] == peerID {
			return peer
		}
	}
	t.Fatalf("peer %s missing in %#v", peerID, peers)
	return nil
}
