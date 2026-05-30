package daemon

import (
	"context"
	"crypto/sha256"
	"fmt"
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

// pusher sends clipboard content to a peer host. *transport.Client satisfies
// it; the interface exists so fanout can be exercised with a fake in tests.
type pusher interface {
	PushAs(ctx context.Context, host string, port int, content clipboard.Content, origin string) error
}

type Daemon struct {
	cfg    *config.Config
	cb     clipboard.Backend
	disc   discovery.Discoverer
	auth   *transport.Auth
	cl     pusher
	sv     *transport.Server
	origin string

	mu     sync.Mutex
	seen   *seenSet
	lastTS time.Time

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
		seen:       newSeenSet(),
	}
	d.cl = transport.NewClient(auth, origin)
	d.sv = transport.NewServer(cfg.Listen, auth, d.onReceive, d.peersHandler)
	d.sv.SetHistory(
		func(limit int) (any, error) { return store.LoadHistory(limit) },
		d.Restore,
		store.SetPinned,
		func(id string, allUnpinned bool) error {
			if allUnpinned {
				return store.ClearUnpinned()
			}
			return store.DeleteEntry(id)
		},
	)
	return d, nil
}

// Origin is the short hostname used as this daemon's identity in envelopes.
func (d *Daemon) Origin() string { return d.origin }

