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
	started           bool
}

type sshSyncPeer struct {
	id             string
	user           string
	host           string
	port           int
	privateKeyPath string
	knownHostsPath string
}

type sshPeerSession struct {
	peer sshSyncPeer
	send chan sshOutboundState
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
		out = append(out, sshSyncPeer{
			id:             peer.ID,
			user:           peer.SSHUser,
			host:           peer.SSHHost,
			port:           peer.SSHPort,
			privateKeyPath: cfg.SSH.SyncKey,
			knownHostsPath: cfg.SSH.KnownHosts,
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
		session := &sshPeerSession{peer: peer, send: make(chan sshOutboundState, 1)}
		m.sessions[peer.id] = session
		go m.runPeer(ctx, session)
	}
	m.mu.Unlock()
}

func (m *sshSyncManager) Publish(ctx context.Context, content clipboard.Content, origin string, skipOrigin string) {
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
		enqueueLatestSSHState(session, state)
	}
	_ = ctx
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
		if err := m.runPeerOnce(ctx, session); err != nil && ctx.Err() == nil {
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
		User:           session.peer.user,
		Host:           session.peer.host,
		Port:           session.peer.port,
		PrivateKeyPath: session.peer.privateKeyPath,
		KnownHostsPath: session.peer.knownHostsPath,
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

	readEvents := make(chan sshPeerReadEvent, 8)
	go func() {
		if err := m.readPeerEvents(attemptCtx, stream, write, readEvents); err != nil {
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
		if err := write(attemptCtx, func(ctx context.Context) error {
			return stream.WriteState(ctx, currentSeq, state.content, state.origin)
		}); err != nil {
			m.rememberPendingIfCurrent(session.peer.id, state)
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
			removed := m.handleSSHStreamAck(session.peer.id, inflight, event.ack)
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

func (m *sshSyncManager) clearPending(peerID string, state sshOutboundState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if pending, ok := m.pending[peerID]; ok && pending.content.ID == state.content.ID {
		delete(m.pending, peerID)
	}
}

func (m *sshSyncManager) rememberPendingIfCurrent(peerID string, state sshOutboundState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil {
		m.pending = map[string]sshOutboundState{}
	}
	if pending, ok := m.pending[peerID]; !ok || pending.content.ID == state.content.ID {
		m.pending[peerID] = state
	}
}

func (m *sshSyncManager) handleSSHStreamAck(peerID string, inflight map[uint64]sshOutboundState, ack transport.SSHStreamAckResult) bool {
	state, ok := inflight[ack.Seq]
	if !ok {
		return false
	}
	delete(inflight, ack.Seq)
	if !qualifyingSSHStreamAck(ack, state) {
		return true
	}
	m.clearPending(peerID, state)
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

func (m *sshSyncManager) readPeerEvents(ctx context.Context, stream *transport.SSHSyncStream, write func(context.Context, func(context.Context) error) error, events chan<- sshPeerReadEvent) error {
	for {
		event, err := stream.ReadNext(ctx, time.Now())
		if err != nil {
			return err
		}
		switch event.Type {
		case transport.SSHStreamFrameState:
			result := event.State
			clipID := ""
			status := "no_state"
			if result.NullReason == "" {
				clipID = result.Content.ID
				status = "applied"
				if m.onReceive != nil {
					m.onReceive(result.Content, result.Origin)
				}
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
