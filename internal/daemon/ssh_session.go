package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
)

type sshSyncManager struct {
	auth              *transport.Auth
	localID           string
	peers             []sshSyncPeer
	starter           sshProcessStarter
	onReceive         func(clipboard.Content, string)
	reconnect         time.Duration
	handshake         time.Duration
	frameWriteTimeout time.Duration
	ackTimeout        time.Duration
	mu                sync.RWMutex
	sessions          map[string]*sshPeerSession
	pending           map[string]sshOutboundState
	peerRuntime       map[string]SSHPeerRuntimeState
	started           bool
}

type sshSyncPeer struct {
	id             string
	user           string
	host           string
	port           int
	privateKeyPath string
	knownHostsPath string
	gatewayPath    string
	connectKeyID   string
	directGateway  bool
}

type sshPeerSession struct {
	peer   sshSyncPeer
	send   chan sshOutboundState
	cancel context.CancelFunc
}

type sshOutboundState struct {
	content clipboard.Content
	origin  string
}

type sshPeerReadEvent struct {
	ack transport.SSHStreamAckResult
	err error
}

type sshProcessStarter interface {
	Start(ctx context.Context, cmd sshprovision.SSHCommand) (sshStartedProcess, error)
}

type sshStartedProcess interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Wait() error
}

func newSSHSyncManager(cfg *config.Config, auth *transport.Auth, localID string, onReceive func(clipboard.Content, string), starter sshProcessStarter) *sshSyncManager {
	if starter == nil {
		starter = execSSHProcessStarter{}
	}
	return &sshSyncManager{
		auth:              auth,
		localID:           localID,
		peers:             sshSyncPeersFromConfig(cfg),
		starter:           starter,
		onReceive:         onReceive,
		reconnect:         time.Second,
		handshake:         10 * time.Second,
		frameWriteTimeout: 10 * time.Second,
		ackTimeout:        30 * time.Second,
		sessions:          map[string]*sshPeerSession{},
		pending:           map[string]sshOutboundState{},
		peerRuntime:       map[string]SSHPeerRuntimeState{},
	}
}

func sshSyncPeersFromConfig(cfg *config.Config) []sshSyncPeer {
	if cfg == nil || cfg.Transport != config.TransportSSH || cfg.SSH == nil || cfg.SSH.SyncKey == "" || cfg.SSH.KnownHosts == "" {
		return nil
	}
	out := make([]sshSyncPeer, 0, len(cfg.SSH.Peers))
	limit := cfg.SSH.MaxSessions
	for _, peer := range cfg.SSH.Peers {
		if !peer.Enabled || !peer.Connect || !peer.Persistent || peer.MigrationState != config.MigrationStateSSHKeysReady {
			continue
		}
		if limit > 0 && len(out) >= limit {
			break
		}
		gatewayPath := peer.GatewayPath
		if gatewayPath == "" {
			gatewayPath = peer.Proof.ConnectGatewayPath
		}
		out = append(out, sshSyncPeer{
			id:             peer.ID,
			user:           peer.SSHUser,
			host:           peer.SSHHost,
			port:           peer.SSHPort,
			privateKeyPath: cfg.SSH.SyncKey,
			knownHostsPath: cfg.SSH.KnownHosts,
			gatewayPath:    gatewayPath,
			connectKeyID:   peer.Proof.ConnectKeyID,
			directGateway:  peer.Proof.ConnectVerifiedBy == config.ProofVerifiedByTailscaleSSH,
		})
	}
	return out
}

func (m *sshSyncManager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	for _, peer := range m.peers {
		m.startPeerLocked(ctx, peer)
	}
	m.mu.Unlock()
}

