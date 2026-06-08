package daemon

import (
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/version"
)

// FleetSnapshot is one host's redacted self-report of its mesh edges, shipped
// off-box to peers over the pinned-sync-key SSH `fleet-snapshot` gateway verb and
// aggregated by each peer's daemon into a fleet-wide view. It is an explicit
// allowlist: it carries paths-free public identifiers and live edge status only —
// never the shared key, key material, the proof key ids, or the sync-key path.
type FleetSnapshot struct {
	Origin  string              `json:"origin"`
	Version string              `json:"version"`
	Peers   []FleetSnapshotPeer `json:"peers"`
}

// FleetSnapshotPeer is the reporting host's view of one edge: the static edge
// description from config plus the live session status the running daemon tracks.
// migration_state and ssh_active always serialize (a false/empty value is
// meaningful to the fleet view). ssh_host/port/user are omitempty (an accept-only
// edge carries none). The timestamps are zero-valued ("never") when a session has
// not reached that milestone; omitempty is a no-op on time.Time, so they serialize
// as the zero time, matching the PeerState convention the Mac already decodes.
type FleetSnapshotPeer struct {
	ID               string    `json:"id"`
	SSHHost          string    `json:"ssh_host,omitempty"`
	SSHPort          int       `json:"ssh_port,omitempty"`
	SSHUser          string    `json:"ssh_user,omitempty"`
	MigrationState   string    `json:"migration_state"`
	SSHStatus        string    `json:"ssh_status,omitempty"`
	SSHActive        bool      `json:"ssh_active"`
	LastRecvTS       time.Time `json:"last_recv_ts,omitempty"`
	SSHLastAckTS     time.Time `json:"ssh_last_ack_ts,omitempty"`
	SSHLastConnectTS time.Time `json:"ssh_last_connect_ts,omitempty"`
}

// BuildFleetSnapshot assembles a host's snapshot from its config (the authoritative
// roster: edge identity + migration state) overlaid with the live PeerState rows
// the daemon tracks (matched by id, since PeerState.Hostname is the peer id for SSH
// peers). A configured peer with no live row still appears, so an edge that has not
// connected yet is visible rather than silently absent. origin is supplied by the
// caller (the daemon's resolved origin, or the gateway handler's validated local id)
// rather than read from cfg.Hostname, which an unprovisioned config may leave empty.
func BuildFleetSnapshot(cfg *config.Config, origin string, peers []PeerState) FleetSnapshot {
	snapshot := FleetSnapshot{
		Origin:  origin,
		Version: version.Version,
		Peers:   []FleetSnapshotPeer{},
	}
	if cfg == nil {
		return snapshot
	}

	live := make(map[string]PeerState, len(peers))
	for _, p := range peers {
		live[p.Hostname] = p
	}

	if cfg.SSH == nil {
		return snapshot
	}
	for _, peer := range cfg.SSH.Peers {
		row := FleetSnapshotPeer{
			ID:             peer.ID,
			SSHHost:        peer.SSHHost,
			SSHPort:        peer.SSHPort,
			SSHUser:        peer.SSHUser,
			MigrationState: string(peer.MigrationState),
		}
		if state, ok := live[peer.ID]; ok {
			row.SSHStatus = state.SSHStatus
			row.SSHActive = state.SSHActive
			row.LastRecvTS = state.LastRecvTS
			row.SSHLastAckTS = state.SSHLastAckTS
			row.SSHLastConnectTS = state.SSHLastConnectTS
		}
		snapshot.Peers = append(snapshot.Peers, row)
	}
	return snapshot
}
