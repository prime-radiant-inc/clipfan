package sshprovision

import (
	"fmt"
	"sort"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

type DirectPairHost struct {
	ID            string
	SSHHost       string
	SSHUser       string
	SSHPort       int
	InstallPath   string
	GatewayPath   string
	SSHServerMode SSHServerMode
}

type DirectPairPlanInput struct {
	Local  DirectPairHost
	Remote DirectPairHost
}

type DirectMeshPlan struct {
	Pairs []DirectPairPlan
}

type DirectPairPlan struct {
	PairID        string
	ConnectHostID string
	AcceptHostID  string
	Steps         []DirectPairStep
	ConfigWrites  []DirectPairConfigWrite
}

type DirectPairStepKind string

const (
	StepConfirmHostKey               DirectPairStepKind = "confirm_host_key"
	StepUpsertKnownHostPin           DirectPairStepKind = "upsert_known_host_pin"
	StepEnsureConnectorSyncKey       DirectPairStepKind = "ensure_connector_sync_key"
	StepInstallAcceptorAuthorizedKey DirectPairStepKind = "install_acceptor_authorized_key"
	StepProbeForcedCommand           DirectPairStepKind = "probe_forced_command"
	StepWriteConnectorPeerConfig     DirectPairStepKind = "write_connector_peer_config"
	StepWriteAcceptorPeerConfig      DirectPairStepKind = "write_acceptor_peer_config"
	StepPatchDirectionalProofs       DirectPairStepKind = "patch_directional_proofs"
	StepTransitionSSHMaterialStaged  DirectPairStepKind = "transition_ssh_material_staged"
	StepTransitionSSHKeysReady       DirectPairStepKind = "transition_ssh_keys_ready"
)

type DirectPairStep struct {
	Kind        DirectPairStepKind
	ConfigWrite bool
}

type DirectPairConfigWrite struct {
	TargetHostID      string
	PeerID            string
	SSHHost           string
	SSHUser           string
	SSHPort           int
	InstallPath       string
	GatewayPath       string
	TargetGatewayPath string
	Enabled           bool
	Accept            bool
	Connect           bool
	Persistent        bool
	OnDemand          bool
	MigrationState    string
	AcceptVerifiedBy  string
	ConnectVerifiedBy string
}

func BuildDirectPairPlan(input DirectPairPlanInput) (DirectPairPlan, error) {
	local, err := normalizeDirectPairHost(input.Local)
	if err != nil {
		return DirectPairPlan{}, fmt.Errorf("invalid local host: %w", err)
	}
	remote, err := normalizeDirectPairHost(input.Remote)
	if err != nil {
		return DirectPairPlan{}, fmt.Errorf("invalid remote host: %w", err)
	}
	if local.ID == remote.ID {
		return DirectPairPlan{}, fmt.Errorf("duplicate host id: %s", local.ID)
	}

	connector, acceptor := local, remote
	if remote.ID < local.ID {
		connector, acceptor = remote, local
	}

	return DirectPairPlan{
		PairID:        connector.ID + "--" + acceptor.ID,
		ConnectHostID: connector.ID,
		AcceptHostID:  acceptor.ID,
		Steps: []DirectPairStep{
			{Kind: StepConfirmHostKey},
			{Kind: StepUpsertKnownHostPin},
			{Kind: StepEnsureConnectorSyncKey},
			{Kind: StepInstallAcceptorAuthorizedKey},
			{Kind: StepProbeForcedCommand},
			{Kind: StepWriteConnectorPeerConfig, ConfigWrite: true},
			{Kind: StepWriteAcceptorPeerConfig, ConfigWrite: true},
			{Kind: StepPatchDirectionalProofs, ConfigWrite: true},
			{Kind: StepTransitionSSHMaterialStaged, ConfigWrite: true},
			{Kind: StepTransitionSSHKeysReady, ConfigWrite: true},
		},
		ConfigWrites: []DirectPairConfigWrite{
			reciprocalPeerConfigWrite(connector, acceptor),
			reciprocalPeerConfigWrite(acceptor, connector),
		},
	}, nil
}

func BuildDirectMeshPlan(hosts []DirectPairHost) (DirectMeshPlan, error) {
	normalized := make([]DirectPairHost, len(hosts))
	for i, host := range hosts {
		next, err := normalizeDirectPairHost(host)
		if err != nil {
			return DirectMeshPlan{}, fmt.Errorf("invalid host %d: %w", i, err)
		}
		normalized[i] = next
	}
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].ID < normalized[j].ID
	})
	for i := 1; i < len(normalized); i++ {
		if normalized[i-1].ID == normalized[i].ID {
			return DirectMeshPlan{}, fmt.Errorf("duplicate host id: %s", normalized[i].ID)
		}
	}

	var mesh DirectMeshPlan
	for i := 0; i < len(normalized); i++ {
		for j := i + 1; j < len(normalized); j++ {
			plan, err := BuildDirectPairPlan(DirectPairPlanInput{Local: normalized[i], Remote: normalized[j]})
			if err != nil {
				return DirectMeshPlan{}, err
			}
			mesh.Pairs = append(mesh.Pairs, plan)
		}
	}
	return mesh, nil
}

func normalizeDirectPairHost(host DirectPairHost) (DirectPairHost, error) {
	if err := config.ValidateHostID(host.ID); err != nil {
		return DirectPairHost{}, err
	}
	sshHost, err := config.CanonicalSSHHost(host.SSHHost)
	if err != nil {
		return DirectPairHost{}, fmt.Errorf("invalid ssh host: %w", err)
	}
	if err := config.ValidateSSHUser(host.SSHUser); err != nil {
		return DirectPairHost{}, fmt.Errorf("invalid ssh user: %w", err)
	}
	if host.SSHPort < 1 || host.SSHPort > 65535 {
		return DirectPairHost{}, fmt.Errorf("invalid ssh port: %d", host.SSHPort)
	}
	if err := config.ValidateSSHExecutablePath(host.InstallPath); err != nil {
		return DirectPairHost{}, fmt.Errorf("invalid install path: %w", err)
	}
	if err := config.ValidateSSHExecutablePath(host.GatewayPath); err != nil {
		return DirectPairHost{}, fmt.Errorf("invalid gateway path: %w", err)
	}
	mode, err := NormalizeSSHServerMode(string(host.SSHServerMode))
	if err != nil {
		return DirectPairHost{}, err
	}
	host.SSHHost = sshHost
	host.SSHServerMode = mode
	return host, nil
}

func reciprocalPeerConfigWrite(target DirectPairHost, peer DirectPairHost) DirectPairConfigWrite {
	return DirectPairConfigWrite{
		TargetHostID:      target.ID,
		PeerID:            peer.ID,
		SSHHost:           peer.SSHHost,
		SSHUser:           peer.SSHUser,
		SSHPort:           peer.SSHPort,
		InstallPath:       peer.InstallPath,
		GatewayPath:       peer.GatewayPath,
		TargetGatewayPath: target.GatewayPath,
		Enabled:           true,
		Accept:            true,
		Connect:           true,
		Persistent:        true,
		MigrationState:    string(config.MigrationStateLoopbackUnprovisioned),
		AcceptVerifiedBy:  ProofVerifiedByForSSHServerMode(target.SSHServerMode),
		ConnectVerifiedBy: ProofVerifiedByForSSHServerMode(peer.SSHServerMode),
	}
}