func (m *sshSyncManager) Refresh(ctx context.Context, cfg *config.Config) {
	nextPeers := sshSyncPeersFromConfig(cfg)
	nextByID := map[string]sshSyncPeer{}
	for _, peer := range nextPeers {
		nextByID[peer.id] = peer
	}

	m.mu.Lock()
	m.peers = nextPeers
	for peerID, session := range m.sessions {
		next, keep := nextByID[peerID]
		if keep && session.peer == next {
			continue
		}
		if session.cancel != nil {
			session.cancel()
		}
		delete(m.sessions, peerID)
		if !keep {
			delete(m.pending, peerID)
			delete(m.peerRuntime, peerID)
		}
	}
	if m.started {
		for _, peer := range nextPeers {
			if _, ok := m.sessions[peer.id]; ok {
				continue
			}
			m.startPeerLocked(ctx, peer)
		}
	}
	m.mu.Unlock()
}

func (m *sshSyncManager) startPeerLocked(ctx context.Context, peer sshSyncPeer) {
	sessionCtx, cancel := context.WithCancel(ctx)
	session := &sshPeerSession{peer: peer, send: make(chan sshOutboundState, 1), cancel: cancel}
	m.sessions[peer.id] = session
	m.markPeerConnectingLocked(peer.id)
	go m.runPeer(sessionCtx, session)
}

func (m *sshSyncManager) Publish(ctx context.Context, content clipboard.Content, origin string, skipOrigin string) {
	if len(content.Bytes) > transport.MaxSSHStreamPayloadBytes {
		slog.Warn("clip exceeds ssh stream payload limit; not syncing to peers", "clip", content.ID, "bytes", len(content.Bytes), "limit", transport.MaxSSHStreamPayloadBytes)
		return
	}
	state := sshOutboundState{content: content, origin: origin}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil {
		m.pending = map[string]sshOutboundState{}
	}
	visited := map[string]struct{}{}
	for _, peer := range m.peers {
		if skipOrigin != "" && hostsMatch(peer.id, skipOrigin) {
			continue
		}
		visited[peer.id] = struct{}{}
		m.pending[peer.id] = state
		m.markPeerPendingLocked(peer.id, true)
		if session := m.sessions[peer.id]; session != nil {
			enqueueLatestSSHState(session, state)
		}
	}
	for peerID, session := range m.sessions {
		if _, ok := visited[peerID]; ok {
			continue
		}
		if skipOrigin != "" && hostsMatch(peerID, skipOrigin) {
			continue
		}
		m.pending[peerID] = state
		m.markPeerPendingLocked(peerID, true)
		enqueueLatestSSHState(session, state)
	}
	_ = ctx
}

func (m *sshSyncManager) Snapshot() map[string]SSHPeerRuntimeState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[string]SSHPeerRuntimeState{}
	for _, peer := range m.peers {
		state := m.peerRuntime[peer.id]
		state.PeerID = peer.id
		state.Pending = state.Pending || m.pendingContainsLocked(peer.id)
		state.Status = sshPeerRuntimeStatus(state)
		out[peer.id] = state
	}
	return out
}

func (m *sshSyncManager) markPeerConnectingLocked(peerID string) {
	state := m.ensurePeerRuntimeLocked(peerID)
	state.Active = false
	state.Pending = m.pendingContainsLocked(peerID)
	state.Status = "connecting"
	m.peerRuntime[peerID] = state
}

func (m *sshSyncManager) markPeerConnectingIfSessionCurrent(session *sshPeerSession) bool {
	if session == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[session.peer.id] != session {
		return false
	}
	m.markPeerConnectingLocked(session.peer.id)
	return true
}

func (m *sshSyncManager) markPeerConnected(peerID string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.markPeerConnectedLocked(peerID, now)
}

func (m *sshSyncManager) markPeerConnectedIfSessionCurrent(session *sshPeerSession, now time.Time) bool {
	if session == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[session.peer.id] != session {
		return false
	}
	m.markPeerConnectedLocked(session.peer.id, now)
	return true
}

