package sshprovision

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

var ErrDirectPairConfigPathMissing = errors.New("direct_pair_config_path_missing")

type DirectPairConfigOps interface {
	ReadConfigRevision(path string) (config.ConfigRevisionStatus, error)
	UpdateSSHLocalMaterial(path string, req config.SSHLocalMaterialUpdateRequest) (config.ConfigRevisionStatus, error)
	UpsertSSHPeer(path string, peerID string, req config.SSHPeerUpsertRequest) (config.SSHPeerConfigReadResult, error)
	PatchSSHPeerProof(path string, peerID string, req config.SSHPeerProofPatchRequest) (config.SSHPeerConfigReadResult, error)
	TransitionSSHPeer(path string, peerID string, req config.SSHPeerTransitionRequest) (config.SSHPeerConfigReadResult, error)
}

type DirectPairConfigApplyPhase string

const (
	DirectPairConfigApplyAll   DirectPairConfigApplyPhase = ""
	DirectPairConfigApplyStage DirectPairConfigApplyPhase = "stage"
	DirectPairConfigApplyReady DirectPairConfigApplyPhase = "ready"
)

type DirectPairConfigApplicator struct {
	ConfigPathByHostID map[string]string
	TargetHostIDs      []string
	Phase              DirectPairConfigApplyPhase
	Ops                DirectPairConfigOps
	Now                func() time.Time
	LogID              func(DirectPairConfigMutation) string
}

