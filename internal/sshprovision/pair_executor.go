package sshprovision

import (
	"context"
	"errors"
	"fmt"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
)

var ErrDirectPairProvisionerNotReady = errors.New("direct_pair_provisioner_not_ready")

type DirectPairProvisionHost struct {
	Host           DirectPairHost
	KnownHostsPath string
	SyncKeyPath    string
}

type DirectPairProvisionInput struct {
	Local  DirectPairProvisionHost
	Remote DirectPairProvisionHost
}

type SyncKeyMaterial struct {
	PrivateKeyPath string
	PublicKey      string
	KeyID          string
}

type DirectPairConfigMutation struct {
	Plan                    DirectPairPlan
	Writes                  []DirectPairConfigWrite
	ConnectorSyncKey        SyncKeyMaterial
	ConnectorKnownHostsPath string
}

type DirectPairProvisionResult struct {
	Plan             DirectPairPlan
	CompletedSteps   []DirectPairStepKind
	ConfigWrites     []DirectPairConfigWrite
	KnownHostPin     KnownHostPin
	ConnectorSyncKey SyncKeyMaterial
}

type DirectPairProvisionDriver interface {
	ConfirmHostKey(context.Context, DirectPairHost) (string, error)
	UpsertKnownHostPin(context.Context, DirectPairHost, DirectPairHost, string, KnownHostPin) error
	EnsureSyncKey(context.Context, DirectPairProvisionHost) (SyncKeyMaterial, error)
	InstallAuthorizedKey(context.Context, DirectPairHost, ManagedAuthorizedKey) error
	RunProbe(context.Context, PinnedSSHCommand, DirectPairProvisionHost, string, string) error
	WriteConfig(context.Context, DirectPairConfigMutation) error
}

type DirectPairProvisioner struct {
	Driver            DirectPairProvisionDriver
	configV2WriteGate func() bool
}

func NewDirectPairProvisioner(driver DirectPairProvisionDriver) DirectPairProvisioner {
	return DirectPairProvisioner{Driver: driver}
}

func (provisioner DirectPairProvisioner) Provision(ctx context.Context, input DirectPairProvisionInput) (DirectPairProvisionResult, error) {
	if provisioner.Driver == nil {
		return DirectPairProvisionResult{}, ErrDirectPairProvisionerNotReady
	}
	normalized, err := normalizeDirectPairProvisionInput(input)
	if err != nil {
		return DirectPairProvisionResult{}, err
	}
	plan, err := BuildDirectPairPlan(DirectPairPlanInput{
		Local:  normalized.Local.Host,
		Remote: normalized.Remote.Host,
	})
	if err != nil {
		return DirectPairProvisionResult{}, err
	}

	result := DirectPairProvisionResult{
		Plan:         plan,
		ConfigWrites: append([]DirectPairConfigWrite(nil), plan.ConfigWrites...),
	}
	connector, err := directPairProvisionHostByID(normalized, plan.ConnectHostID)
	if err != nil {
		return result, err
	}
	acceptor, err := directPairProvisionHostByID(normalized, plan.AcceptHostID)
	if err != nil {
		return result, err
	}

	if !provisioner.configV2WriteEnabled() {
		return result, config.ErrConfigV2WritesDisabled
	}

	confirmedHostKeyLine, err := provisioner.Driver.ConfirmHostKey(ctx, acceptor.Host)
	if err != nil {
		return result, err
	}
	pin, err := ParseKnownHostScanLine(acceptor.Host.SSHHost, acceptor.Host.SSHPort, confirmedHostKeyLine)
	if err != nil {
		return result, err
	}
	result.KnownHostPin = pin
	result.CompletedSteps = append(result.CompletedSteps, StepConfirmHostKey)

	if err := provisioner.Driver.UpsertKnownHostPin(ctx, connector.Host, acceptor.Host, connector.KnownHostsPath, pin); err != nil {
		return result, err
	}
	result.CompletedSteps = append(result.CompletedSteps, StepUpsertKnownHostPin)

	key, err := provisioner.Driver.EnsureSyncKey(ctx, connector)
	if err != nil {
		return result, err
	}
	if err := validateSyncKeyMaterial(key); err != nil {
		return result, err
	}
	result.ConnectorSyncKey = key
	result.CompletedSteps = append(result.CompletedSteps, StepEnsureConnectorSyncKey)

	authKey, err := NewManagedAuthorizedKey(ManagedAuthorizedKey{
		PeerID:      connector.Host.ID,
		KeyID:       key.KeyID,
		GatewayPath: acceptor.Host.GatewayPath,
		PublicKey:   key.PublicKey,
	})
	if err != nil {
		return result, err
	}
	if err := provisioner.Driver.InstallAuthorizedKey(ctx, acceptor.Host, authKey); err != nil {
		return result, err
	}
	result.CompletedSteps = append(result.CompletedSteps, StepInstallAcceptorAuthorizedKey)

	probe := PinnedSSHCommand{
		User:           acceptor.Host.SSHUser,
		Host:           acceptor.Host.SSHHost,
		Port:           acceptor.Host.SSHPort,
		PrivateKeyPath: key.PrivateKeyPath,
		KnownHostsPath: connector.KnownHostsPath,
	}
	if err := provisioner.Driver.RunProbe(ctx, probe, connector, connector.Host.ID, key.KeyID); err != nil {
		return result, err
	}
	result.CompletedSteps = append(result.CompletedSteps, StepProbeForcedCommand)

	if err := provisioner.Driver.WriteConfig(ctx, DirectPairConfigMutation{
		Plan:                    plan,
		Writes:                  append([]DirectPairConfigWrite(nil), plan.ConfigWrites...),
		ConnectorSyncKey:        key,
		ConnectorKnownHostsPath: connector.KnownHostsPath,
	}); err != nil {
		return result, err
	}
	for _, step := range plan.Steps {
		if step.ConfigWrite {
			result.CompletedSteps = append(result.CompletedSteps, step.Kind)
		}
	}
	return result, nil
}