func (m *sshSyncManager) markPeerConnectedLocked(peerID string, now time.Time) {
	state := m.ensurePeerRuntimeLocked(peerID)
	state.Active = true
	state.Pending = m.pendingContainsLocked(peerID)
	state.LastConnectTS = now
	state.LastError = ""
	state.LastErrorTS = time.Time{}
	state.Status = sshPeerRuntimeStatus(state)
	m.peerRuntime[peerID] = state
}

func (m *sshSyncManager) markPeerPendingIfSessionCurrent(session *sshPeerSession, pending bool) bool {
	if session == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[session.peer.id] != session {
		return false
	}
	m.markPeerPendingLocked(session.peer.id, pending)
	return true
}

func (m *sshSyncManager) markPeerPendingLocked(peerID string, pending bool) {
	state := m.ensurePeerRuntimeLocked(peerID)
	state.Pending = pending || m.pendingContainsLocked(peerID)
	state.Status = sshPeerRuntimeStatus(state)
	m.peerRuntime[peerID] = state
}

func (m *sshSyncManager) markPeerAcked(peerID string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.markPeerAckedLocked(peerID, now)
}

func (m *sshSyncManager) clearPendingAndMarkAckedIfSessionCurrent(session *sshPeerSession, state sshOutboundState, now time.Time) bool {
	if session == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[session.peer.id] != session {
		return false
	}
	if pending, ok := m.pending[session.peer.id]; ok && pending.content.ID == state.content.ID {
		delete(m.pending, session.peer.id)
		m.markPeerPendingLocked(session.peer.id, false)
	}
	m.markPeerAckedLocked(session.peer.id, now)
	return true
}

func (m *sshSyncManager) markPeerAckedLocked(peerID string, now time.Time) {
	state := m.ensurePeerRuntimeLocked(peerID)
	state.Active = true
	state.Pending = m.pendingContainsLocked(peerID)
	state.LastAckTS = now
	state.LastError = ""
	state.LastErrorTS = time.Time{}
	state.Status = sshPeerRuntimeStatus(state)
	m.peerRuntime[peerID] = state
}

func (m *sshSyncManager) markPeerReceived(peerID string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.markPeerReceivedLocked(peerID, now)
}

func (m *sshSyncManager) markPeerReceivedIfSessionCurrent(session *sshPeerSession, now time.Time) bool {
	if session == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[session.peer.id] != session {
		return false
	}
	m.markPeerReceivedLocked(session.peer.id, now)
	return true
}

func (m *sshSyncManager) receiveContentIfSessionCurrent(session *sshPeerSession, content clipboard.Content, origin string, now time.Time) bool {
	if session == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[session.peer.id] != session {
		return false
	}
	if m.onReceive != nil {
		m.onReceive(content, origin)
	}
	m.markPeerReceivedLocked(session.peer.id, now)
	return true
}

func (m *sshSyncManager) markPeerReceivedLocked(peerID string, now time.Time) {
	state := m.ensurePeerRuntimeLocked(peerID)
	state.Active = true
	state.Pending = m.pendingContainsLocked(peerID)
	state.LastRecvTS = now
	state.LastError = ""
	state.LastErrorTS = time.Time{}
	state.Status = sshPeerRuntimeStatus(state)
	m.peerRuntime[peerID] = state
}

func (m *sshSyncManager) markPeerError(peerID string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.markPeerErrorLocked(peerID, err)
}

func (m *sshSyncManager) markPeerErrorIfSessionCurrent(session *sshPeerSession, err error) bool {
	if session == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[session.peer.id] != session {
		return false
	}
	m.markPeerErrorLocked(session.peer.id, err)
	return true
}

func (m *sshSyncManager) markPeerErrorLocked(peerID string, err error) {
	state := m.ensurePeerRuntimeLocked(peerID)
	state.Active = false
	state.Pending = m.pendingContainsLocked(peerID)
	if err != nil {
		state.LastError = err.Error()
		state.LastErrorTS = time.Now().UTC()
	}
	state.Status = "attention"
	m.peerRuntime[peerID] = state
}

