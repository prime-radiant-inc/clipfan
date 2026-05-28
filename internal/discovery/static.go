package discovery

import (
	"context"
	"os"
	"strings"
)

type Static struct {
	hosts []string
	port  int
	self  string
}

func NewStatic(hosts []string, port int) *Static {
	self, _ := os.Hostname()
	return &Static{hosts: hosts, port: port, self: shortName(self)}
}

func (s *Static) Peers(ctx context.Context) ([]Peer, error) {
	out := make([]Peer, 0, len(s.hosts))
	for _, h := range s.hosts {
		out = append(out, Peer{
			Hostname: h,
			Port:     s.port,
			Self:     shortName(h) == s.self,
		})
	}
	return out, nil
}

func shortName(h string) string {
	h = strings.TrimSuffix(h, ".local")
	return strings.SplitN(h, ".", 2)[0]
}
