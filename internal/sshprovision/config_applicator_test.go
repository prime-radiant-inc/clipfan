package sshprovision

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
)

var testDirectPairSharedKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))

func TestDirectPairConfigApplicatorAppliesRevisionOrderedConfigMutations(t *testing.T) {
	t.Parallel()

	ops := newFakeDirectPairConfigOps()
	applicator := DirectPairConfigApplicator{
		ConfigPathByHostID: map[string]string{
			"linux-b": "/configs/linux-b.json",
			"mac-a":   "/configs/mac-a.json",
		},
		Ops:   ops,
		Now:   func() time.Time { return time.Date(2026, 6, 2, 12, 34, 56, 0, time.UTC) },
		LogID: func(DirectPairConfigMutation) string { return "log-1" },
	}

	err := applicator.Apply(context.Background(), validDirectPairConfigMutation(t))
	if err != nil {
		t.Fatalf("Apply error = %v", err)
	}

	wantCalls := []string{
		"read:/configs/linux-b.json",
		"read:/configs/mac-a.json",
		"local:/configs/linux-b.json:rev=7:transport=ssh:shared_key_set=true:sync=/home/jesse/.config/clipfan/ssh/sync_ed25519:known=/home/jesse/.config/clipfan/ssh/known_hosts",
		"local:/configs/mac-a.json:rev=7:transport=ssh:shared_key_set=true:sync=/Users/jesse/.config/clipfan/ssh/sync_ed25519:known=/Users/jesse/.config/clipfan/ssh/known_hosts",
		"upsert:/configs/linux-b.json:mac-a:rev=8:enabled=true:accept=true:connect=true:persistent=true:on_demand=false:shared_key_nil=true",
		"upsert:/configs/mac-a.json:linux-b:rev=8:enabled=true:accept=true:connect=true:persistent=true:on_demand=false:shared_key_nil=true",
		"proof:/configs/linux-b.json:mac-a:rev=9:accept=true:connect=true:accept_key=mac-key-123456:connect_key=linux-key-123456:verified=regular_ssh",
		"proof:/configs/mac-a.json:linux-b:rev=9:accept=true:connect=true:accept_key=linux-key-123456:connect_key=mac-key-123456:verified=regular_ssh",
		"transition:/configs/linux-b.json:mac-a:rev=10:loopback_unprovisioned->ssh_material_staged:material_staged:log-1",
		"transition:/configs/mac-a.json:linux-b:rev=10:loopback_unprovisioned->ssh_material_staged:material_staged:log-1",
		"transition:/configs/linux-b.json:mac-a:rev=11:ssh_material_staged->ssh_keys_ready:ssh_material_verified:log-1",
		"transition:/configs/mac-a.json:linux-b:rev=11:ssh_material_staged->ssh_keys_ready:ssh_material_verified:log-1",
	}
	if !reflect.DeepEqual(ops.calls, wantCalls) {
		t.Fatalf("calls:\n got %#v\nwant %#v", ops.calls, wantCalls)
	}
}

func TestDirectPairConfigApplicatorEnsuresRevisionForPreV2Configs(t *testing.T) {
	t.Parallel()

	ops := newFakeDirectPairConfigOps()
	ops.revisionStates["/configs/linux-b.json"] = config.RevisionStatePreV2
	applicator := DirectPairConfigApplicator{}

	revisions, err := applicator.readRevisions(map[string]string{
		"linux-b": "/configs/linux-b.json",
		"mac-a":   "/configs/mac-a.json",
	}, ops)
	if err != nil {
		t.Fatalf("readRevisions error = %v", err)
	}
	if revisions["linux-b"] != 1 || revisions["mac-a"] != 7 {
		t.Fatalf("revisions = %#v, want linux-b=1 mac-a=7", revisions)
	}
	wantCalls := []string{
		"read:/configs/linux-b.json",
		"ensure:/configs/linux-b.json:state=pre_v2:rev=<nil>",
		"read:/configs/mac-a.json",
	}
	if !reflect.DeepEqual(ops.calls, wantCalls) {
		t.Fatalf("calls:\n got %#v\nwant %#v", ops.calls, wantCalls)
	}
}

