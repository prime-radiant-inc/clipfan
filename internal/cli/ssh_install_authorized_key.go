package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

type sshAuthorizedKeyUpsert func(string, sshprovision.ManagedAuthorizedKey) (bool, error)

func RunSSHInstallAuthorizedKey(args []string, stdout io.Writer, stderr io.Writer) error {
	return runSSHInstallAuthorizedKey(args, stdout, stderr, os.UserHomeDir, sshprovision.UpsertManagedAuthorizedKeyFile)
}

func runSSHInstallAuthorizedKey(args []string, stdout io.Writer, stderr io.Writer, userHomeDir func() (string, error), upsert sshAuthorizedKeyUpsert) error {
	fs := flag.NewFlagSet("ssh-install-authorized-key", flag.ContinueOnError)
	fs.SetOutput(stderr)
	peerID := fs.String("peer", "", "authorized peer id")
	keyID := fs.String("key-id", "", "authorized key id")
	gatewayPath := fs.String("gateway-path", "", "gateway path")
	publicKey := fs.String("public-key", "", "base64 ed25519 public key blob")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected ssh-install-authorized-key argument")
	}
	if userHomeDir == nil {
		return fmt.Errorf("missing user home resolver")
	}
	if upsert == nil {
		return fmt.Errorf("missing authorized key upsert")
	}
	entry, err := sshprovision.NewManagedAuthorizedKey(sshprovision.ManagedAuthorizedKey{
		PeerID:      *peerID,
		KeyID:       *keyID,
		GatewayPath: *gatewayPath,
		PublicKey:   *publicKey,
	})
	if err != nil {
		return err
	}
	homeDir, err := userHomeDir()
	if err != nil {
		return fmt.Errorf("user home: %w", err)
	}
	changed, err := upsert(homeDir, entry)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(map[string]any{
		"status":  "ok",
		"changed": changed,
		"peer_id": entry.PeerID,
		"key_id":  entry.KeyID,
	})
}
