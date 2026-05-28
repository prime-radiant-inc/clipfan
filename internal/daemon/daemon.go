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
	"github.com/prime-radiant-inc/clipfan/internal/store"
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

	// For images, materialize to a file under XDG_STATE_HOME and put the
	// path on the (text) clipboard. This is the load-bearing trick that
	// makes Codex and Claude Code attach the image via bracketed paste
	// without any X server or xclip dependency.
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

	// Persist for the xclip/wl-paste shim before touching the OS clipboard
	// so the shim never sees a stale image_path for the current state.
	if err := store.SaveState(state, textPayload.Bytes); err != nil {
		slog.Warn("save state", "err", err)
	}

	if err := d.cb.Write(textPayload); err != nil {
		slog.Warn("local clip write", "err", err)
	}
	if err := tmux.LoadBufferAll(textPayload.Bytes); err != nil {
		slog.Debug("tmux load-buffer", "err", err)
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
