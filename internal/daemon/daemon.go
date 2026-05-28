package daemon

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/discovery"
	"github.com/prime-radiant-inc/clipfan/internal/store"
	"github.com/prime-radiant-inc/clipfan/internal/tmux"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
)

// PeerState is the daemon-tracked status of a single peer, exposed via
// GET /v1/peers and consumed by the menubar app.
type PeerState struct {
	Hostname    string    `json:"hostname"`
	Port        int       `json:"port"`
	LastPushTS  time.Time `json:"last_push_ts,omitempty"`
	LastPushOK  bool      `json:"last_push_ok"`
	LastPushErr string    `json:"last_push_err,omitempty"`
	LastRecvTS  time.Time `json:"last_recv_ts,omitempty"`
}

type Daemon struct {
	cfg    *config.Config
	cb     clipboard.Backend
	disc   discovery.Discoverer
	auth   *transport.Auth
	cl     *transport.Client
	sv     *transport.Server
	origin string

	mu       sync.Mutex
	lastHash [32]byte
	lastTS   time.Time

	peersMu    sync.RWMutex
	peerStatus map[string]*PeerState
}

func New(cfg *config.Config) (*Daemon, error) {
	auth, err := transport.NewAuth(cfg.SharedKey)
	if err != nil {
		return nil, err
	}

	var disc discovery.Discoverer
	switch cfg.Discovery {
	case "static":
		disc = discovery.NewStatic(cfg.StaticPeers, cfg.Port)
	default:
		disc = discovery.NewTailscale(cfg.Port)
	}

	origin := cfg.Hostname
	if origin == "" {
		h, _ := os.Hostname()
		origin = strings.TrimSuffix(strings.SplitN(h, ".", 2)[0], ".local")
	}

	d := &Daemon{
		cfg:        cfg,
		cb:         clipboard.NewBackend(),
		disc:       disc,
		auth:       auth,
		origin:     origin,
		peerStatus: map[string]*PeerState{},
	}
	d.cl = transport.NewClient(auth, origin)
	d.sv = transport.NewServer(cfg.Listen, auth, d.onReceive, d.peersHandler)
	return d, nil
}

// Origin is the short hostname used as this daemon's identity in envelopes.
func (d *Daemon) Origin() string { return d.origin }

// Snapshot merges discovered peers with per-peer push/receive stats so the
// menubar app sees both known peers and any seen-but-not-configured origins.
func (d *Daemon) Snapshot(ctx context.Context) []PeerState {
	known := map[string]*PeerState{}

	if peers, err := d.disc.Peers(ctx); err == nil {
		for _, p := range peers {
			if p.Self {
				continue
			}
			known[p.Hostname] = &PeerState{Hostname: p.Hostname, Port: p.Port}
		}
	}

	d.peersMu.RLock()
	for h, s := range d.peerStatus {
		if k, ok := known[h]; ok {
			// merge stats over the discovered peer
			k.LastPushTS, k.LastPushOK, k.LastPushErr, k.LastRecvTS = s.LastPushTS, s.LastPushOK, s.LastPushErr, s.LastRecvTS
		} else {
			known[h] = &PeerState{
				Hostname:    s.Hostname,
				Port:        s.Port,
				LastPushTS:  s.LastPushTS,
				LastPushOK:  s.LastPushOK,
				LastPushErr: s.LastPushErr,
				LastRecvTS:  s.LastRecvTS,
			}
		}
	}
	d.peersMu.RUnlock()

	out := make([]PeerState, 0, len(known))
	for _, s := range known {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hostname < out[j].Hostname })
	return out
}

func (d *Daemon) peersHandler() any {
	return map[string]any{
		"origin": d.origin,
		"peers":  d.Snapshot(context.Background()),
	}
}

func (d *Daemon) Run(ctx context.Context) error {
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- d.sv.Serve(ctx)
	}()

	if c, err := d.cb.Read(); err == nil && len(c.Bytes) > 0 {
		d.mu.Lock()
		d.lastHash = c.Hash
		d.lastTS = c.TS
		d.mu.Unlock()
	}

	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return <-serverErr
		case err := <-serverErr:
			return err
		case <-tick.C:
			d.pollOnce(ctx)
		}
	}
}

