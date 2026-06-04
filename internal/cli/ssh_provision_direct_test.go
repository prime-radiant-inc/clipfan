package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

const (
	testDirectProvisionEd25519Key      = "AAAAC3NzaC1lZDI1NTE5AAAAIC6JxQKUfHw2JMc2+5ZUTc5xI8QX1sGm8c5C7h4eY7p1"
	testDirectProvisionOtherEd25519Key = "AAAAC3NzaC1lZDI1NTE5AAAAIHP7O1LPaDr6RfFdqHtKc9m8gw98RK54GpcfwoAK2JhH"
	testDirectProvisionThirdEd25519Key = "AAAAC3NzaC1lZDI1NTE5AAAAIHRoaXJkLXRlc3QtZWQyNTUxOS1wdWJsaWMta2V5ISEh"
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
	if err := runSSHApplyDirectConfig([]string{"--payload-base64", encoded}, &stdout, &bytes.Buffer{}, ops); err != nil {
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
	if err := runSSHApplyDirectConfig([]string{"--payload-base64", encoded}, &stdout, &bytes.Buffer{}, ops); err != nil {
		t.Fatalf("ready runSSHApplyDirectConfig() error = %v", err)
	}
	if got := ops.calls[len(ops.calls)-2:]; !reflect.DeepEqual(got, []string{
		"read:" + configPath,
		"transition:" + configPath + ":mac-a:ssh_material_staged->ssh_keys_ready",
	}) {
		t.Fatalf("ready calls:\n got %#v", got)
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
	err = runSSHApplyDirectConfig([]string{"--payload-base64", encoded}, &bytes.Buffer{}, &bytes.Buffer{}, ops)
	if !errors.Is(err, config.ErrHostIDMismatch) {
		t.Fatalf("runSSHApplyDirectConfig() error = %v, want ErrHostIDMismatch", err)
	}
	if len(ops.calls) != 0 {
		t.Fatalf("calls = %#v, want none", ops.calls)
	}
}

type fakeDirectProvisionRunner struct {
	keyscans              []string
	configApplies         []string
	configApplySharedKeys []string
}

func (r *fakeDirectProvisionRunner) Run(_ context.Context, command sshprovision.SSHCommand) (sshprovision.CommandOutput, error) {
	if len(command.Args) == 0 {
		return sshprovision.CommandOutput{}, errors.New("empty command")
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
		return sshprovision.CommandOutput{Stdout: []byte(host + " ssh-ed25519 " + testDirectProvisionPublicKey(host))}, nil
	}
	remote := command.Args[len(command.Args)-1]
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

func encodeSSHApplyDirectConfigPayloadForTest(payload SSHApplyDirectConfigPayload) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
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
