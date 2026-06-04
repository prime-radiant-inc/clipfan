package sshprovision

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

func TestDirectPairProvisionerRunsMaterialAndProbeBeforeConfigWrites(t *testing.T) {
	t.Parallel()

	fake := newFakeDirectPairDriver()
	fake.configWritesEnabled = true
	provisioner := DirectPairProvisioner{Driver: fake, configV2WriteGate: func() bool { return true }}

	result, err := provisioner.Provision(context.Background(), validDirectPairProvisionInput())
	if err != nil {
		t.Fatalf("Provision() error = %v", err)
	}

	wantOps := []string{
		"confirm:mac-a",
		"pin:linux-b:mac-a:mac-a.tailnet ssh-ed25519 " + testEd25519Key,
		"key:linux-b",
		"auth:mac-a:linux-b:key-123456",
		"probe-from:linux-b:jesse@mac-a.tailnet:22:/home/jesse/.config/clipfan/ssh/sync_ed25519:/home/jesse/.config/clipfan/ssh/known_hosts",
		"config:linux-b->mac-a:enabled=true,connect=true,accept=false",
		"config:mac-a->linux-b:enabled=true,connect=false,accept=true",
	}
	if !reflect.DeepEqual(fake.ops, wantOps) {
		t.Fatalf("ops:\n got %#v\nwant %#v", fake.ops, wantOps)
	}

	wantCompleted := []DirectPairStepKind{
		StepConfirmHostKey,
		StepUpsertKnownHostPin,
		StepEnsureConnectorSyncKey,
		StepInstallAcceptorAuthorizedKey,
		StepProbeForcedCommand,
		StepWriteConnectorPeerConfig,
		StepWriteAcceptorPeerConfig,
		StepPatchDirectionalProofs,
		StepTransitionSSHMaterialStaged,
	}
	if !reflect.DeepEqual(result.CompletedSteps, wantCompleted) {
		t.Fatalf("completed steps:\n got %#v\nwant %#v", result.CompletedSteps, wantCompleted)
	}
	if result.ConnectorSyncKey.KeyID != "key-123456" || result.ConnectorSyncKey.PublicKey != testEd25519Key {
		t.Fatalf("connector key = %#v", result.ConnectorSyncKey)
	}
	if result.KnownHostPin.Pattern != "mac-a.tailnet" {
		t.Fatalf("known host pin = %#v", result.KnownHostPin)
	}
	if len(result.ConfigWrites) != 2 || !result.ConfigWrites[0].Enabled || !result.ConfigWrites[1].Enabled {
		t.Fatalf("config writes = %#v", result.ConfigWrites)
	}
	if fake.configMutation.ConnectorKnownHostsPath != "/home/jesse/.config/clipfan/ssh/known_hosts" {
		t.Fatalf("ConnectorKnownHostsPath = %q", fake.configMutation.ConnectorKnownHostsPath)
	}
	if fake.configMutation.ConnectorSyncKey.PrivateKeyPath != "/home/jesse/.config/clipfan/ssh/sync_ed25519" {
		t.Fatalf("ConnectorSyncKey = %#v", fake.configMutation.ConnectorSyncKey)
	}
}

func TestDirectPairProvisionerFailsClosedWhenConfigV2WritesDisabled(t *testing.T) {
	t.Parallel()

	fake := newFakeDirectPairDriver()
	provisioner := DirectPairProvisioner{Driver: fake, configV2WriteGate: func() bool { return false }}

	result, err := provisioner.Provision(context.Background(), validDirectPairProvisionInput())
	if !errors.Is(err, config.ErrConfigV2WritesDisabled) {
		t.Fatalf("Provision() error = %v, want ErrConfigV2WritesDisabled", err)
	}

	wantOps := []string(nil)
	if !reflect.DeepEqual(fake.ops, wantOps) {
		t.Fatalf("ops:\n got %#v\nwant %#v", fake.ops, wantOps)
	}
	if len(result.ConfigWrites) != 2 {
		t.Fatalf("result.ConfigWrites length = %d, want 2", len(result.ConfigWrites))
	}
	for _, step := range result.CompletedSteps {
		if step == StepWriteConnectorPeerConfig || step == StepWriteAcceptorPeerConfig {
			t.Fatalf("completed config step while gate disabled: %#v", result.CompletedSteps)
		}
	}
}

