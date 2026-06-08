package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/daemon"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
)

// runDefaultSSHGatewayFleetSnapshot answers the read-only `fleet-snapshot` gateway
// verb: it authorizes the calling peer, reads this host's live peer status from the
// local daemon over signed loopback, and writes a redacted FleetSnapshot. The
// snapshot carries no secrets (BuildFleetSnapshot is an explicit allowlist), so a
// peer learns only mesh topology and edge status it is already entitled to.
func runDefaultSSHGatewayFleetSnapshot(identity SSHGatewayIdentity, stdout io.Writer) error {
	if _, err := os.Stat(config.Path()); err != nil {
		// Opaque to the peer: this runs as an sshd forced command, so the error reaches
		// the caller's stderr — don't leak the config path or stat details.
		return ErrSSHGatewayCommandRejected
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	origin, err := sshGatewayLocalID(cfg)
	if err != nil {
		return err
	}
	if err := validateSSHGatewayFleetSnapshotPeer(cfg, identity); err != nil {
		return err
	}
	localHost, localPort, err := sshGatewayLocalDaemonTarget(cfg)
	if err != nil {
		return err
	}
	auth, err := transport.NewAuth(cfg.SharedKey)
	if err != nil {
		return err
	}
	client := transport.NewClient(auth)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body, err := client.Peers(ctx, localHost, localPort)
	if err != nil {
		return err
	}
	var payload struct {
		Peers []daemon.PeerState `json:"peers"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(daemon.BuildFleetSnapshot(cfg, origin, payload.Peers))
}

// validateSSHGatewayFleetSnapshotPeer authorizes a peer to read this host's fleet
// snapshot. The snapshot is read-only and redacted, but it reveals mesh topology,
// so it is deliberately granted NO MORE ACCESS THAN THE SYNC STREAM: the peer must
// satisfy the full sync predicate (enabled, accept, connect, persistent,
// ssh_keys_ready, and the proof accept-key id matching the presented key). The
// daemon aggregator only ever gathers from peers that already meet this bar, so the
// conservative predicate rejects no legitimate caller. Delegating to
// validateSSHGatewaySyncPeer keeps the two in lockstep by construction.
func validateSSHGatewayFleetSnapshotPeer(cfg *config.Config, identity SSHGatewayIdentity) error {
	return validateSSHGatewaySyncPeer(cfg, identity)
}