func (d *Daemon) pollOnce(ctx context.Context) {
	c, err := d.cb.Read()
	if err != nil || len(c.Bytes) == 0 {
		return
	}
	d.mu.Lock()
	if bytes.Equal(c.Hash[:], d.lastHash[:]) {
		d.mu.Unlock()
		return
	}
	d.lastHash = c.Hash
	d.lastTS = c.TS
	d.mu.Unlock()
	slog.Debug("local clip changed", "kind", c.Kind, "bytes", len(c.Bytes))
	d.fanout(ctx, c, "" /* skipOrigin = none */)
}

func (d *Daemon) onReceive(c clipboard.Content, origin string) {
	if origin == d.origin {
		return
	}
	d.mu.Lock()
	if bytes.Equal(c.Hash[:], d.lastHash[:]) {
		d.mu.Unlock()
		return
	}
	if !d.lastTS.IsZero() && c.TS.Before(d.lastTS) {
		d.mu.Unlock()
		return
	}
	d.lastHash = c.Hash
	d.lastTS = c.TS
	d.mu.Unlock()

	d.recordRecv(origin)

	textPayload := c
	state := store.State{Kind: "text", TS: c.TS}
	if c.Kind == clipboard.KindImage {
		path, err := store.SaveImage(c.Bytes)
		if err != nil {
			slog.Error("save image", "err", err)
			return
		}
		slog.Debug("saved image", "path", path, "bytes", len(c.Bytes))
		textPayload = clipboard.New(clipboard.KindText, []byte(path), c.TS)
		state = store.State{Kind: "image", TS: c.TS, ImagePath: path}
	}

	if err := store.SaveState(state, textPayload.Bytes); err != nil {
		slog.Warn("save state", "err", err)
	}
	if err := d.cb.Write(textPayload); err != nil {
		slog.Warn("local clip write", "err", err)
	}
	if err := tmux.LoadBufferAll(textPayload.Bytes); err != nil {
		slog.Debug("tmux load-buffer", "err", err)
	}

	// Relay: re-broadcast to every peer except the origin so disjoint peers
	// (e.g. flower-garden on LAN, paradise-park on tailnet) still converge
	// through the Mac hub. Echo-loop prevention is the hash dedup above.
	go d.fanout(context.Background(), c, origin)
}

// fanout pushes content to every discovered peer except `skipOrigin`. When
// skipOrigin is empty this is a fresh broadcast (stamps our own origin).
// When non-empty this is a relay (stamps the original origin).
func (d *Daemon) fanout(ctx context.Context, c clipboard.Content, skipOrigin string) {
	peers, err := d.disc.Peers(ctx)
	if err != nil {
		slog.Warn("discovery", "err", err)
		return
	}
	originStamp := d.origin
	if skipOrigin != "" {
		originStamp = skipOrigin
	}
	for _, p := range peers {
		if p.Self || p.Hostname == skipOrigin {
			continue
		}
		p := p
		go func() {
			pushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			err := d.cl.PushAs(pushCtx, p.Hostname, p.Port, c, originStamp)
			d.recordPush(p, err)
			if err != nil {
				slog.Debug("push", "host", p.Hostname, "err", err)
			}
		}()
	}
}

func (d *Daemon) recordPush(p discovery.Peer, err error) {
	d.peersMu.Lock()
	defer d.peersMu.Unlock()
	s, ok := d.peerStatus[p.Hostname]
	if !ok {
		s = &PeerState{Hostname: p.Hostname, Port: p.Port}
		d.peerStatus[p.Hostname] = s
	}
	s.LastPushTS = time.Now().UTC()
	if err != nil {
		s.LastPushOK = false
		s.LastPushErr = err.Error()
	} else {
		s.LastPushOK = true
		s.LastPushErr = ""
	}
}

func (d *Daemon) recordRecv(origin string) {
	d.peersMu.Lock()
	defer d.peersMu.Unlock()
	s, ok := d.peerStatus[origin]
	if !ok {
		s = &PeerState{Hostname: origin}
		d.peerStatus[origin] = s
	}
	s.LastRecvTS = time.Now().UTC()
}