func TestDirectPairProvisionerStopsBeforeMutationOnHostKeyMismatch(t *testing.T) {
	t.Parallel()

	fake := newFakeDirectPairDriver()
	fake.confirmedHostKeyLine = "other.example ssh-ed25519 " + testEd25519Key
	provisioner := DirectPairProvisioner{Driver: fake, configV2WriteGate: func() bool { return true }}

	result, err := provisioner.Provision(context.Background(), validDirectPairProvisionInput())
	if !errors.Is(err, ErrKnownHostMismatch) {
		t.Fatalf("Provision() error = %v, want ErrKnownHostMismatch", err)
	}
	if wantOps := []string{"confirm:mac-a"}; !reflect.DeepEqual(fake.ops, wantOps) {
		t.Fatalf("ops:\n got %#v\nwant %#v", fake.ops, wantOps)
	}
	if len(result.CompletedSteps) != 0 {
		t.Fatalf("completed steps = %#v", result.CompletedSteps)
	}
}

func TestDirectPairProvisionerRejectsMissingDriver(t *testing.T) {
	t.Parallel()

	provisioner := DirectPairProvisioner{configV2WriteGate: func() bool { return true }}
	_, err := provisioner.Provision(context.Background(), validDirectPairProvisionInput())
	if !errors.Is(err, ErrDirectPairProvisionerNotReady) {
		t.Fatalf("Provision() error = %v, want ErrDirectPairProvisionerNotReady", err)
	}
}

func validDirectPairProvisionInput() DirectPairProvisionInput {
	return DirectPairProvisionInput{
		Local: DirectPairProvisionHost{
			Host:           planHost("mac-a", "mac-a.tailnet", "jesse", 22),
			KnownHostsPath: "/Users/jesse/.config/clipfan/ssh/known_hosts",
		},
		Remote: DirectPairProvisionHost{
			Host:           planHost("linux-b", "linux-b.tailnet", "jesse", 22),
			KnownHostsPath: "/home/jesse/.config/clipfan/ssh/known_hosts",
		},
	}
}

type fakeDirectPairDriver struct {
	confirmedHostKeyLine string
	key                  SyncKeyMaterial
	configWritesEnabled  bool
	configMutation       DirectPairConfigMutation
	ops                  []string
}

func newFakeDirectPairDriver() *fakeDirectPairDriver {
	return &fakeDirectPairDriver{
		confirmedHostKeyLine: "mac-a.tailnet ssh-ed25519 " + testEd25519Key,
		key: SyncKeyMaterial{
			PrivateKeyPath: "/home/jesse/.config/clipfan/ssh/sync_ed25519",
			PublicKey:      testEd25519Key,
			KeyID:          "key-123456",
		},
	}
}

func (f *fakeDirectPairDriver) ConfirmHostKey(_ context.Context, host DirectPairHost) (string, error) {
	f.ops = append(f.ops, "confirm:"+host.ID)
	return f.confirmedHostKeyLine, nil
}

func (f *fakeDirectPairDriver) UpsertKnownHostPin(_ context.Context, host DirectPairHost, target DirectPairHost, _ string, pin KnownHostPin) error {
	f.ops = append(f.ops, "pin:"+host.ID+":"+target.ID+":"+pin.Line())
	return nil
}

func (f *fakeDirectPairDriver) EnsureSyncKey(_ context.Context, host DirectPairHost) (SyncKeyMaterial, error) {
	f.ops = append(f.ops, "key:"+host.ID)
	return f.key, nil
}

func (f *fakeDirectPairDriver) InstallAuthorizedKey(_ context.Context, host DirectPairHost, entry ManagedAuthorizedKey) error {
	f.ops = append(f.ops, "auth:"+host.ID+":"+entry.PeerID+":"+entry.KeyID)
	return nil
}

func (f *fakeDirectPairDriver) RunProbe(_ context.Context, cmd SSHCommand, host DirectPairHost) error {
	f.ops = append(f.ops, "probe-from:"+host.ID+":"+cmd.Args[len(cmd.Args)-2]+":"+probeArgValue(cmd.Args, "-p")+":"+probeArgValue(cmd.Args, "-i")+":"+probeOptionValue(cmd.Args, "UserKnownHostsFile="))
	return nil
}

func (f *fakeDirectPairDriver) WriteConfig(_ context.Context, mutation DirectPairConfigMutation) error {
	if !f.configWritesEnabled {
		return errors.New("unexpected config write")
	}
	f.configMutation = mutation
	for _, write := range mutation.Writes {
		f.ops = append(f.ops, "config:"+write.TargetHostID+"->"+write.PeerID+":enabled="+boolString(write.Enabled)+",connect="+boolString(write.Connect)+",accept="+boolString(write.Accept))
	}
	return nil
}

func probeArgValue(args []string, flag string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func probeOptionValue(args []string, prefix string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix)
		}
	}
	return ""
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
