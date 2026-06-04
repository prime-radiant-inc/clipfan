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

type DirectPairConfigApplicator struct {
	ConfigPathByHostID map[string]string
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
	revisions, err := a.readRevisions(paths, ops)
	if err != nil {
		return err
	}
	timestamp := a.now().UTC().Format(time.RFC3339)
	logID := a.logID(mutation)

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
		if hostID == mutation.Plan.ConnectHostID {
			req.SyncKey = &mutation.ConnectorSyncKey.PrivateKeyPath
			req.KnownHosts = &mutation.ConnectorKnownHostsPath
		}
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
		current := revisions[write.TargetHostID]
		result, err := ops.PatchSSHPeerProof(paths[write.TargetHostID], write.PeerID, config.SSHPeerProofPatchRequest{
			ExpectedConfigRevision: &current,
			AcceptProof:            acceptProofPatch(write, mutation.ConnectorSyncKey, timestamp),
			ConnectProof:           connectProofPatch(write, mutation.ConnectorSyncKey, timestamp),
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
	return nil
}

func (a DirectPairConfigApplicator) configPaths(mutation DirectPairConfigMutation) (map[string]string, error) {
	hostIDs := map[string]struct{}{
		mutation.Plan.ConnectHostID: {},
		mutation.Plan.AcceptHostID:  {},
	}
	for _, write := range mutation.Writes {
		hostIDs[write.TargetHostID] = struct{}{}
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
	return &config.SSHPeerDirectionalProofPatch{
		KeyID:       key.KeyID,
		GatewayPath: write.GatewayPath,
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
