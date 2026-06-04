package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

type SSHApplyDirectConfigPayload struct {
	HostID     string                                `json:"host_id"`
	ConfigPath string                                `json:"config_path"`
	Phase      string                                `json:"phase"`
	Mutation   sshprovision.DirectPairConfigMutation `json:"mutation"`
}

func RunSSHApplyDirectConfig(args []string, stdout io.Writer, stderr io.Writer) error {
	return runSSHApplyDirectConfigWithStdin(args, os.Stdin, stdout, stderr, nil)
}

func runSSHApplyDirectConfig(args []string, stdout io.Writer, stderr io.Writer, ops sshprovision.DirectPairConfigOps) error {
	return runSSHApplyDirectConfigWithStdin(args, nil, stdout, stderr, ops)
}

func runSSHApplyDirectConfigWithStdin(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer, ops sshprovision.DirectPairConfigOps) error {
	fs := flag.NewFlagSet("ssh-apply-direct-config", flag.ContinueOnError)
	fs.SetOutput(stderr)
	payloadBase64 := fs.String("payload-base64", "", "base64 config payload")
	payloadStdin := fs.Bool("payload-stdin", false, "read base64 config payload from stdin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected ssh-apply-direct-config argument")
	}
	payloadText := *payloadBase64
	if *payloadStdin {
		if payloadText != "" {
			return fmt.Errorf("payload_source_conflict")
		}
		if stdin == nil {
			return fmt.Errorf("missing_payload_stdin")
		}
		data, err := io.ReadAll(stdin)
		if err != nil {
			return fmt.Errorf("read_payload_stdin: %w", err)
		}
		payloadText = string(data)
	}
	payload, err := decodeSSHApplyDirectConfigPayload(payloadText)
	if err != nil {
		return err
	}
	if err := requireSSHApplyDirectConfigTarget(payload); err != nil {
		return err
	}
	applicator := sshprovision.DirectPairConfigApplicator{
		ConfigPathByHostID: map[string]string{payload.HostID: payload.ConfigPath},
		TargetHostIDs:      []string{payload.HostID},
		Phase:              sshApplyDirectConfigPhase(payload.Phase),
		Ops:                ops,
	}
	if err := applicator.Apply(context.Background(), payload.Mutation); err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(map[string]string{"status": "ok", "host_id": payload.HostID})
}

func decodeSSHApplyDirectConfigPayload(payloadBase64 string) (SSHApplyDirectConfigPayload, error) {
	payloadBase64 = strings.TrimSpace(payloadBase64)
	if payloadBase64 == "" {
		return SSHApplyDirectConfigPayload{}, fmt.Errorf("missing_payload")
	}
	data, err := base64.StdEncoding.DecodeString(payloadBase64)
	if err != nil {
		return SSHApplyDirectConfigPayload{}, fmt.Errorf("malformed_payload: %w", err)
	}
	var payload SSHApplyDirectConfigPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return SSHApplyDirectConfigPayload{}, fmt.Errorf("malformed_payload: %w", err)
	}
	if err := config.ValidateHostID(payload.HostID); err != nil {
		return SSHApplyDirectConfigPayload{}, fmt.Errorf("invalid_payload_host_id: %w", err)
	}
	if err := config.ValidateSafeAbsolutePath(payload.ConfigPath); err != nil {
		return SSHApplyDirectConfigPayload{}, fmt.Errorf("invalid_payload_config_path: %w", err)
	}
	if phase := sshApplyDirectConfigPhase(payload.Phase); phase != sshprovision.DirectPairConfigApplyStage && phase != sshprovision.DirectPairConfigApplyReady {
		return SSHApplyDirectConfigPayload{}, fmt.Errorf("invalid_payload_phase")
	}
	if payload.Mutation.Plan.PairID == "" || len(payload.Mutation.Writes) == 0 {
		return SSHApplyDirectConfigPayload{}, fmt.Errorf("invalid_payload_mutation")
	}
	if _, ok := payload.Mutation.SyncKeys[payload.HostID]; !ok {
		return SSHApplyDirectConfigPayload{}, fmt.Errorf("invalid_payload_missing_sync_key")
	}
	if payload.Mutation.KnownHostsPaths[payload.HostID] == "" {
		return SSHApplyDirectConfigPayload{}, fmt.Errorf("invalid_payload_missing_known_hosts")
	}
	hasTargetWrite := false
	for _, write := range payload.Mutation.Writes {
		if write.TargetHostID == payload.HostID {
			hasTargetWrite = true
			break
		}
	}
	if !hasTargetWrite {
		return SSHApplyDirectConfigPayload{}, fmt.Errorf("invalid_payload_missing_target_write")
	}
	return payload, nil
}

func sshApplyDirectConfigPhase(phase string) sshprovision.DirectPairConfigApplyPhase {
	switch phase {
	case string(sshprovision.DirectPairConfigApplyStage):
		return sshprovision.DirectPairConfigApplyStage
	case string(sshprovision.DirectPairConfigApplyReady):
		return sshprovision.DirectPairConfigApplyReady
	default:
		return sshprovision.DirectPairConfigApplyAll
	}
}

func requireSSHApplyDirectConfigTarget(payload SSHApplyDirectConfigPayload) error {
	status, err := config.ReadConfigRevision(payload.ConfigPath)
	if err != nil {
		return err
	}
	_, err = config.RequirePersistedHostIDAtRevision(payload.ConfigPath, payload.HostID, config.RevisionExpectation{
		State:    status.RevisionState,
		Revision: status.ConfigRevision,
	})
	return err
}
