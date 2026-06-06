package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

func RunSSHRunProbe(args []string, stdout io.Writer, stderr io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return runSSHRunProbe(ctx, args, stdout, stderr, sshprovision.ExecCommandRunner{MaxOutputBytes: 4096})
}

func runSSHRunProbe(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer, runner sshprovision.CommandRunner) error {
	fs := flag.NewFlagSet("ssh-run-probe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	user := fs.String("user", "", "target ssh user")
	host := fs.String("host", "", "target ssh host")
	port := fs.Int("port", 22, "target ssh port")
	privateKeyPath := fs.String("private-key", "", "sync private key path")
	knownHostsPath := fs.String("known-hosts", "", "known_hosts path")
	expectPeer := fs.String("expect-peer", "", "expected gateway peer id")
	expectKeyID := fs.String("expect-key-id", "", "expected gateway key id")
	gatewayPath := fs.String("gateway-path", "", "gateway path for direct gateway probes")
	directGateway := fs.Bool("direct-gateway", false, "run gateway directly instead of relying on forced command")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected ssh-run-probe argument")
	}
	if runner == nil {
		return fmt.Errorf("missing ssh command runner")
	}
	if err := config.ValidateHostID(*expectPeer); err != nil {
		return fmt.Errorf("invalid expected peer: %w", err)
	}
	if err := sshprovision.ValidateManagedAuthorizedKeyID(*expectKeyID); err != nil {
		return fmt.Errorf("invalid expected key id: %w", err)
	}
	command, err := sshprovision.PinnedSSHProbeCommand(sshprovision.PinnedSSHCommand{
		User:            *user,
		Host:            *host,
		Port:            *port,
		PrivateKeyPath:  *privateKeyPath,
		KnownHostsPath:  *knownHostsPath,
		GatewayPath:     *gatewayPath,
		AuthorizedPeer:  *expectPeer,
		AuthorizedKeyID: *expectKeyID,
		DirectGateway:   *directGateway,
	})
	if err != nil {
		return err
	}
	output, err := runner.Run(ctx, command)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("ssh_probe_timeout: %w", ctxErr)
		}
		return err
	}
	if output.StdoutTruncated || output.StderrTruncated {
		return fmt.Errorf("ssh_probe_output_truncated")
	}
	if len(bytes.TrimSpace(output.Stderr)) != 0 {
		return fmt.Errorf("ssh_probe_stderr_not_empty")
	}
	probe, err := decodeGatewayProbeOutput(output.Stdout)
	if err != nil {
		return err
	}
	if probe.Status != "ok" || probe.PeerID != *expectPeer || probe.KeyID != *expectKeyID {
		return fmt.Errorf("ssh_probe_identity_mismatch")
	}
	return json.NewEncoder(stdout).Encode(map[string]any{
		"status":  "ok",
		"peer_id": probe.PeerID,
		"key_id":  probe.KeyID,
	})
}

type gatewayProbeOutput struct {
	Status string `json:"status"`
	PeerID string `json:"peer_id"`
	KeyID  string `json:"key_id"`
}

func decodeGatewayProbeOutput(data []byte) (gatewayProbeOutput, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var payload gatewayProbeOutput
	if err := decoder.Decode(&payload); err != nil {
		return gatewayProbeOutput{}, fmt.Errorf("malformed_ssh_probe_output: %w", err)
	}
	if err := rejectTrailingProbeJSON(decoder); err != nil {
		return gatewayProbeOutput{}, err
	}
	if payload.Status == "" || payload.PeerID == "" || payload.KeyID == "" {
		return gatewayProbeOutput{}, fmt.Errorf("malformed_ssh_probe_output: missing fields")
	}
	if err := config.ValidateHostID(payload.PeerID); err != nil {
		return gatewayProbeOutput{}, fmt.Errorf("malformed_ssh_probe_output: invalid peer id")
	}
	if err := sshprovision.ValidateManagedAuthorizedKeyID(payload.KeyID); err != nil {
		return gatewayProbeOutput{}, fmt.Errorf("malformed_ssh_probe_output: invalid key id")
	}
	return payload, nil
}

func rejectTrailingProbeJSON(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("malformed_ssh_probe_output: trailing data")
		}
		return fmt.Errorf("malformed_ssh_probe_output: %w", err)
	}
	return nil
}
