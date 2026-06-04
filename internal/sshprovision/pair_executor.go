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
	SyncKeys                map[string]SyncKeyMaterial
	KnownHostsPaths         map[string]string
	ConnectorSyncKey        SyncKeyMaterial
	ConnectorKnownHostsPath string
}

type DirectPairProvisionResult struct {
	Plan             DirectPairPlan
	CompletedSteps   []DirectPairStepKind
	ConfigWrites     []DirectPairConfigWrite
	KnownHostPins    map[string]KnownHostPin
	KnownHostPin     KnownHostPin
	SyncKeys         map[string]SyncKeyMaterial
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

func NewDirectPairProvisionerWithConfigV2WriteGate(driver DirectPairProvisionDriver, gate func() bool) DirectPairProvisioner {
	return DirectPairProvisioner{Driver: driver, configV2WriteGate: gate}
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
		Plan:          plan,
		ConfigWrites:  append([]DirectPairConfigWrite(nil), plan.ConfigWrites...),
		KnownHostPins: map[string]KnownHostPin{},
		SyncKeys:      map[string]SyncKeyMaterial{},
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
	hosts := []DirectPairProvisionHost{connector, acceptor}
	directions := []directPairProvisionDirection{
		{source: connector, target: acceptor},
		{source: acceptor, target: connector},
	}

	for _, direction := range directions {
		confirmedHostKeyLine, err := provisioner.Driver.ConfirmHostKey(ctx, direction.target.Host)
		if err != nil {
			return result, err
		}
		pin, err := ParseKnownHostScanLine(direction.target.Host.SSHHost, direction.target.Host.SSHPort, confirmedHostKeyLine)
		if err != nil {
			return result, err
		}
		result.KnownHostPins[direction.target.Host.ID] = pin
		if direction.target.Host.ID == acceptor.Host.ID {
			result.KnownHostPin = pin
		}
	}
	result.CompletedSteps = append(result.CompletedSteps, StepConfirmHostKey)

	for _, direction := range directions {
		pin := result.KnownHostPins[direction.target.Host.ID]
		if err := provisioner.Driver.UpsertKnownHostPin(ctx, direction.source.Host, direction.target.Host, direction.source.KnownHostsPath, pin); err != nil {
			return result, err
		}
	}
	result.CompletedSteps = append(result.CompletedSteps, StepUpsertKnownHostPin)

	for _, host := range hosts {
		key, err := provisioner.Driver.EnsureSyncKey(ctx, host)
		if err != nil {
			return result, err
		}
		if err := validateSyncKeyMaterial(key); err != nil {
			return result, err
		}
		result.SyncKeys[host.Host.ID] = key
		if host.Host.ID == connector.Host.ID {
			result.ConnectorSyncKey = key
		}
	}
	result.CompletedSteps = append(result.CompletedSteps, StepEnsureConnectorSyncKey)

	for _, direction := range directions {
		key := result.SyncKeys[direction.source.Host.ID]
		authKey, err := NewManagedAuthorizedKey(ManagedAuthorizedKey{
			PeerID:      direction.source.Host.ID,
			KeyID:       key.KeyID,
			GatewayPath: direction.target.Host.GatewayPath,
			PublicKey:   key.PublicKey,
		})
		if err != nil {
			return result, err
		}
		if err := provisioner.Driver.InstallAuthorizedKey(ctx, direction.target.Host, authKey); err != nil {
			return result, err
		}
	}
	result.CompletedSteps = append(result.CompletedSteps, StepInstallAcceptorAuthorizedKey)

	for _, direction := range directions {
		key := result.SyncKeys[direction.source.Host.ID]
		probe := PinnedSSHCommand{
			User:           direction.target.Host.SSHUser,
			Host:           direction.target.Host.SSHHost,
			Port:           direction.target.Host.SSHPort,
			PrivateKeyPath: key.PrivateKeyPath,
			KnownHostsPath: direction.source.KnownHostsPath,
		}
		if err := provisioner.Driver.RunProbe(ctx, probe, direction.source, direction.source.Host.ID, key.KeyID); err != nil {
			return result, err
		}
	}
	result.CompletedSteps = append(result.CompletedSteps, StepProbeForcedCommand)

	if err := provisioner.Driver.WriteConfig(ctx, DirectPairConfigMutation{
		Plan:                    plan,
		Writes:                  append([]DirectPairConfigWrite(nil), plan.ConfigWrites...),
		SyncKeys:                cloneSyncKeyMaterialMap(result.SyncKeys),
		KnownHostsPaths:         knownHostsPathsByHost(hosts),
		ConnectorSyncKey:        result.ConnectorSyncKey,
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

type directPairProvisionDirection struct {
	source DirectPairProvisionHost
	target DirectPairProvisionHost
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

func knownHostsPathsByHost(hosts []DirectPairProvisionHost) map[string]string {
	out := make(map[string]string, len(hosts))
	for _, host := range hosts {
		out[host.Host.ID] = host.KnownHostsPath
	}
	return out
}

func cloneSyncKeyMaterialMap(values map[string]SyncKeyMaterial) map[string]SyncKeyMaterial {
	out := make(map[string]SyncKeyMaterial, len(values))
	for hostID, material := range values {
		out[hostID] = material
	}
	return out
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