func normalizeDirectPairProvisionInput(input DirectPairProvisionInput) (DirectPairProvisionInput, error) {
	local, err := normalizeDirectPairProvisionHost(input.Local)
	if err != nil {
		return DirectPairProvisionInput{}, fmt.Errorf("invalid local host: %w", err)
	}
	remote, err := normalizeDirectPairProvisionHost(input.Remote)
	if err != nil {
		return DirectPairProvisionInput{}, fmt.Errorf("invalid remote host: %w", err)
	}
	return DirectPairProvisionInput{Local: local, Remote: remote}, nil
}

func normalizeDirectPairProvisionHost(host DirectPairProvisionHost) (DirectPairProvisionHost, error) {
	normalizedHost, err := normalizeDirectPairHost(host.Host)
	if err != nil {
		return DirectPairProvisionHost{}, err
	}
	if err := config.ValidateSSHExecutablePath(host.KnownHostsPath); err != nil {
		return DirectPairProvisionHost{}, fmt.Errorf("invalid known_hosts path: %w", err)
	}
	if err := config.ValidateSyncKeyPath(host.SyncKeyPath); err != nil {
		return DirectPairProvisionHost{}, fmt.Errorf("invalid sync key path: %w", err)
	}
	if err := config.ValidateSSHExecutablePath(host.SyncKeyPath); err != nil {
		return DirectPairProvisionHost{}, fmt.Errorf("invalid sync key ssh path: %w", err)
	}
	host.Host = normalizedHost
	return host, nil
}

func directPairProvisionHostByID(input DirectPairProvisionInput, hostID string) (DirectPairProvisionHost, error) {
	switch hostID {
	case input.Local.Host.ID:
		return input.Local, nil
	case input.Remote.Host.ID:
		return input.Remote, nil
	default:
		return DirectPairProvisionHost{}, fmt.Errorf("missing provision host: %s", hostID)
	}
}

func validateSyncKeyMaterial(key SyncKeyMaterial) error {
	if err := config.ValidateSSHExecutablePath(key.PrivateKeyPath); err != nil {
		return fmt.Errorf("invalid sync private key path: %w", err)
	}
	if err := ValidateManagedAuthorizedKeyID(key.KeyID); err != nil {
		return err
	}
	keyType, err := publicKeyType(key.PublicKey)
	if err != nil {
		return err
	}
	if keyType != managedKeyType {
		return fmt.Errorf("%w: unsupported public key type %q", ErrInvalidAuthorizedKey, keyType)
	}
	return nil
}

func (provisioner DirectPairProvisioner) configV2WriteEnabled() bool {
	if provisioner.configV2WriteGate != nil {
		return provisioner.configV2WriteGate()
	}
	return releaseflags.ConfigV2WriteEnabled
}
