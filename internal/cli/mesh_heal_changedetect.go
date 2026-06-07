package cli

import "github.com/prime-radiant-inc/clipfan/internal/config"

// edgePeerView is one end's record of a mesh edge: what one host's config says
// about its peer at the other end. It is projected from that host's roster-read
// report, carrying only the fields that decide whether the directed link is
// fully provisioned.
type edgePeerView struct {
	Found          bool
	Enabled        bool
	Accept         bool
	Connect        bool
	MigrationState string
	AcceptKeyID    string
	ConnectKeyID   string
}

// edgeIsHealthy reports whether the bidirectional edge between two hosts is
// fully provisioned and needs no repair, given each host's own view of it. Both
// ends must independently be present, enabled, accepting, connecting, in the
// ssh_keys_ready state, and carry non-empty accept/connect key ids.
//
// This is a presence check, not a rotation check: a stale-but-present key id
// reads as healthy. Key rotation is out of scope per the spec.
func edgeIsHealthy(a, b edgePeerView) bool {
	return endIsHealthy(a) && endIsHealthy(b)
}

func endIsHealthy(v edgePeerView) bool {
	return v.Found &&
		v.Enabled &&
		v.Accept &&
		v.Connect &&
		v.MigrationState == config.MigrationStateSSHKeysReady &&
		v.AcceptKeyID != "" &&
		v.ConnectKeyID != ""
}
