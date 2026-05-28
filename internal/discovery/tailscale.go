package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

type Tailscale struct {
	port int
}

func NewTailscale(port int) *Tailscale { return &Tailscale{port: port} }

type tsPeer struct {
	HostName string `json:"HostName"`
	Online   bool   `json:"Online"`
}

type tsStatus struct {
	Self *tsPeer            `json:"Self"`
	Peer map[string]*tsPeer `json:"Peer"`
}

func (t *Tailscale) Peers(ctx context.Context) ([]Peer, error) {
	out, err := exec.CommandContext(ctx, "tailscale", "status", "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("tailscale status: %w", err)
	}
	var s tsStatus
	if err := json.Unmarshal(out, &s); err != nil {
		return nil, fmt.Errorf("parse tailscale status: %w", err)
	}
	peers := make([]Peer, 0, len(s.Peer)+1)
	if s.Self != nil {
		peers = append(peers, Peer{Hostname: shortName(s.Self.HostName), Port: t.port, Self: true})
	}
	for _, p := range s.Peer {
		if p == nil || !p.Online {
			continue
		}
		peers = append(peers, Peer{Hostname: shortName(p.HostName), Port: t.port, Self: false})
	}
	return peers, nil
}
