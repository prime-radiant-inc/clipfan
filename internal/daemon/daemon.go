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
	Hostname         string    `json:"hostname"`
	Port             int       `json:"port"`
	LastPushTS       time.Time `json:"last_push_ts,omitempty"`
	LastPushOK       bool      `json:"last_push_ok"`
	LastPushErr      string    `json:"last_push_err,omitempty"`
	LastRecvTS       time.Time `json:"last_recv_ts,omitempty"`
	Transport        string    `json:"transport,omitempty"`
	SSHHost          string    `json:"ssh_host,omitempty"`
	SSHPort          int       `json:"ssh_port,omitempty"`
	SSHUser          string    `json:"ssh_user,omitempty"`
	SSHActive        bool      `json:"ssh_active,omitempty"`
	SSHPending       bool      `json:"ssh_pending,omitempty"`
	SSHStatus        string    `json:"ssh_status,omitempty"`
	SSHLastConnectTS time.Time `json:"ssh_last_connect_ts,omitempty"`
	SSHLastAckTS     time.Time `json:"ssh_last_ack_ts,omitempty"`
	SSHLastError     string    `json:"ssh_last_error,omitempty"`
	SSHLastErrorTS   time.Time `json:"ssh_last_error_ts,omitempty"`
}

type SSHPeerRuntimeState struct {
	PeerID        string
	Active        bool
	Pending       bool
	Status        string
	LastConnectTS time.Time
	LastAckTS     time.Time
	LastRecvTS    time.Time
	LastError     string
	LastErrorTS   time.Time
}

// SSHSyncRuntime owns persistent SSH sync sessions for transport:"ssh".
// The daemon publishes only already-accepted current-state events through this
// interface so polling, receive, history, echo, and tmux rules stay centralized.
type SSHSyncRuntime interface {
	Start(ctx context.Context)
	Refresh(ctx context.Context, cfg *config.Config)
	Publish(ctx context.Context, content clipboard.Content, origin string, skipOrigin string)
	Snapshot() map[string]SSHPeerRuntimeState
}

type Daemon struct {
	cfg              *config.Config
	cb               clipboard.Backend
	disc             discovery.Discoverer
	auth             *transport.Auth
	sv               *transport.Server
	serve            func(context.Context) error
	serveListener    func(context.Context, net.Listener) error
	origin           string
	storagePreflight StoragePreflightPolicy
	listenerPlan     config.ListenerPlan
	stateDir         string
	configPath       string
	sshSync          SSHSyncRuntime
	sshSyncMu        sync.RWMutex
	discMu           sync.RWMutex
	runCtxMu         sync.RWMutex
	runCtx           context.Context

	mu      sync.Mutex
	seen    *seenSet
	lastTS  time.Time
	current currentClip

	peersMu    sync.RWMutex
	peerStatus map[string]*PeerState

	fleetMu        sync.Mutex
	fleetCached    FleetView
	fleetFetchedAt time.Time
}

// currentClip is the daemon's record of what it last wrote to the local
// clipboard, so pollOnce can recognise echoes of our own write — even when the
// content comes back re-represented (an image read back as its store path).
type currentClip struct {
	id        string
	kind      clipboard.Kind
	hash      [32]byte // canonical bytes hash (text bytes, or image bytes)
	imagePath string   // set for image clips
	content   clipboard.Content
	origin    string
	visible   bool
}

func New(cfg *config.Config) (*Daemon, error) {
	return NewWithOptions(cfg, Options{StoragePreflight: DefaultStoragePreflightPolicy()})
}