func (m *sshSyncManager) ensurePeerRuntimeLocked(peerID string) SSHPeerRuntimeState {
	if m.peerRuntime == nil {
		m.peerRuntime = map[string]SSHPeerRuntimeState{}
	}
	state := m.peerRuntime[peerID]
	if state.PeerID == "" {
		state.PeerID = peerID
	}
	return state
}

func (m *sshSyncManager) pendingContainsLocked(peerID string) bool {
	if m.pending == nil {
		return false
	}
	_, ok := m.pending[peerID]
	return ok
}

func sshPeerRuntimeStatus(state SSHPeerRuntimeState) string {
	if state.Active {
		if state.Pending {
			return "syncing"
		}
		return "live"
	}
	if state.Status == "connecting" {
		return "connecting"
	}
	if state.LastError != "" {
		return "attention"
	}
	return "waiting"
}

func enqueueLatestSSHState(session *sshPeerSession, state sshOutboundState) {
	select {
	case session.send <- state:
	default:
		select {
		case <-session.send:
		default:
		}
		select {
		case session.send <- state:
		default:
		}
	}
}

func drainQueuedDuplicateSSHState(session *sshPeerSession, state sshOutboundState) {
	select {
	case queued := <-session.send:
		if queued.content.ID != state.content.ID {
			select {
			case session.send <- queued:
			default:
			}
		}
	default:
	}
}

