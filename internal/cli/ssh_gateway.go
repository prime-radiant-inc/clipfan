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

type SSHGatewayIdentity struct {
	PeerID string
	KeyID  string
}

type SSHGatewayHandlers struct {
	Probe      func(SSHGatewayIdentity, io.Writer) error
	SyncStream func(SSHGatewayIdentity, io.Writer) error
}

func RunSSHGateway(args []string, stdout io.Writer, stderr io.Writer) error {
	return runSSHGateway(args, stdout, stderr, os.Getenv)
}

func runSSHGateway(args []string, stdout io.Writer, stderr io.Writer, getenv func(string) string) error {
	return runSSHGatewayWithHandlers(args, stdout, stderr, getenv, defaultSSHGatewayHandlers())
}

func runSSHGatewayWithHandlers(args []string, stdout io.Writer, stderr io.Writer, getenv func(string) string, handlers SSHGatewayHandlers) error {
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
	identity := SSHGatewayIdentity{PeerID: *peerID, KeyID: *keyID}

	switch getenv("SSH_ORIGINAL_COMMAND") {
	case sshprovision.SSHGatewayProbeCommand:
		if handlers.Probe == nil {
			return ErrSSHGatewayCommandRejected
		}
		return handlers.Probe(identity, stdout)
	case sshprovision.SSHGatewaySyncStreamCommand:
		if handlers.SyncStream == nil {
			return ErrSSHGatewayCommandRejected
		}
		return handlers.SyncStream(identity, stdout)
	default:
		return ErrSSHGatewayCommandRejected
	}
}

func defaultSSHGatewayHandlers() SSHGatewayHandlers {
	return SSHGatewayHandlers{
		Probe: func(identity SSHGatewayIdentity, stdout io.Writer) error {
			return json.NewEncoder(stdout).Encode(map[string]string{
				"status":  "ok",
				"peer_id": identity.PeerID,
				"key_id":  identity.KeyID,
				"version": version.Version,
			})
		},
	}
}
