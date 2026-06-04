package sshprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

var (
	ErrConfirmedHostKeyMissing       = errors.New("confirmed_host_key_missing")
	ErrRegularSSHProvisionerNotReady = errors.New("regular_ssh_provisioner_not_ready")
	ErrRemoteProvisionOutput         = errors.New("remote_provision_output")
)

type RegularSSHProvisionDriver struct {
	Runner                CommandRunner
	RegularKnownHostsPath string
	ConfirmedHostKeyLines map[string]string
	WriteConfigFunc       func(context.Context, DirectPairConfigMutation) error
}

func (d RegularSSHProvisionDriver) ConfirmHostKey(_ context.Context, host DirectPairHost) (string, error) {
	line := d.ConfirmedHostKeyLines[host.ID]
	if strings.TrimSpace(line) == "" {
		return "", fmt.Errorf("%w: %s", ErrConfirmedHostKeyMissing, host.ID)
	}
	return line, nil
}

func (d RegularSSHProvisionDriver) UpsertKnownHostPin(ctx context.Context, connector DirectPairHost, target DirectPairHost, targetKnownHostsPath string, pin KnownHostPin) error {
	command, err := RegularSSHInstallKnownHostCommand(RegularSSHInstallKnownHostSpec{
		User:                 connector.SSHUser,
		Host:                 connector.SSHHost,
		Port:                 connector.SSHPort,
		KnownHostsPath:       d.RegularKnownHostsPath,
		InstallPath:          connector.InstallPath,
		TargetKnownHostsPath: targetKnownHostsPath,
		TargetHost:           target.SSHHost,
		TargetPort:           target.SSHPort,
		KeyType:              pin.KeyType,
		PublicKey:            pin.PublicKey,
	})
	if err != nil {
		return err
	}
	var payload statusPayload
	if err := d.runJSON(ctx, command, &payload); err != nil {
		return err
	}
	if payload.Status != "ok" {
		return fmt.Errorf("%w: install known host failed", ErrRemoteProvisionOutput)
	}
	return nil
}

func (d RegularSSHProvisionDriver) EnsureSyncKey(ctx context.Context, connector DirectPairProvisionHost) (SyncKeyMaterial, error) {
	command, err := RegularSSHEnsureSyncKeyCommand(RegularSSHEnsureSyncKeySpec{
		User:           connector.Host.SSHUser,
		Host:           connector.Host.SSHHost,
		Port:           connector.Host.SSHPort,
		KnownHostsPath: d.RegularKnownHostsPath,
		InstallPath:    connector.Host.InstallPath,
		HostID:         connector.Host.ID,
		KeyPath:        connector.SyncKeyPath,
	})
	if err != nil {
		return SyncKeyMaterial{}, err
	}
	var payload ensureSyncKeyPayload
	if err := d.runJSON(ctx, command, &payload); err != nil {
		return SyncKeyMaterial{}, err
	}
	if payload.Status != "ok" || payload.HostID != connector.Host.ID || payload.PrivateKeyPath != connector.SyncKeyPath {
		return SyncKeyMaterial{}, fmt.Errorf("%w: sync key identity mismatch", ErrRemoteProvisionOutput)
	}
	material, err := SyncKeyMaterialFromConfig(config.SyncKeyCreateResult{
		PrivateKeyPath: payload.PrivateKeyPath,
		PublicKey:      managedKeyType + " " + payload.PublicKey,
		KeyID:          payload.KeyID,
	})
	if err != nil {
		return SyncKeyMaterial{}, err
	}
	return material, nil
}

func (d RegularSSHProvisionDriver) InstallAuthorizedKey(ctx context.Context, acceptor DirectPairHost, entry ManagedAuthorizedKey) error {
	command, err := RegularSSHInstallAuthorizedKeyCommand(RegularSSHInstallAuthorizedKeySpec{
		User:           acceptor.SSHUser,
		Host:           acceptor.SSHHost,
		Port:           acceptor.SSHPort,
		KnownHostsPath: d.RegularKnownHostsPath,
		InstallPath:    acceptor.InstallPath,
		GatewayPath:    entry.GatewayPath,
		PeerID:         entry.PeerID,
		KeyID:          entry.KeyID,
		PublicKey:      entry.PublicKey,
	})
	if err != nil {
		return err
	}
	var payload authorizedKeyPayload
	if err := d.runJSON(ctx, command, &payload); err != nil {
		return err
	}
	if payload.Status != "ok" || payload.PeerID != entry.PeerID || payload.KeyID != entry.KeyID {
		return fmt.Errorf("%w: authorized key identity mismatch", ErrRemoteProvisionOutput)
	}
	return nil
}

func (d RegularSSHProvisionDriver) RunProbe(ctx context.Context, probe PinnedSSHCommand, connector DirectPairProvisionHost, expectPeerID string, expectKeyID string) error {
	command, err := RegularSSHRunProbeCommand(RegularSSHRunProbeSpec{
		User:           connector.Host.SSHUser,
		Host:           connector.Host.SSHHost,
		Port:           connector.Host.SSHPort,
		KnownHostsPath: d.RegularKnownHostsPath,
		InstallPath:    connector.Host.InstallPath,
		Probe:          probe,
		ExpectPeerID:   expectPeerID,
		ExpectKeyID:    expectKeyID,
	})
	if err != nil {
		return err
	}
	var payload authorizedKeyPayload
	if err := d.runJSON(ctx, command, &payload); err != nil {
		return err
	}
	if payload.Status != "ok" || payload.PeerID != expectPeerID || payload.KeyID != expectKeyID {
		return fmt.Errorf("%w: probe identity mismatch", ErrRemoteProvisionOutput)
	}
	return nil
}

func (d RegularSSHProvisionDriver) WriteConfig(ctx context.Context, mutation DirectPairConfigMutation) error {
	if d.WriteConfigFunc == nil {
		return ErrRegularSSHProvisionerNotReady
	}
	return d.WriteConfigFunc(ctx, mutation)
}

type statusPayload struct {
	Status string `json:"status"`
}

type ensureSyncKeyPayload struct {
	Status         string `json:"status"`
	HostID         string `json:"host_id"`
	KeyID          string `json:"key_id"`
	PublicKey      string `json:"public_key"`
	PrivateKeyPath string `json:"private_key_path"`
}

type authorizedKeyPayload struct {
	Status string `json:"status"`
	PeerID string `json:"peer_id"`
	KeyID  string `json:"key_id"`
}

func (d RegularSSHProvisionDriver) runJSON(ctx context.Context, command SSHCommand, out any) error {
	runner := d.Runner
	if runner == nil {
		return ErrRegularSSHProvisionerNotReady
	}
	output, err := runner.Run(ctx, command)
	if err != nil {
		return err
	}
	if output.StdoutTruncated || output.StderrTruncated {
		return fmt.Errorf("%w: output truncated", ErrRemoteProvisionOutput)
	}
	if len(bytes.TrimSpace(output.Stderr)) != 0 {
		return fmt.Errorf("%w: stderr not empty", ErrRemoteProvisionOutput)
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Stdout))
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("%w: malformed json: %v", ErrRemoteProvisionOutput, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%w: trailing json", ErrRemoteProvisionOutput)
		}
		return fmt.Errorf("%w: malformed json: %v", ErrRemoteProvisionOutput, err)
	}
	return nil
}