func (m *sshSyncManager) runPeer(ctx context.Context, session *sshPeerSession) {
	for {
		if !m.markPeerConnectingIfSessionCurrent(session) {
			return
		}
		if err := m.runPeerOnce(ctx, session); err != nil && ctx.Err() == nil {
			m.markPeerErrorIfSessionCurrent(session, err)
			slog.Debug("ssh sync stream ended", "peer", session.peer.id, "err", err)
		}
		if ctx.Err() != nil {
			return
		}
		timer := time.NewTimer(m.reconnect)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (m *sshSyncManager) runPeerOnce(ctx context.Context, session *sshPeerSession) error {
	attemptCtx, cancelAttempt := context.WithCancel(ctx)
	cmd, err := sshprovision.PinnedSSHSyncStreamCommand(sshprovision.PinnedSSHCommand{
		User:            session.peer.user,
		Host:            session.peer.host,
		Port:            session.peer.port,
		PrivateKeyPath:  session.peer.privateKeyPath,
		KnownHostsPath:  session.peer.knownHostsPath,
		GatewayPath:     session.peer.gatewayPath,
		AuthorizedPeer:  m.localID,
		AuthorizedKeyID: session.peer.connectKeyID,
		DirectGateway:   session.peer.directGateway,
	})
	if err != nil {
		cancelAttempt()
		return err
	}
	proc, err := m.starter.Start(attemptCtx, cmd)
	if err != nil {
		cancelAttempt()
		return err
	}
	defer func() {
		cancelAttempt()
		_ = proc.Stdin().Close()
		_ = proc.Stdout().Close()
		_ = proc.Wait()
	}()

	handshakeCtx, cancelHandshake := context.WithTimeout(attemptCtx, m.handshake)
	defer cancelHandshake()
	stream := transport.NewSSHSyncStream(m.auth, m.localID, session.peer.id, proc.Stdout(), proc.Stdin())
	now := time.Now()
	hello, err := transport.NewSSHStreamHello(m.auth, transport.SSHStreamPurposeSyncStream, m.localID, session.peer.id, now, "")
	if err != nil {
		return err
	}
	var writeMu sync.Mutex
	write := func(ctx context.Context, fn func(context.Context) error) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return m.writeSSHFrame(ctx, cancelAttempt, fn)
	}
	if err := write(handshakeCtx, func(ctx context.Context) error { return stream.WriteHello(ctx, hello) }); err != nil {
		return err
	}
	if _, err := stream.ReadHello(handshakeCtx, time.Now()); err != nil {
		return err
	}
	if !m.markPeerConnectedIfSessionCurrent(session, time.Now().UTC()) {
		return context.Canceled
	}

	readEvents := make(chan sshPeerReadEvent, 8)
	go func() {
		if err := m.readPeerEvents(attemptCtx, session, stream, write, readEvents); err != nil {
			readEvents <- sshPeerReadEvent{err: err}
		}
	}()

	var seq uint64
	inflight := map[uint64]sshOutboundState{}
	ackTimeout := m.ackTimeout
	if ackTimeout <= 0 {
		ackTimeout = 30 * time.Second
	}
	ackTimer := time.NewTimer(ackTimeout)
	if !ackTimer.Stop() {
		select {
		case <-ackTimer.C:
		default:
		}
	}
	var ackTimerC <-chan time.Time
	defer ackTimer.Stop()
	armAckTimerIfIdle := func(wasIdle bool) {
		if !wasIdle || len(inflight) == 0 || ackTimerC != nil {
			return
		}
		ackTimer.Reset(ackTimeout)
		ackTimerC = ackTimer.C
	}
	resetAckTimerAfterAck := func(removed bool) {
		if !removed {
			return
		}
		if !ackTimer.Stop() {
			select {
			case <-ackTimer.C:
			default:
			}
		}
		if len(inflight) == 0 {
			ackTimerC = nil
			return
		}
		ackTimer.Reset(ackTimeout)
		ackTimerC = ackTimer.C
	}
	sendState := func(state sshOutboundState) error {
		seq++
		currentSeq := seq
		wasIdle := len(inflight) == 0
		if !m.markPeerPendingIfSessionCurrent(session, true) {
			return context.Canceled
		}
		if err := write(attemptCtx, func(ctx context.Context) error {
			return stream.WriteState(ctx, currentSeq, state.content, state.origin)
		}); err != nil {
			m.rememberPendingIfSessionCurrent(session, state)
			return err
		}
		inflight[currentSeq] = state
		armAckTimerIfIdle(wasIdle)
		return nil
	}
	if state, ok := m.pendingState(session.peer.id); ok {
		if err := sendState(state); err != nil {
			return err
		}
		drainQueuedDuplicateSSHState(session, state)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event := <-readEvents:
			if event.err != nil {
				return event.err
			}
			removed := m.handleSSHStreamAck(session, inflight, event.ack)
			resetAckTimerAfterAck(removed)
		case state := <-session.send:
			if err := sendState(state); err != nil {
				return err
			}
		case <-ackTimerC:
			cancelAttempt()
			return fmt.Errorf("ssh stream ack timeout: %w", context.DeadlineExceeded)
		}
	}
}

func (m *sshSyncManager) pendingState(peerID string) (sshOutboundState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.pending[peerID]
	return state, ok
}

func (m *sshSyncManager) rememberPending(peerID string, state sshOutboundState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil {
		m.pending = map[string]sshOutboundState{}
	}
	m.pending[peerID] = state
}

func (m *sshSyncManager) clearPendingIfSessionCurrent(session *sshPeerSession, state sshOutboundState) bool {
	if session == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[session.peer.id] != session {
		return false
	}
	if pending, ok := m.pending[session.peer.id]; ok && pending.content.ID == state.content.ID {
		delete(m.pending, session.peer.id)
		m.markPeerPendingLocked(session.peer.id, false)
	}
	return true
}

func (m *sshSyncManager) rememberPendingIfSessionCurrent(session *sshPeerSession, state sshOutboundState) {
	if session == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[session.peer.id] != session {
		return
	}
	if m.pending == nil {
		m.pending = map[string]sshOutboundState{}
	}
	peerID := session.peer.id
	if pending, ok := m.pending[peerID]; !ok || pending.content.ID == state.content.ID {
		m.pending[peerID] = state
	}
}