type Options struct {
	StoragePreflight        StoragePreflightPolicy
	ListenerBoundaryEnabled *bool
	SSHSyncRuntime          SSHSyncRuntime
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

	disc := discovererFromConfig(runtimeCfg)

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
		sshSync:          opts.SSHSyncRuntime,
		peerStatus:       map[string]*PeerState{},
		seen:             newSeenSet(),
	}
	if d.sshSync == nil && runtimeCfg.Transport == config.TransportSSH && releaseflags.SSHPersistentCurrentEnabled {
		manager := newSSHSyncManager(&runtimeCfg, auth, origin, d.onReceive, nil)
		if len(manager.peers) > 0 {
			d.sshSync = manager
		}
	}
	d.sv = transport.NewServer(listenerPlan.BindListen, auth, d.peersHandler)
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
	d.sv.SetVersionFunc(d.versionHandler)
	d.sv.SetCurrentFunc(d.currentHandler)
	d.sv.SetCurrentApply(func(c clipboard.Content, origin string) error {
		d.onReceive(c, origin)
		return nil
	})
	d.sv.SetFleetFunc(d.fleetHandler)
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
	d.sv.SetSSHPeerConfig(d.sshPeerConfigReadHandler, d.sshPeerConfigPutHandler)
	d.sv.SetSSHPeerConfigProofPatch(d.sshPeerConfigProofPatchHandler)
	d.sv.SetSSHPeerConfigTransition(d.sshPeerConfigTransitionHandler)
	d.sv.SetSSHPeerConfigDisable(d.sshPeerConfigDisableHandler)
	d.sv.SetSSHPeerConfigDelete(d.sshPeerConfigDeleteHandler)
	d.sv.SetHostRemove(d.hostRemoveHandler)
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

	if peers, err := d.discoveredPeers(ctx); err == nil {
		for _, p := range peers {
			if p.Self {
				continue
			}
			known[p.Hostname] = &PeerState{Hostname: p.Hostname, Port: p.Port}
		}
	}

	var sshRuntimeSnapshot map[string]SSHPeerRuntimeState
	if runtime := d.currentSSHSyncRuntime(); runtime != nil {
		sshRuntimeSnapshot = runtime.Snapshot()
	}

	d.peersMu.RLock()
	d.addConfiguredSSHPeersToSnapshot(known, sshRuntimeSnapshot)
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

func discovererFromConfig(cfg config.Config) discovery.Discoverer {
	switch cfg.Discovery {
	case "static":
		return discovery.NewStatic(cfg.StaticPeers, cfg.Port)
	default:
		return discovery.NewTailscale(cfg.Port, cfg.StaticPeers)
	}
}

func (d *Daemon) discoveredPeers(ctx context.Context) ([]discovery.Peer, error) {
	d.discMu.RLock()
	disc := d.disc
	d.discMu.RUnlock()
	if disc == nil {
		return nil, nil
	}
	return disc.Peers(ctx)
}

func (d *Daemon) addConfiguredSSHPeersToSnapshot(known map[string]*PeerState, runtime map[string]SSHPeerRuntimeState) {
	if d == nil || d.cfg == nil || d.cfg.Transport != config.TransportSSH || d.cfg.SSH == nil {
		return
	}
	port := d.cfg.Port
	if port == 0 {
		port = 7853
	}
	for _, peer := range d.cfg.SSH.Peers {
		if !sshPeerVisibleInSnapshot(peer) {
			continue
		}
		if hostsMatch(peer.ID, d.origin) {
			continue
		}
		row := d.mergeSnapshotPeer(known, PeerState{Hostname: peer.ID, Port: port})
		row.Transport = config.TransportSSH
		row.SSHHost = peer.SSHHost
		row.SSHPort = peer.SSHPort
		row.SSHUser = peer.SSHUser
		row.SSHStatus = "waiting"
		if runtimeState, ok := runtime[peer.ID]; ok {
			mergeSSHRuntimeState(row, runtimeState)
		}
	}
}

func (d *Daemon) mergeSnapshotPeer(known map[string]*PeerState, candidate PeerState) *PeerState {
	target := candidate.Hostname
	for existing := range known {
		if hostsMatch(existing, candidate.Hostname) {
			target = existing
			break
		}
	}
	row, ok := known[target]
	if !ok {
		row = &PeerState{Hostname: candidate.Hostname, Port: candidate.Port}
		known[target] = row
	}
	if row.Port == 0 {
		row.Port = candidate.Port
	}
	return row
}

func mergeSSHRuntimeState(row *PeerState, state SSHPeerRuntimeState) {
	if row == nil {
		return
	}
	row.SSHActive = state.Active
	row.SSHPending = state.Pending
	if state.Status != "" {
		row.SSHStatus = state.Status
	}
	if !state.LastConnectTS.IsZero() {
		row.SSHLastConnectTS = state.LastConnectTS
	}
	if !state.LastAckTS.IsZero() {
		row.SSHLastAckTS = state.LastAckTS
	}
	if !state.LastRecvTS.IsZero() && row.LastRecvTS.IsZero() {
		row.LastRecvTS = state.LastRecvTS
	}
	row.SSHLastError = state.LastError
	if !state.LastErrorTS.IsZero() {
		row.SSHLastErrorTS = state.LastErrorTS
	}
}

func sshPeerVisibleInSnapshot(peer config.SSHPeer) bool {
	if !peer.Enabled || peer.MigrationState != config.MigrationStateSSHKeysReady {
		return false
	}
	return peer.Accept || peer.Connect
}

