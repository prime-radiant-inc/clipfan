package daemon

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/discovery"
	"github.com/prime-radiant-inc/clipfan/internal/tmux"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
)

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
		cfg:    cfg,
		cb:     clipboard.NewBackend(),
		disc:   disc,
		auth:   auth,
		origin: origin,
	}
	d.cl = transport.NewClient(auth, origin)
	d.sv = transport.NewServer(cfg.Listen, auth, d.onReceive)
	return d, nil
}

func (d *Daemon) Run(ctx context.Context) error {
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- d.sv.Serve(ctx)
	}()

	// Seed lastHash with whatever the local clipboard already holds so we
	// don't broadcast on first tick.
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
	d.broadcast(ctx, c)
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
	if err := d.cb.Write(c); err != nil {
		slog.Warn("local clip write", "err", err)
	}
	if c.Kind == clipboard.KindText {
		if err := tmux.LoadBufferAll(c.Bytes); err != nil {
			slog.Debug("tmux load-buffer", "err", err)
		}
	}
}

func (d *Daemon) broadcast(ctx context.Context, c clipboard.Content) {
	peers, err := d.disc.Peers(ctx)
	if err != nil {
		slog.Warn("discovery", "err", err)
		return
	}
	for _, p := range peers {
		if p.Self {
			continue
		}
		p := p
		go func() {
			pushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if err := d.cl.Push(pushCtx, p.Hostname, p.Port, c); err != nil {
				slog.Debug("push", "host", p.Hostname, "err", err)
			}
		}()
	}
}