func (m *sshSyncManager) handleSSHStreamAck(session *sshPeerSession, inflight map[uint64]sshOutboundState, ack transport.SSHStreamAckResult) bool {
	state, ok := inflight[ack.Seq]
	if !ok {
		return false
	}
	delete(inflight, ack.Seq)
	if qualifyingSSHStreamAck(ack, state) {
		m.clearPendingAndMarkAckedIfSessionCurrent(session, state, time.Now().UTC())
		return true
	}
	if ack.Status == "rejected" && ack.ID == state.content.ID {
		// A rejection is final for this clip: resending identical bytes can never
		// succeed, and leaving it pending re-poisons every reconnect. Drop it.
		slog.Warn("peer rejected clip; dropping from pending", "peer", session.peer.id, "clip", ack.ID, "reason", ack.Reason)
		m.clearPendingIfSessionCurrent(session, state)
	}
	return true
}

func qualifyingSSHStreamAck(ack transport.SSHStreamAckResult, state sshOutboundState) bool {
	if state.content.ID == "" {
		return ack.ID == "" && ack.Status == "no_state"
	}
	if ack.ID != state.content.ID {
		return false
	}
	switch ack.Status {
	case "applied", "ignored_seen", "ignored_echo":
		return true
	default:
		return false
	}
}

func (m *sshSyncManager) writeSSHFrame(ctx context.Context, cancelAttempt context.CancelFunc, fn func(context.Context) error) error {
	timeout := m.frameWriteTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	writeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- fn(writeCtx)
	}()
	select {
	case err := <-done:
		return err
	case <-writeCtx.Done():
		if cancelAttempt != nil {
			cancelAttempt()
		}
		return writeCtx.Err()
	}
}

func (m *sshSyncManager) readPeerEvents(ctx context.Context, session *sshPeerSession, stream *transport.SSHSyncStream, write func(context.Context, func(context.Context) error) error, events chan<- sshPeerReadEvent) error {
	for {
		event, err := stream.ReadNextNow(ctx)
		if err != nil {
			return err
		}
		switch event.Type {
		case transport.SSHStreamFrameState:
			result := event.State
			clipID := ""
			status := "no_state"
			if result.NullReason == "" {
				if !m.receiveContentIfSessionCurrent(session, result.Content, result.Origin, time.Now().UTC()) {
					return context.Canceled
				}
				clipID = result.Content.ID
				status = "applied"
			}
			if err := write(ctx, func(ctx context.Context) error {
				return stream.WriteAck(ctx, result.Seq, clipID, status, "")
			}); err != nil {
				return err
			}
		case transport.SSHStreamFrameAck:
			select {
			case events <- sshPeerReadEvent{ack: event.Ack}:
			case <-ctx.Done():
				return ctx.Err()
			}
		case transport.SSHStreamFrameError:
			return fmt.Errorf("ssh stream error frame: %s", event.ErrorCode)
		default:
			return fmt.Errorf("%w: %s", transport.ErrSSHStreamUnexpectedFrame, event.Type)
		}
	}
}

type execSSHProcessStarter struct{}

func (execSSHProcessStarter) Start(ctx context.Context, cmd sshprovision.SSHCommand) (sshStartedProcess, error) {
	if len(cmd.Args) == 0 {
		return nil, fmt.Errorf("empty ssh command")
	}
	process := exec.CommandContext(ctx, cmd.Args[0], cmd.Args[1:]...)
	stdin, err := process.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := process.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	if err := process.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	return execSSHStartedProcess{cmd: process, stdin: stdin, stdout: stdout}, nil
}

type execSSHStartedProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func (p execSSHStartedProcess) Stdin() io.WriteCloser { return p.stdin }
func (p execSSHStartedProcess) Stdout() io.ReadCloser { return p.stdout }
func (p execSSHStartedProcess) Wait() error           { return p.cmd.Wait() }
