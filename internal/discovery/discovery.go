package discovery

import "context"

type Peer struct {
	Hostname string
	Port     int
	Self     bool
}

type Discoverer interface {
	Peers(ctx context.Context) ([]Peer, error)
}
