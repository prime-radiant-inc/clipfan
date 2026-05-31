package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

type Tailscale struct {
	port    int
	allowed map[string]bool
}

func NewTailscale(port int, allowedHosts []string) *Tailscale {
	return &Tailscale{port: port, allowed: allowedHostSet(allowedHosts)}
}

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
	return parseTailscalePeers(out, t.port, t.allowedHosts())
}

func parseTailscalePeers(out []byte, port int, allowedHosts []string) ([]Peer, error) {
	allowed := allowedHostSet(allowedHosts)
	var s tsStatus
	if err := json.Unmarshal(out, &s); err != nil {
		return nil, fmt.Errorf("parse tailscale status: %w", err)
	}
	peers := make([]Peer, 0, len(s.Peer)+1)
	if s.Self != nil {
		peers = append(peers, Peer{Hostname: shortName(s.Self.HostName), Port: port, Self: true})
	}
	for _, p := range s.Peer {
		if p == nil || !p.Online {
			continue
		}
		host := shortName(p.HostName)
		if !allowed[host] {
			continue
		}
		peers = append(peers, Peer{Hostname: host, Port: port, Self: false})
	}
	return peers, nil
}

func (t *Tailscale) allowedHosts() []string {
	out := make([]string, 0, len(t.allowed))
	for h := range t.allowed {
		out = append(out, h)
	}
	return out
}
