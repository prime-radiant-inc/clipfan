package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
	"github.com/prime-radiant-inc/clipfan/internal/version"
)

var ErrSSHGatewayCommandRejected = errors.New("ssh_gateway_command_rejected")

func RunSSHGateway(args []string, stdout io.Writer, stderr io.Writer) error {
	return runSSHGateway(args, stdout, stderr, os.Getenv)
}

func runSSHGateway(args []string, stdout io.Writer, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("ssh-gateway", flag.ContinueOnError)
	fs.SetOutput(stderr)
	peerID := fs.String("authorized-peer", "", "authorized peer id")
	keyID := fs.String("authorized-key-id", "", "authorized key id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected ssh-gateway argument")
	}
	if err := config.ValidateHostID(*peerID); err != nil {
		return fmt.Errorf("invalid authorized peer: %w", err)
	}
	if err := sshprovision.ValidateManagedAuthorizedKeyID(*keyID); err != nil {
		return fmt.Errorf("invalid authorized key id: %w", err)
	}

	switch getenv("SSH_ORIGINAL_COMMAND") {
	case sshprovision.SSHGatewayProbeCommand:
		return json.NewEncoder(stdout).Encode(map[string]string{
			"status":  "ok",
			"peer_id": *peerID,
			"key_id":  *keyID,
			"version": version.Version,
		})
	default:
		return ErrSSHGatewayCommandRejected
	}
}