func TestDirectPairConfigApplicatorRejectsInvalidSharedKeyBeforeMutation(t *testing.T) {
	t.Parallel()

	ops := newFakeDirectPairConfigOps()
	applicator := DirectPairConfigApplicator{
		ConfigPathByHostID: map[string]string{
			"linux-b": "/configs/linux-b.json",
			"mac-a":   "/configs/mac-a.json",
		},
		Ops: ops,
	}
	mutation := validDirectPairConfigMutation(t)
	mutation.SharedKey = "not-base64"

	err := applicator.Apply(context.Background(), mutation)
	if err == nil || err.Error() != "invalid_shared_key" {
		t.Fatalf("Apply error = %v, want invalid_shared_key", err)
	}
	if len(ops.calls) != 0 {
		t.Fatalf("calls = %#v, want none", ops.calls)
	}
}

func TestDirectPairConfigApplicatorRejectsMissingSharedKeyBeforeMutation(t *testing.T) {
	t.Parallel()

	ops := newFakeDirectPairConfigOps()
	applicator := DirectPairConfigApplicator{
		ConfigPathByHostID: map[string]string{
			"linux-b": "/configs/linux-b.json",
			"mac-a":   "/configs/mac-a.json",
		},
		Ops: ops,
	}
	mutation := validDirectPairConfigMutation(t)
	mutation.SharedKey = ""

	err := applicator.Apply(context.Background(), mutation)
	if err == nil || err.Error() != "invalid_shared_key" {
		t.Fatalf("Apply error = %v, want invalid_shared_key", err)
	}
	if len(ops.calls) != 0 {
		t.Fatalf("calls = %#v, want none", ops.calls)
	}
}

func TestDirectPairConfigApplicatorRejectsMissingConfigPath(t *testing.T) {
	t.Parallel()

	ops := newFakeDirectPairConfigOps()
	applicator := DirectPairConfigApplicator{
		ConfigPathByHostID: map[string]string{"linux-b": "/configs/linux-b.json"},
		Ops:                ops,
	}

	err := applicator.Apply(context.Background(), validDirectPairConfigMutation(t))
	if !errors.Is(err, ErrDirectPairConfigPathMissing) {
		t.Fatalf("Apply error = %v, want ErrDirectPairConfigPathMissing", err)
	}
	if len(ops.calls) != 0 {
		t.Fatalf("calls = %#v, want none", ops.calls)
	}
}

func TestDirectPairConfigApplicatorPreflightsPeerMaterialBeforeMutation(t *testing.T) {
	t.Parallel()

	ops := newFakeDirectPairConfigOps()
	applicator := DirectPairConfigApplicator{
		ConfigPathByHostID: map[string]string{
			"linux-b": "/configs/linux-b.json",
		},
		TargetHostIDs: []string{"linux-b"},
		Phase:         DirectPairConfigApplyStage,
		Ops:           ops,
	}
	mutation := validDirectPairConfigMutation(t)
	delete(mutation.SyncKeys, "mac-a")

	err := applicator.Apply(context.Background(), mutation)
	if err == nil || !strings.Contains(err.Error(), "missing_sync_key_material: mac-a") {
		t.Fatalf("Apply error = %v, want missing mac-a sync key", err)
	}
	if len(ops.calls) != 0 {
		t.Fatalf("calls = %#v, want none", ops.calls)
	}
}