func (a DirectPairConfigApplicator) Apply(ctx context.Context, mutation DirectPairConfigMutation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	paths, err := a.configPaths(mutation)
	if err != nil {
		return err
	}
	ops := a.ops()
	phase := a.phase()
	if err := validateDirectPairConfigMutationForApply(mutation, paths, phase); err != nil {
		return err
	}
	revisions, err := a.readRevisions(paths, ops)
	if err != nil {
		return err
	}
	timestamp := a.now().UTC().Format(time.RFC3339)
	logID := a.logID(mutation)

	if phase.appliesStage() {
		for _, hostID := range sortedMapKeys(paths) {
			if err := ctx.Err(); err != nil {
				return err
			}
			current := revisions[hostID]
			transport := config.TransportSSH
			req := config.SSHLocalMaterialUpdateRequest{
				ExpectedConfigRevision: &current,
				Transport:              &transport,
			}
			syncKey, err := mutationSyncKeyForHost(mutation, hostID)
			if err != nil {
				return err
			}
			knownHostsPath, err := mutationKnownHostsPathForHost(mutation, hostID)
			if err != nil {
				return err
			}
			req.SyncKey = &syncKey.PrivateKeyPath
			req.KnownHosts = &knownHostsPath
			status, err := ops.UpdateSSHLocalMaterial(paths[hostID], req)
			if err != nil {
				return err
			}
			if err := setRevision(revisions, hostID, status.ConfigRevision); err != nil {
				return err
			}
		}

		for _, write := range mutation.Writes {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !shouldApplyConfigWrite(paths, write.TargetHostID) {
				continue
			}
			current := revisions[write.TargetHostID]
			result, err := ops.UpsertSSHPeer(paths[write.TargetHostID], write.PeerID, config.SSHPeerUpsertRequest{
				ExpectedConfigRevision: &current,
				Peer:                   sshPeerUpsertFields(write),
			})
			if err != nil {
				return err
			}
			if err := setRevision(revisions, write.TargetHostID, result.ConfigRevision); err != nil {
				return err
			}
		}

		for _, write := range mutation.Writes {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !shouldApplyConfigWrite(paths, write.TargetHostID) {
				continue
			}
			current := revisions[write.TargetHostID]
			acceptKey, err := mutationSyncKeyForHost(mutation, write.PeerID)
			if err != nil {
				return err
			}
			connectKey, err := mutationSyncKeyForHost(mutation, write.TargetHostID)
			if err != nil {
				return err
			}
			result, err := ops.PatchSSHPeerProof(paths[write.TargetHostID], write.PeerID, config.SSHPeerProofPatchRequest{
				ExpectedConfigRevision: &current,
				AcceptProof:            acceptProofPatch(write, acceptKey, timestamp),
				ConnectProof:           connectProofPatch(write, connectKey, timestamp),
			})
			if err != nil {
				return err
			}
			if err := setRevision(revisions, write.TargetHostID, result.ConfigRevision); err != nil {
				return err
			}
		}

		for _, write := range mutation.Writes {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !shouldApplyConfigWrite(paths, write.TargetHostID) {
				continue
			}
			current := revisions[write.TargetHostID]
			result, err := ops.TransitionSSHPeer(paths[write.TargetHostID], write.PeerID, config.SSHPeerTransitionRequest{
				ExpectedConfigRevision: &current,
				FromState:              config.MigrationStateLoopbackUnprovisioned,
				ToState:                config.MigrationStateSSHMaterialStaged,
				Reason:                 "material_staged",
				LogID:                  logID,
			})
			if err != nil {
				return err
			}
			if err := setRevision(revisions, write.TargetHostID, result.ConfigRevision); err != nil {
				return err
			}
		}
	}

	if phase.appliesReady() {
		for _, write := range mutation.Writes {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !shouldApplyConfigWrite(paths, write.TargetHostID) {
				continue
			}
			current := revisions[write.TargetHostID]
			result, err := ops.TransitionSSHPeer(paths[write.TargetHostID], write.PeerID, config.SSHPeerTransitionRequest{
				ExpectedConfigRevision: &current,
				FromState:              config.MigrationStateSSHMaterialStaged,
				ToState:                config.MigrationStateSSHKeysReady,
				Reason:                 "ssh_material_verified",
				LogID:                  logID,
			})
			if err != nil {
				return err
			}
			if err := setRevision(revisions, write.TargetHostID, result.ConfigRevision); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a DirectPairConfigApplicator) configPaths(mutation DirectPairConfigMutation) (map[string]string, error) {
	hostIDs := map[string]struct{}{}
	if len(a.TargetHostIDs) > 0 {
		for _, hostID := range a.TargetHostIDs {
			hostIDs[hostID] = struct{}{}
		}
	} else {
		hostIDs[mutation.Plan.ConnectHostID] = struct{}{}
		hostIDs[mutation.Plan.AcceptHostID] = struct{}{}
		for _, write := range mutation.Writes {
			hostIDs[write.TargetHostID] = struct{}{}
		}
	}
	out := make(map[string]string, len(hostIDs))
	for hostID := range hostIDs {
		path := a.ConfigPathByHostID[hostID]
		if path == "" {
			return nil, fmt.Errorf("%w: %s", ErrDirectPairConfigPathMissing, hostID)
		}
		out[hostID] = path
	}
	return out, nil
}

func shouldApplyConfigWrite(paths map[string]string, hostID string) bool {
	_, ok := paths[hostID]
	return ok
}

func validateDirectPairConfigMutationForApply(mutation DirectPairConfigMutation, paths map[string]string, phase DirectPairConfigApplyPhase) error {
	if !phase.appliesStage() {
		return nil
	}
	for hostID := range paths {
		if _, err := mutationSyncKeyForHost(mutation, hostID); err != nil {
			return err
		}
		if _, err := mutationKnownHostsPathForHost(mutation, hostID); err != nil {
			return err
		}
	}
	for _, write := range mutation.Writes {
		if !shouldApplyConfigWrite(paths, write.TargetHostID) {
			continue
		}
		if _, err := mutationSyncKeyForHost(mutation, write.TargetHostID); err != nil {
			return err
		}
		if _, err := mutationSyncKeyForHost(mutation, write.PeerID); err != nil {
			return err
		}
	}
	return nil
}

func (a DirectPairConfigApplicator) readRevisions(paths map[string]string, ops DirectPairConfigOps) (map[string]uint64, error) {
	revisions := make(map[string]uint64, len(paths))
	for _, hostID := range sortedMapKeys(paths) {
		status, err := ops.ReadConfigRevision(paths[hostID])
		if err != nil {
			return nil, err
		}
		if status.RevisionState != config.RevisionStateVersioned || status.ConfigRevision == nil || *status.ConfigRevision == 0 {
			return nil, config.ErrConfigRevisionConflict
		}
		revisions[hostID] = *status.ConfigRevision
	}
	return revisions, nil
}

func (a DirectPairConfigApplicator) ops() DirectPairConfigOps {
	if a.Ops != nil {
		return a.Ops
	}
	return defaultDirectPairConfigOps{}
}

func (a DirectPairConfigApplicator) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a DirectPairConfigApplicator) logID(mutation DirectPairConfigMutation) string {
	if a.LogID != nil {
		return a.LogID(mutation)
	}
	return fmt.Sprintf("ssh-provision-%d", a.now().UTC().UnixNano())
}

func (a DirectPairConfigApplicator) phase() DirectPairConfigApplyPhase {
	switch a.Phase {
	case DirectPairConfigApplyStage, DirectPairConfigApplyReady:
		return a.Phase
	default:
		return DirectPairConfigApplyAll
	}
}

func (p DirectPairConfigApplyPhase) appliesStage() bool {
	return p == DirectPairConfigApplyAll || p == DirectPairConfigApplyStage
}

func (p DirectPairConfigApplyPhase) appliesReady() bool {
	return p == DirectPairConfigApplyAll || p == DirectPairConfigApplyReady
}

type defaultDirectPairConfigOps struct{}

func (defaultDirectPairConfigOps) ReadConfigRevision(path string) (config.ConfigRevisionStatus, error) {
	return config.ReadConfigRevision(path)
}

func (defaultDirectPairConfigOps) UpdateSSHLocalMaterial(path string, req config.SSHLocalMaterialUpdateRequest) (config.ConfigRevisionStatus, error) {
	return config.UpdateSSHLocalMaterial(path, req)
}

func (defaultDirectPairConfigOps) UpsertSSHPeer(path string, peerID string, req config.SSHPeerUpsertRequest) (config.SSHPeerConfigReadResult, error) {
	return config.UpsertSSHPeer(path, peerID, req)
}

func (defaultDirectPairConfigOps) PatchSSHPeerProof(path string, peerID string, req config.SSHPeerProofPatchRequest) (config.SSHPeerConfigReadResult, error) {
	return config.PatchSSHPeerProof(path, peerID, req)
}

func (defaultDirectPairConfigOps) TransitionSSHPeer(path string, peerID string, req config.SSHPeerTransitionRequest) (config.SSHPeerConfigReadResult, error) {
	return config.TransitionSSHPeer(path, peerID, req)
}

func sshPeerUpsertFields(write DirectPairConfigWrite) config.SSHPeerUpsertFields {
	state := config.MigrationState(write.MigrationState)
	fields := config.SSHPeerUpsertFields{
		ID:             stringPtr(write.PeerID),
		Enabled:        boolPtr(write.Enabled),
		Accept:         boolPtr(write.Accept),
		Connect:        boolPtr(write.Connect),
		Persistent:     boolPtr(write.Persistent),
		OnDemand:       boolPtr(write.OnDemand),
		InstallPath:    stringPtrIfNotEmpty(write.InstallPath),
		GatewayPath:    stringPtrIfNotEmpty(write.GatewayPath),
		MigrationState: &state,
	}
	if write.Connect {
		fields.SSHHost = stringPtrIfNotEmpty(write.SSHHost)
		fields.SSHUser = stringPtrIfNotEmpty(write.SSHUser)
		fields.SSHPort = intPtr(write.SSHPort)
	}
	return fields
}

func acceptProofPatch(write DirectPairConfigWrite, key SyncKeyMaterial, verifiedAt string) *config.SSHPeerDirectionalProofPatch {
	if !write.Accept {
		return nil
	}
	gatewayPath := write.TargetGatewayPath
	if gatewayPath == "" {
		gatewayPath = write.GatewayPath
	}
	return &config.SSHPeerDirectionalProofPatch{
		KeyID:       key.KeyID,
		GatewayPath: gatewayPath,
		VerifiedAt:  verifiedAt,
		VerifiedBy:  config.ProofVerifiedByRegularSSH,
	}
}

func connectProofPatch(write DirectPairConfigWrite, key SyncKeyMaterial, verifiedAt string) *config.SSHPeerDirectionalProofPatch {
	if !write.Connect {
		return nil
	}
	return &config.SSHPeerDirectionalProofPatch{
		KeyID:       key.KeyID,
		GatewayPath: write.GatewayPath,
		VerifiedAt:  verifiedAt,
		VerifiedBy:  config.ProofVerifiedByRegularSSH,
	}
}

func mutationSyncKeyForHost(mutation DirectPairConfigMutation, hostID string) (SyncKeyMaterial, error) {
	if material, ok := mutation.SyncKeys[hostID]; ok {
		return material, nil
	}
	if hostID == mutation.Plan.ConnectHostID && mutation.ConnectorSyncKey.PrivateKeyPath != "" {
		return mutation.ConnectorSyncKey, nil
	}
	return SyncKeyMaterial{}, fmt.Errorf("missing_sync_key_material: %s", hostID)
}

func mutationKnownHostsPathForHost(mutation DirectPairConfigMutation, hostID string) (string, error) {
	if path := mutation.KnownHostsPaths[hostID]; path != "" {
		return path, nil
	}
	if hostID == mutation.Plan.ConnectHostID && mutation.ConnectorKnownHostsPath != "" {
		return mutation.ConnectorKnownHostsPath, nil
	}
	return "", fmt.Errorf("missing_known_hosts_path: %s", hostID)
}

func setRevision(revisions map[string]uint64, hostID string, revision *uint64) error {
	if revision == nil || *revision == 0 {
		return config.ErrConfigRevisionConflict
	}
	revisions[hostID] = *revision
	return nil
}

func stringPtrIfNotEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func stringPtr(value string) *string {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

func intPtr(value int) *int {
	return &value
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