func configuredSnapshotHostnames(cfg *config.Config) map[string]struct{} {
	out := map[string]struct{}{}
	if cfg == nil {
		return out
	}
	for _, peer := range cfg.StaticPeers {
		if strings.TrimSpace(peer) != "" {
			out[peer] = struct{}{}
		}
	}
	if cfg.Transport != config.TransportSSH || cfg.SSH == nil {
		return out
	}
	for _, peer := range cfg.SSH.Peers {
		if sshPeerVisibleInSnapshot(peer) {
			out[peer.ID] = struct{}{}
		}
	}
	return out
}

func configuredHostnamesMatch(host string, configured map[string]struct{}) bool {
	for configuredHost := range configured {
		if hostsMatch(host, configuredHost) {
			return true
		}
	}
	return false
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
	payload := map[string]any{
		"origin":      d.origin,
		"peers":       d.Snapshot(context.Background()),
		"version":     version.Version,
		"max_history": store.CapLimit(),
	}
	if status, err := config.ReadRevisionStatus(d.configPath); err == nil {
		payload["config_version"] = status.ConfigVersion
		payload["config_revision"] = status.ConfigRevision
		payload["revision_state"] = status.RevisionState
	}
	return payload
}

func (d *Daemon) versionHandler() any {
	return map[string]string{"version": version.Version}
}

func (d *Daemon) currentHandler() transport.CurrentPayload {
	d.mu.Lock()
	current := d.current
	d.mu.Unlock()
	if !current.visible || current.content.ID == "" {
		return transport.NoCurrentPayload("no_visible_current")
	}
	return transport.CurrentPayloadFromContent(current.content, current.origin)
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
	d.setRunContext(ctx)
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
	if runtime := d.currentSSHSyncRuntime(); runtime != nil {
		runtime.Start(ctx)
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
	d.current = currentClip{id: c.ID, kind: c.Kind, hash: c.Hash, imagePath: imagePath, content: c, origin: d.origin, visible: !c.Concealed}
	d.mu.Unlock()
	if c.Concealed {
		slog.Debug("concealed local clip skipped", "id", c.ID, "kind", c.Kind)
		return
	}
	d.publishSSH(ctx, c, d.origin, "" /* skipOrigin = none */)
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

	// Content we just wrote to our own clipboard coming back through `clipfan copy`
	// under a fresh clip-ID. Clip-ID dedup can't catch a re-originated ID, so
	// suppress by content here the same way pollOnce does.
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
			d.current = currentClip{id: c.ID, kind: clipboard.KindImage, hash: c.Hash, imagePath: imagePath, content: c, origin: origin, visible: true}
		} else {
			d.current = currentClip{id: c.ID, kind: clipboard.KindText, hash: c.Hash, content: c, origin: origin, visible: true}
		}
		d.mu.Unlock()
	}
	if err := tmux.LoadBufferAll(textPayload); err != nil {
		slog.Debug("tmux load-buffer", "err", err)
	}

	go d.publishSSH(context.Background(), c, origin, origin)
}

func (d *Daemon) publishSSH(ctx context.Context, c clipboard.Content, origin string, skipOrigin string) {
	if d.listenerPlan.SafeMode {
		return
	}
	runtime := d.currentSSHSyncRuntime()
	if runtime == nil {
		return
	}
	runtime.Publish(ctx, c, origin, skipOrigin)
}

func (d *Daemon) setRunContext(ctx context.Context) {
	d.runCtxMu.Lock()
	d.runCtx = ctx
	d.runCtxMu.Unlock()
}

func (d *Daemon) currentRunContext() context.Context {
	d.runCtxMu.RLock()
	ctx := d.runCtx
	d.runCtxMu.RUnlock()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (d *Daemon) currentSSHSyncRuntime() SSHSyncRuntime {
	d.sshSyncMu.RLock()
	runtime := d.sshSync
	d.sshSyncMu.RUnlock()
	return runtime
}

// Restore makes the history entry with the given id the current clipboard:
// it writes the local OS clipboard, re-records it in history (floating it to
// the top), and publishes through SSH sync so the fleet converges.
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
			d.current = currentClip{id: c.ID, kind: clipboard.KindImage, hash: c.Hash, imagePath: e.ImagePath, content: c, origin: d.origin, visible: true}
		} else {
			d.current = currentClip{id: c.ID, kind: clipboard.KindText, hash: c.Hash, content: c, origin: d.origin, visible: true}
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

	d.publishSSH(context.Background(), c, d.origin, "")
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