func TestDirectPairConfigApplicatorDefaultOpsFailClosedWithGeneratedGate(t *testing.T) {
	if releaseflags.ConfigV2WriteEnabled {
		t.Skip("requires public generated ConfigV2WriteEnabled=false profile")
	}

	linuxPath := writeSSHProvisionConfigForTest(t, `{"config_version":2,"config_revision":7,"shared_key":"k","hostname":"linux-b","transport":"ssh","max_history":50}`)
	macPath := writeSSHProvisionConfigForTest(t, `{"config_version":2,"config_revision":7,"shared_key":"k","hostname":"mac-a","transport":"ssh","max_history":50}`)
	beforeLinux := readSSHProvisionJSONMap(t, linuxPath)
	beforeMac := readSSHProvisionJSONMap(t, macPath)
	applicator := DirectPairConfigApplicator{
		ConfigPathByHostID: map[string]string{
			"linux-b": linuxPath,
			"mac-a":   macPath,
		},
	}

	err := applicator.Apply(context.Background(), validDirectPairConfigMutation(t))
	if !errors.Is(err, config.ErrConfigV2WritesDisabled) {
		t.Fatalf("Apply error = %v, want ErrConfigV2WritesDisabled", err)
	}
	assertSSHProvisionJSONValueEqual(t, beforeLinux, readSSHProvisionJSONMap(t, linuxPath))
	assertSSHProvisionJSONValueEqual(t, beforeMac, readSSHProvisionJSONMap(t, macPath))
}

type fakeDirectPairConfigOps struct {
	revisions      map[string]uint64
	revisionStates map[string]config.RevisionState
	calls          []string
}

func newFakeDirectPairConfigOps() *fakeDirectPairConfigOps {
	return &fakeDirectPairConfigOps{
		revisions: map[string]uint64{
			"/configs/linux-b.json": 7,
			"/configs/mac-a.json":   7,
		},
		revisionStates: map[string]config.RevisionState{},
	}
}

func (f *fakeDirectPairConfigOps) ReadConfigRevision(path string) (config.ConfigRevisionStatus, error) {
	f.calls = append(f.calls, "read:"+path)
	return f.status(path), nil
}

func (f *fakeDirectPairConfigOps) EnsureConfigV2Revision(path string, status config.ConfigRevisionStatus) (config.ConfigRevisionStatus, error) {
	f.calls = append(f.calls, "ensure:"+path+":state="+string(status.RevisionState)+":rev="+revString(status.ConfigRevision))
	f.revisionStates[path] = config.RevisionStateVersioned
	f.revisions[path] = 1
	return f.status(path), nil
}

func (f *fakeDirectPairConfigOps) UpdateSSHLocalMaterial(path string, req config.SSHLocalMaterialUpdateRequest) (config.ConfigRevisionStatus, error) {
	f.calls = append(f.calls, "local:"+path+":rev="+revString(req.ExpectedConfigRevision)+":transport="+stringValue(req.Transport)+":shared_key_set="+boolString(req.SharedKey != nil)+":sync="+stringValue(req.SyncKey)+":known="+stringValue(req.KnownHosts))
	return f.bump(path), nil
}

func (f *fakeDirectPairConfigOps) UpsertSSHPeer(path string, peerID string, req config.SSHPeerUpsertRequest) (config.SSHPeerConfigReadResult, error) {
	f.calls = append(f.calls, "upsert:"+path+":"+peerID+":rev="+revString(req.ExpectedConfigRevision)+":enabled="+boolValue(req.Peer.Enabled)+":accept="+boolValue(req.Peer.Accept)+":connect="+boolValue(req.Peer.Connect)+":persistent="+boolValue(req.Peer.Persistent)+":on_demand="+boolValue(req.Peer.OnDemand)+":shared_key_nil="+boolString(req.Peer.SharedKey == nil))
	status := f.bump(path)
	return config.SSHPeerConfigReadResult{ConfigVersion: status.ConfigVersion, ConfigRevision: status.ConfigRevision, RevisionState: status.RevisionState}, nil
}

func (f *fakeDirectPairConfigOps) PatchSSHPeerProof(path string, peerID string, req config.SSHPeerProofPatchRequest) (config.SSHPeerConfigReadResult, error) {
	acceptKeyID := ""
	connectKeyID := ""
	verifiedBy := ""
	if req.ConnectProof != nil {
		connectKeyID = req.ConnectProof.KeyID
		verifiedBy = req.ConnectProof.VerifiedBy
	}
	if req.AcceptProof != nil {
		acceptKeyID = req.AcceptProof.KeyID
		verifiedBy = req.AcceptProof.VerifiedBy
	}
	f.calls = append(f.calls, "proof:"+path+":"+peerID+":rev="+revString(req.ExpectedConfigRevision)+":accept="+boolString(req.AcceptProof != nil)+":connect="+boolString(req.ConnectProof != nil)+":accept_key="+acceptKeyID+":connect_key="+connectKeyID+":verified="+verifiedBy)
	status := f.bump(path)
	return config.SSHPeerConfigReadResult{ConfigVersion: status.ConfigVersion, ConfigRevision: status.ConfigRevision, RevisionState: status.RevisionState}, nil
}