// Snapshot merges discovered peers with per-peer push/receive stats so the
// menubar app sees both known peers and any seen-but-not-configured origins.
//
// Hostnames don't always match across the two sources: the configured peer
// list has "jesse-paradise-park" (tailnet) while a recv envelope arrives
// stamped "paradise-park" (the sender's `os.Hostname` short name). We
// reconcile by short-name matching so each real host shows up once.
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
		// `clipfan copy` injects with origin=self; the recv path then
		// records a peerStatus entry for our own short name. Filter it
		// out of the snapshot so the menubar doesn't list us as a peer.
		if hostsMatch(h, d.origin) {
			continue
		}
		target := h
		// If this origin maps to a discovered peer (same short name, or one is
		// a "<user>-<short>" tailnet variant of the other), fold stats into
		// the discovered entry rather than creating a duplicate row.
		for kh := range known {
			if hostsMatch(kh, h) {
				target = kh
				break
			}
		}
		k, ok := known[target]
		if !ok {
			k = &PeerState{Hostname: s.Hostname, Port: s.Port}
			known[target] = k
		}
		if !s.LastPushTS.IsZero() {
			k.LastPushTS = s.LastPushTS
			k.LastPushOK = s.LastPushOK
			k.LastPushErr = s.LastPushErr
		}
		if !s.LastRecvTS.IsZero() {
			k.LastRecvTS = s.LastRecvTS
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

func shortName(h string) string {
	h = strings.TrimSuffix(h, ".local")
	return strings.SplitN(h, ".", 2)[0]
}

// hostsMatch returns true if two hostnames almost certainly identify the
// same physical host. Exact short-name equality is the easy case; we also
// accept the "<user>-<short>" pattern Tailscale uses so e.g. the configured
// peer "jesse-paradise-park" gets reconciled with the recv origin
// "paradise-park". The minMatchLen floor prevents incidental suffix matches
// (e.g. "park" being treated as the same host as "paradise-park").
func hostsMatch(a, b string) bool {
	sa, sb := shortName(a), shortName(b)
	if sa == sb {
		return true
	}
	const minMatchLen = 6
	long, short := sb, sa
	if len(sa) > len(sb) {
		long, short = sa, sb
	}
	if len(short) < minMatchLen {
		return false
	}
	return strings.HasSuffix(long, "-"+short)
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
		d.seen.add(c.Hash)
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
	// A clipboard text that is one of our own image-store paths is the daemon's
	// representation of an image (written as text on backends that can't hold
	// images), not new user content. Never broadcast it — doing so demotes an
	// image to a path string that clobbers the real image on image-capable peers.
	// This is content-based, not hash-based, so it holds even when a trailing
	// newline or re-read makes the path's hash miss the echo guard.
	if c.Kind == clipboard.KindText && store.IsImageStorePath(string(c.Bytes)) {
		return
	}
	d.mu.Lock()
	if d.seen.has(c.Hash) {
		d.mu.Unlock()
		return
	}
	d.seen.add(c.Hash)
	d.lastTS = c.TS
	d.mu.Unlock()
	slog.Debug("local clip changed", "kind", c.Kind, "bytes", len(c.Bytes))
	if !c.Concealed {
		recImg := ""
		if c.Kind == clipboard.KindImage {
			if p, err := store.SaveImage(c.Bytes); err == nil {
				recImg = p
			}
		}
		if err := store.AppendHistory(c, d.origin, recImg); err != nil {
			slog.Debug("append history", "err", err)
		}
	}
	d.fanout(ctx, c, "" /* skipOrigin = none */)
}

func (d *Daemon) onReceive(c clipboard.Content, origin string) {
	// A text clip that is one of our own image-store paths is an image echoed
	// as a path string by a host that can't hold images on its clipboard.
	// Writing it would clobber the real image (which arrived separately as a
	// kind=image clip) with a useless path, and relaying it would spread the
	// clobber across the fleet. Drop it.
	if c.Kind == clipboard.KindText && store.IsImageStorePath(string(c.Bytes)) {
		return
	}

	// We intentionally do NOT short-circuit when origin == d.origin. That
	// path is reached by `clipfan copy` injecting into us as ourselves; we
	// want to treat it like any other clipboard change. Relay loops are
	// prevented by the hash dedup below, not by origin filtering.
	d.mu.Lock()
	if d.seen.has(c.Hash) {
		d.mu.Unlock()
		return
	}
	if !d.lastTS.IsZero() && c.TS.Before(d.lastTS) {
		d.mu.Unlock()
		return
	}
	d.seen.add(c.Hash)
	d.lastTS = c.TS
	d.mu.Unlock()

	d.recordRecv(origin)

	var imagePath string
	textPayload := c.Bytes
	state := store.State{Kind: "text", TS: c.TS}
	if c.Kind == clipboard.KindImage {
		p, err := store.SaveImage(c.Bytes)
		if err != nil {
			slog.Error("save image", "err", err)
			return
		}
		slog.Debug("saved image", "path", p, "bytes", len(c.Bytes))
		imagePath = p
		textPayload = []byte(p)
		state = store.State{Kind: "image", TS: c.TS, ImagePath: p}
	}

	if err := store.SaveState(state, textPayload); err != nil {
		slog.Warn("save state", "err", err)
	}

	if !c.Concealed {
		recImg := ""
		if c.Kind == clipboard.KindImage {
			recImg = imagePath
		}
		if err := store.AppendHistory(c, origin, recImg); err != nil {
			slog.Debug("append history", "err", err)
		}
	}

	if c.Kind == clipboard.KindImage {
		if err := d.cb.WriteImage(c.Bytes, imagePath); err != nil {
			slog.Warn("local clip write (image)", "err", err)
		}
	} else {
		if err := d.cb.WriteText(textPayload); err != nil {
			slog.Warn("local clip write (text)", "err", err)
		}
	}
	if err := tmux.LoadBufferAll(textPayload); err != nil {
		slog.Debug("tmux load-buffer", "err", err)
	}

	// Register the hash of exactly what we loaded into the tmux buffer. A
	// tmux after-set-buffer / after-load-buffer hook re-submits that content
	// through `clipfan copy`; registering it here dedups that echo regardless
	// of clipboard backend. This is the loop guard for the hook bridge — note
	// for an image, textPayload is the on-disk path (not the image bytes), so
	// its hash differs from c.Hash and must be registered explicitly.
	loaded := sha256.Sum256(textPayload)
	d.mu.Lock()
	d.seen.add(loaded)
	d.mu.Unlock()

	// Register what we just wrote so our own poll loop doesn't re-broadcast it.
	// On text-only backends WriteImage stores the on-disk path as text, so the
	// readback hash differs from the received image hash; remembering it
	// suppresses an echo of that path back into the mesh.
	if readback, err := d.cb.Read(); err == nil && len(readback.Bytes) > 0 {
		d.mu.Lock()
		d.seen.add(readback.Hash)
		d.mu.Unlock()
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
		if p.Self {
			continue
		}
		if skipOrigin != "" && hostsMatch(p.Hostname, skipOrigin) {
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

// Restore makes the history entry with the given id the current clipboard:
// it writes the local OS clipboard, re-records it in history (floating it to
// the top), and fanouts to peers so the fleet converges.
func (d *Daemon) Restore(id string) error {
	e, ok, err := store.EntryByID(id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("history entry %s not found", id)
	}

	var c clipboard.Content
	if e.Kind == "image" {
		body, err := os.ReadFile(e.ImagePath)
		if err != nil {
			return fmt.Errorf("read image %s: %w", e.ImagePath, err)
		}
		c = clipboard.New(clipboard.KindImage, body, time.Now().UTC())
		if err := d.cb.WriteImage(body, e.ImagePath); err != nil {
			slog.Error("restore write image", "err", err)
		}
	} else {
		c = clipboard.New(clipboard.KindText, []byte(e.Text), time.Now().UTC())
		if err := d.cb.WriteText([]byte(e.Text)); err != nil {
			slog.Error("restore write text", "err", err)
		}
	}

	d.mu.Lock()
	d.seen.add(c.Hash)
	d.lastTS = c.TS
	d.mu.Unlock()

	recImg := ""
	if e.Kind == "image" {
		recImg = e.ImagePath
	}
	if err := store.AppendHistory(c, d.origin, recImg); err != nil {
		slog.Debug("restore append history", "err", err)
	}

	d.fanout(context.Background(), c, "")
	return nil
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
