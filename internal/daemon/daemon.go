package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/discovery"
	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
	"github.com/prime-radiant-inc/clipfan/internal/store"
	"github.com/prime-radiant-inc/clipfan/internal/tmux"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
	"github.com/prime-radiant-inc/clipfan/internal/version"
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
	cfg              *config.Config
	cb               clipboard.Backend
	disc             discovery.Discoverer
	auth             *transport.Auth
	cl               pusher
	sv               *transport.Server
	serve            func(context.Context) error
	serveListener    func(context.Context, net.Listener) error
	origin           string
	storagePreflight StoragePreflightPolicy
	listenerPlan     config.ListenerPlan
	stateDir         string
	configPath       string
	peerHTTPDisabled bool

	mu      sync.Mutex
	seen    *seenSet
	lastTS  time.Time
	current currentClip

	peersMu    sync.RWMutex
	peerStatus map[string]*PeerState
}

// currentClip is the daemon's record of what it last wrote to the local
// clipboard, so pollOnce can recognise echoes of our own write — even when the
// content comes back re-represented (an image read back as its store path).
type currentClip struct {
	id        string
	kind      clipboard.Kind
	hash      [32]byte // canonical bytes hash (text bytes, or image bytes)
	imagePath string   // set for image clips
}

func New(cfg *config.Config) (*Daemon, error) {
	return NewWithOptions(cfg, Options{StoragePreflight: DefaultStoragePreflightPolicy()})
}

type Options struct {
	StoragePreflight        StoragePreflightPolicy
	ListenerBoundaryEnabled *bool
	PeerHTTPRuntimeDisabled *bool
}

func NewWithOptions(cfg *config.Config, opts Options) (*Daemon, error) {
	auth, err := transport.NewAuth(cfg.SharedKey)
	if err != nil {
		return nil, err
	}
	listenerBoundaryEnabled := config.GeneratedLoopbackDefaultsEnabled()
	if opts.ListenerBoundaryEnabled != nil {
		listenerBoundaryEnabled = *opts.ListenerBoundaryEnabled
	}
	peerHTTPDisabled := releaseflags.PeerHTTPRuntimeDisabled
	if opts.PeerHTTPRuntimeDisabled != nil {
		peerHTTPDisabled = *opts.PeerHTTPRuntimeDisabled
	}
	if cfg.Transport == config.TransportSSH {
		peerHTTPDisabled = true
	}
	if err := config.ValidateSSHTransportConfig(*cfg); err != nil {
		return nil, err
	}
	stateDir := opts.StoragePreflight.StateRoot
	if stateDir == "" {
		stateDir = config.StateDir()
	}
	listenerPlan := config.PlanListener(*cfg, listenerBoundaryEnabled)
	runtimeCfg := *cfg
	if !listenerPlan.SafeMode {
		runtimeCfg.Listen = listenerPlan.BindListen
	}

	var disc discovery.Discoverer
	switch runtimeCfg.Discovery {
	case "static":
		disc = discovery.NewStatic(runtimeCfg.StaticPeers, runtimeCfg.Port)
	default:
		disc = discovery.NewTailscale(runtimeCfg.Port, runtimeCfg.StaticPeers)
	}

	origin := runtimeCfg.Hostname
	if origin == "" {
		h, _ := os.Hostname()
		origin = strings.TrimSuffix(strings.SplitN(h, ".", 2)[0], ".local")
	}

	d := &Daemon{
		cfg:              &runtimeCfg,
		cb:               clipboard.NewBackend(),
		disc:             disc,
		auth:             auth,
		origin:           origin,
		storagePreflight: opts.StoragePreflight,
		listenerPlan:     listenerPlan,
		stateDir:         stateDir,
		configPath:       config.Path(),
		peerHTTPDisabled: peerHTTPDisabled,
		peerStatus:       map[string]*PeerState{},
		seen:             newSeenSet(),
	}
	d.cl = transport.NewClientWithPeerHTTPRuntimeDisabled(auth, origin, peerHTTPDisabled)
	d.sv = transport.NewServer(listenerPlan.BindListen, auth, d.onReceive, d.peersHandler)
	if runtimeCfg.ConfigVersion != nil && *runtimeCfg.ConfigVersion >= 2 {
		d.sv.SetRequiredLocalAuthVersion(transport.AuthVersionRequestHMAC)
	}
	d.sv.SetSafeMode(listenerPlan.SafeMode)
	d.sv.SetSafeModeInfo(transport.SafeModeInfo{
		Origin:                origin,
		Hostname:              origin,
		ConfiguredListen:      listenerPlan.ConfiguredListen,
		EffectiveRepairListen: listenerPlan.EffectiveRepairListen,
		ParseError:            listenerPlan.ParseError,
		PeerSyncStarted:       listenerPlan.PeerSyncStarted,
		ConfigVersion:         runtimeCfg.ConfigVersion,
		ConfigRevision:        runtimeCfg.ConfigRevision,
		Port:                  runtimeCfg.Port,
		StaticPeers:           runtimeCfg.StaticPeers,
	})
	d.sv.SetRecipientIdentity(origin)
	d.sv.SetVersionFunc(d.versionHandler)
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
	d.sv.SetConfigFunc(d.setMaxHistory)
	d.sv.SetListenerRepair(d.listenerRepairStatusHandler, d.listenerRepairPatchHandler)
	d.serveListener = d.sv.ServeListener
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
	return transport.ShortName(h)
}