func (f *fakeDirectPairConfigOps) TransitionSSHPeer(path string, peerID string, req config.SSHPeerTransitionRequest) (config.SSHPeerConfigReadResult, error) {
	f.calls = append(f.calls, "transition:"+path+":"+peerID+":rev="+revString(req.ExpectedConfigRevision)+":"+string(req.FromState)+"->"+string(req.ToState)+":"+req.Reason+":"+req.LogID)
	status := f.bump(path)
	return config.SSHPeerConfigReadResult{ConfigVersion: status.ConfigVersion, ConfigRevision: status.ConfigRevision, RevisionState: status.RevisionState}, nil
}

func (f *fakeDirectPairConfigOps) status(path string) config.ConfigRevisionStatus {
	switch f.revisionStates[path] {
	case config.RevisionStatePreV2:
		return config.ConfigRevisionStatus{RevisionState: config.RevisionStatePreV2}
	case config.RevisionStateMissingRevision:
		version := 2
		return config.ConfigRevisionStatus{ConfigVersion: &version, RevisionState: config.RevisionStateMissingRevision}
	}
	revision := f.revisions[path]
	version := 2
	return config.ConfigRevisionStatus{
		ConfigVersion:  &version,
		ConfigRevision: &revision,
		RevisionState:  config.RevisionStateVersioned,
	}
}

func (f *fakeDirectPairConfigOps) bump(path string) config.ConfigRevisionStatus {
	f.revisions[path]++
	return f.status(path)
}

func validDirectPairConfigMutation(t *testing.T) DirectPairConfigMutation {
	t.Helper()
	input := validDirectPairProvisionInput()
	plan, err := BuildDirectPairPlan(DirectPairPlanInput{
		Local:  input.Local.Host,
		Remote: input.Remote.Host,
	})
	if err != nil {
		t.Fatal(err)
	}
	return DirectPairConfigMutation{
		Plan:      plan,
		Writes:    append([]DirectPairConfigWrite(nil), plan.ConfigWrites...),
		SharedKey: testDirectPairSharedKey,
		SyncKeys: map[string]SyncKeyMaterial{
			"linux-b": {PrivateKeyPath: "/home/jesse/.config/clipfan/ssh/sync_ed25519", PublicKey: testEd25519Key, KeyID: "linux-key-123456"},
			"mac-a":   {PrivateKeyPath: "/Users/jesse/.config/clipfan/ssh/sync_ed25519", PublicKey: testEd25519Key, KeyID: "mac-key-123456"},
		},
		KnownHostsPaths: map[string]string{
			"linux-b": "/home/jesse/.config/clipfan/ssh/known_hosts",
			"mac-a":   "/Users/jesse/.config/clipfan/ssh/known_hosts",
		},
	}
}

func writeSSHProvisionConfigForTest(t *testing.T, body string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "clipfan")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readSSHProvisionJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var out map[string]any
	if err := decoder.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func assertSSHProvisionJSONValueEqual(t *testing.T, want, got any) {
	t.Helper()
	wantData, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	gotData, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wantData, gotData) {
		t.Fatalf("json value mismatch\nwant: %s\ngot:  %s", wantData, gotData)
	}
}

func revString(value *uint64) string {
	if value == nil {
		return "<nil>"
	}
	return strconv.FormatUint(*value, 10)
}

func stringValue(value *string) string {
	if value == nil {
		return "<nil>"
	}
	return *value
}

func boolValue(value *bool) string {
	if value == nil {
		return "<nil>"
	}
	return boolString(*value)
}