// hostsMatch returns true if two hostnames almost certainly identify the
// same physical host. Exact short-name equality is the easy case; we also
// accept the "<user>-<short>" pattern Tailscale uses so e.g. the configured
// peer "jesse-paradise-park" gets reconciled with the recv origin
// "paradise-park". The minMatchLen floor prevents incidental suffix matches
// (e.g. "park" being treated as the same host as "paradise-park").
func hostsMatch(a, b string) bool {
	return transport.HostsMatch(a, b)
}

func (d *Daemon) peersHandler() any {
	return map[string]any{
		"origin":      d.origin,
		"peers":       d.Snapshot(context.Background()),
		"version":     version.Version,
		"max_history": store.CapLimit(),
	}
}

func (d *Daemon) versionHandler() any {
	return map[string]string{"version": version.Version}
}

// setMaxHistory persists a new history cap. Values are clamped to [50, 5000];
// a non-positive request is rejected. After saving, excess history is trimmed.
func (d *Daemon) setMaxHistory(n int) error {
	if err := d.storagePreflight.check(); err != nil {
		return err
	}
	if n <= 0 {
		return fmt.Errorf("max_history must be positive, got %d", n)
	}
	if n < 50 {
		n = 50
	}
	if n > 5000 {
		n = 5000
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.MaxHistory = n
	if err := config.Save(cfg); err != nil {
		return err
	}
	return store.Recap()
}

func (d *Daemon) Run(ctx context.Context) error {
	if err := d.storagePreflight.check(); err != nil {
		return err
	}
	lock, err := acquireDaemonLock(d.stateDir)
	if err != nil {
		return err
	}
	defer lock.release()
	if err := lock.writeDiagnostics(daemonLockDiagnostics{
		PID:           os.Getpid(),
		StartedAt:     time.Now().UTC().Format(time.RFC3339),
		ConfigPath:    d.configPath,
		StateDir:      d.stateDir,
		Listen:        d.listenerPlan.BindListen,
		DaemonVersion: version.Version,
		Hostname:      d.origin,
	}); err != nil {
		return err
	}
	serverErr, err := d.startServer(ctx)
	if err != nil {
		return err
	}
	if d.listenerPlan.SafeMode {
		select {
		case <-ctx.Done():
			return <-serverErr
		case err := <-serverErr:
			return err
		}
	}

	if c, err := d.cb.Read(); err == nil && len(c.Bytes) > 0 {
		d.mu.Lock()
		d.lastTS = c.TS
		if c.Kind == clipboard.KindImage {
			d.current = currentClip{id: "startup", kind: clipboard.KindImage, hash: c.Hash}
		} else {
			d.current = currentClip{id: "startup", kind: clipboard.KindText, hash: c.Hash}
		}
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

func (d *Daemon) startServer(ctx context.Context) (<-chan error, error) {
	serverErr := make(chan error, 1)
	if d.serve != nil {
		go func() {
			serverErr <- d.normalizeRunError(d.serve(ctx))
		}()
		return serverErr, nil
	}
	if d.serveListener != nil {
		ln, err := net.Listen("tcp", d.listenerPlan.BindListen)
		if err != nil {
			return nil, d.normalizeRunError(err)
		}
		go func() {
			serverErr <- d.normalizeRunError(d.serveListener(ctx, ln))
		}()
		return serverErr, nil
	}
	return nil, fmt.Errorf("daemon serve function is not configured")
}

func (d *Daemon) pollOnce(ctx context.Context) {
	if d.listenerPlan.SafeMode {
		return
	}
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
	if d.isEcho(c) {
		return
	}
	c.ID = transport.NewClipID()
	if c.ID == "" {
		slog.Warn("could not mint clip ID; skipping broadcast")
		return
	}
	d.mu.Lock()
	d.seen.add(c.ID)
	d.lastTS = c.TS
	d.mu.Unlock()
	slog.Debug("local clip changed", "id", c.ID, "kind", c.Kind, "bytes", len(c.Bytes))
	imagePath := ""
	if !c.Concealed {
		if c.Kind == clipboard.KindImage {
			if p, err := store.SaveImage(c.Bytes); err == nil {
				imagePath = p
			}
		}
		if err := store.AppendHistory(c, d.origin, imagePath); err != nil {
			slog.Debug("append history", "err", err)
		}
	}
	// Adopt the processed local clip so the next poll of the unchanged
	// clipboard is recognised as an echo, not processed again.
	d.mu.Lock()
	d.current = currentClip{id: c.ID, kind: c.Kind, hash: c.Hash, imagePath: imagePath}
	d.mu.Unlock()
	if c.Concealed {
		slog.Debug("concealed local clip skipped", "id", c.ID, "kind", c.Kind)
		return
	}
	d.fanout(ctx, c, "" /* skipOrigin = none */)
}

// isEcho reports whether a freshly read clipboard content `c` is just our own
// last write coming back — possibly re-represented as the current image's store
// path — rather than a new user copy.
func (d *Daemon) isEcho(c clipboard.Content) bool {
	d.mu.Lock()
	cur := d.current
	d.mu.Unlock()
	if cur.id == "" {
		return false
	}
	if c.Kind == cur.kind && c.Hash == cur.hash {
		return true
	}
	if cur.kind == clipboard.KindImage && c.Kind == clipboard.KindText {
		if strings.TrimSpace(string(c.Bytes)) == cur.imagePath {
			return true
		}
	}
	return false
}

func (d *Daemon) onReceive(c clipboard.Content, origin string) {
	if d.listenerPlan.SafeMode {
		// Milestone 1b1 stops peer work while the listener needs repair. The
		// transport-level safe-mode rejection map is added in 1b2.
		return
	}

	// A text clip that is one of our own image-store paths is an image echoed
	// as a path string by a host that can't hold images on its clipboard.
	// Writing it would clobber the real image (which arrived separately as a
	// kind=image clip) with a useless path, and relaying it would spread the
	// clobber across the fleet. Drop it.
	if c.Kind == clipboard.KindText && store.IsImageStorePath(string(c.Bytes)) {
		return
	}

	// Content we just wrote to our own clipboard coming back — e.g. the tmux
	// after-load-buffer hook re-submitting received text via `clipfan copy` under
	// a fresh clip-ID. Clip-ID dedup can't catch a re-originated ID, so suppress
	// by content here the same way pollOnce does.
	if d.isEcho(c) {
		return
	}

	// We intentionally do NOT short-circuit when origin == d.origin. That
	// path is reached by `clipfan copy` injecting into us as ourselves; we
	// want to treat it like any other clipboard change. Relay loops are
	// prevented by the clip-ID dedup below, not by origin filtering.
	if c.ID == "" {
		slog.Debug("dropping clip with no ID", "origin", origin)
		return
	}
	d.mu.Lock()
	if d.seen.has(c.ID) {
		d.mu.Unlock()
		return
	}
	if c.Concealed {
		d.seen.add(c.ID)
		d.mu.Unlock()
		slog.Debug("concealed peer clip dropped", "id", c.ID, "origin", origin)
		return
	}
	if !d.lastTS.IsZero() && c.TS.Before(d.lastTS) {
		d.mu.Unlock()
		return
	}
	d.seen.add(c.ID)
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

	recImg := ""
	if c.Kind == clipboard.KindImage {
		recImg = imagePath
	}
	if err := store.AppendHistory(c, origin, recImg); err != nil {
		slog.Debug("append history", "err", err)
	}

	var wrote bool
	if c.Kind == clipboard.KindImage {
		if err := d.cb.WriteImage(c.Bytes, imagePath); err != nil {
			slog.Warn("local clip write (image)", "err", err)
		} else {
			wrote = true
		}
	} else {
		if err := d.cb.WriteText(textPayload); err != nil {
			slog.Warn("local clip write (text)", "err", err)
		} else {
			wrote = true
		}
	}
	if wrote {
		d.mu.Lock()
		if c.Kind == clipboard.KindImage {
			d.current = currentClip{id: c.ID, kind: clipboard.KindImage, hash: c.Hash, imagePath: imagePath}
		} else {
			d.current = currentClip{id: c.ID, kind: clipboard.KindText, hash: c.Hash}
		}
		d.mu.Unlock()
	}
	if err := tmux.LoadBufferAll(textPayload); err != nil {
		slog.Debug("tmux load-buffer", "err", err)
	}

	// Relay: re-broadcast to every peer except the origin so disjoint peers
	// (e.g. flower-garden on LAN, paradise-park on tailnet) still converge
	// through the Mac hub. Relay-loop prevention uses clip-ID dedup (seen set)
	// and our-own-write echo suppression (d.current / isEcho).
	go d.fanout(context.Background(), c, origin)
}

// fanout pushes content to every discovered peer except `skipOrigin`. When
// skipOrigin is empty this is a fresh broadcast (stamps our own origin).
// When non-empty this is a relay (stamps the original origin).
func (d *Daemon) fanout(ctx context.Context, c clipboard.Content, skipOrigin string) {
	if d.listenerPlan.SafeMode || d.peerHTTPDisabled {
		return
	}
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
	var wrote bool
	if e.Kind == "image" {
		body, err := os.ReadFile(e.ImagePath)
		if err != nil {
			return fmt.Errorf("read image %s: %w", e.ImagePath, err)
		}
		c = clipboard.New(clipboard.KindImage, body, time.Now().UTC())
		if err := d.cb.WriteImage(body, e.ImagePath); err != nil {
			slog.Error("restore write image", "err", err)
		} else {
			wrote = true
		}
	} else {
		c = clipboard.New(clipboard.KindText, []byte(e.Text), time.Now().UTC())
		if err := d.cb.WriteText([]byte(e.Text)); err != nil {
			slog.Error("restore write text", "err", err)
		} else {
			wrote = true
		}
	}

	c.ID = transport.NewClipID()
	if c.ID == "" {
		// Mint failed (CSPRNG error): the local clipboard write already happened,
		// so the restore succeeded for the user; just skip the broadcast. The next
		// pollOnce will pick the content up and propagate it with a fresh ID.
		slog.Warn("could not mint clip ID; skipping restore broadcast")
		return nil
	}

	d.mu.Lock()
	d.seen.add(c.ID)
	d.lastTS = c.TS
	if wrote {
		if e.Kind == "image" {
			d.current = currentClip{id: c.ID, kind: clipboard.KindImage, hash: c.Hash, imagePath: e.ImagePath}
		} else {
			d.current = currentClip{id: c.ID, kind: clipboard.KindText, hash: c.Hash}
		}
	}
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
