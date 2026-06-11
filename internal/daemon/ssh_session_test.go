package daemon

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
)

func TestSSHSyncPeersFromConfigSelectsReadyPersistentConnectors(t *testing.T) {
	cfg := sshSyncManagerTestConfig()
	cfg.SSH.Peers = append(cfg.SSH.Peers,
		config.SSHPeer{ID: "disabled", Enabled: false, Connect: true, Persistent: true, MigrationState: config.MigrationStateSSHKeysReady},
		config.SSHPeer{ID: "accept-only", Enabled: true, Accept: true, Connect: false, MigrationState: config.MigrationStateSSHKeysReady},
		config.SSHPeer{ID: "not-ready", Enabled: true, Connect: true, Persistent: true, MigrationState: config.MigrationStateSSHMaterialStaged},
	)

	peers := sshSyncPeersFromConfig(cfg)
	if len(peers) != 1 {
		t.Fatalf("peers = %#v, want exactly one ready connector", peers)
	}
	if peers[0].id != "linux-b" || peers[0].privateKeyPath != cfg.SSH.SyncKey || peers[0].knownHostsPath != cfg.SSH.KnownHosts {
		t.Fatalf("peer = %#v", peers[0])
	}
}

func TestSSHSyncPeersFromConfigMarksTailscaleSSHDirectGateway(t *testing.T) {
	cfg := sshSyncManagerTestConfig()
	cfg.SSH.Peers[0].GatewayPath = "/home/jesse/.local/bin/clipfan"
	cfg.SSH.Peers[0].Proof.ConnectVerifiedBy = config.ProofVerifiedByTailscaleSSH

	peers := sshSyncPeersFromConfig(cfg)
	if len(peers) != 1 {
		t.Fatalf("peers = %#v, want one", peers)
	}
	if !peers[0].directGateway || peers[0].gatewayPath != "/home/jesse/.local/bin/clipfan" || peers[0].connectKeyID != "key-connect-123" {
		t.Fatalf("peer = %#v, want direct gateway with connect key id", peers[0])
	}
}

func TestSSHSyncPeersFromConfigHonorsMaxSessions(t *testing.T) {
	cfg := sshSyncManagerTestConfig()
	cfg.SSH.MaxSessions = 1
	cfg.SSH.Peers = append(cfg.SSH.Peers, readySSHPeerForTest("linux-c"))

	peers := sshSyncPeersFromConfig(cfg)
	if len(peers) != 1 {
		t.Fatalf("peers = %#v, want one due to max_sessions", peers)
	}
	if peers[0].id != "linux-b" {
		t.Fatalf("first selected peer = %q, want linux-b", peers[0].id)
	}
}

func TestSSHTransportAutostartsSyncManagerForReadySSHConfig(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := sshSyncManagerTestConfig()
	cfg.Listen = "127.0.0.1:7853"
	cfg.Port = 7853

	d, err := NewWithOptions(cfg, Options{
		ListenerBoundaryEnabled: boolPtr(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	manager, ok := d.sshSync.(*sshSyncManager)
	if !ok {
		t.Fatalf("sshSync = %#v, want *sshSyncManager", d.sshSync)
	}
	if len(manager.peers) != 1 || manager.peers[0].id != "linux-b" {
		t.Fatalf("manager peers = %#v, want linux-b", manager.peers)
	}
}

func TestSSHSyncManagerPublishesStateToStartedPeerStream(t *testing.T) {
	cfg := sshSyncManagerTestConfig()
	auth, err := transport.NewAuth(cfg.SharedKey)
	if err != nil {
		t.Fatal(err)
	}
	starter := newFakeSSHProcessStarter()
	manager := newSSHSyncManager(cfg, auth, "m4", nil, starter)
	manager.reconnect = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)
	start := starter.waitForStart(t)
	assertSyncStreamCommand(t, start.cmd)

	remoteDone := make(chan transport.SSHStreamStateResult, 1)
	go func() {
		remote := transport.NewSSHSyncStream(auth, "linux-b", "m4", start.remoteInput, start.remoteOutput)
		remote.SetHelloNonceCache(transport.NewSSHStreamHelloNonceCache())
		if _, err := remote.ReadHello(ctx, time.Now()); err != nil {
			t.Errorf("remote ReadHello error = %v", err)
			return
		}
		hello := mustDaemonSSHHello(t, auth, "linux-b", "m4")
		if err := remote.WriteHello(ctx, hello); err != nil {
			t.Errorf("remote WriteHello error = %v", err)
			return
		}
		result, err := remote.ReadStateFrame(ctx, time.Now())
		if err != nil {
			t.Errorf("remote ReadStateFrame error = %v", err)
			return
		}
		if err := remote.WriteAck(ctx, result.Seq, result.Content.ID, "applied", ""); err != nil {
			t.Errorf("remote WriteAck error = %v", err)
			return
		}
		remoteDone <- result
	}()

	content := clipboard.New(clipboard.KindText, []byte("from m4"), fixedTime)
	content.ID = "clip-local"
	manager.Publish(ctx, content, "m4", "")

	select {
	case result := <-remoteDone:
		if result.Seq != 1 || result.Sender != "m4" || result.Origin != "m4" || string(result.Content.Bytes) != "from m4" {
			t.Fatalf("remote state result = %#v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for remote state")
	}
}

func TestSSHSyncManagerStartsTailscaleSSHDirectGatewayStream(t *testing.T) {
	cfg := sshSyncManagerTestConfig()
	cfg.SSH.Peers[0].GatewayPath = "/home/jesse/.local/bin/clipfan"
	cfg.SSH.Peers[0].Proof.ConnectVerifiedBy = config.ProofVerifiedByTailscaleSSH
	auth, err := transport.NewAuth(cfg.SharedKey)
	if err != nil {
		t.Fatal(err)
	}
	starter := newFakeSSHProcessStarter()
	manager := newSSHSyncManager(cfg, auth, "m4", nil, starter)
	manager.reconnect = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)
	start := starter.waitForStart(t)
	defer start.close()
	assertDirectSyncStreamCommand(t, start.cmd)
}

func TestSSHSyncManagerRefreshAddsAndRemovesStartedPeerSessions(t *testing.T) {
	cfg := sshSyncManagerTestConfig()
	auth, err := transport.NewAuth(cfg.SharedKey)
	if err != nil {
		t.Fatal(err)
	}
	starter := newFakeSSHProcessStarter()
	manager := newSSHSyncManager(cfg, auth, "m4", nil, starter)
	manager.reconnect = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)
	first := starter.waitForStart(t)
	defer first.close()

	nextCfg := sshSyncManagerTestConfig()
	nextCfg.SSH.Peers = []config.SSHPeer{readySSHPeerForTest("linux-c")}
	manager.Refresh(ctx, nextCfg)
	second := starter.waitForStart(t)
	defer second.close()

	manager.mu.RLock()
	_, oldPresent := manager.sessions["linux-b"]
	_, newPresent := manager.sessions["linux-c"]
	manager.mu.RUnlock()
	if oldPresent || !newPresent {
		t.Fatalf("sessions old=%v new=%v, want old removed and new started", oldPresent, newPresent)
	}
	if len(second.cmd.Args) == 0 || !strings.Contains(strings.Join(second.cmd.Args, " "), "linux-c.example.com") {
		t.Fatalf("second command = %#v, want linux-c target", second.cmd.Args)
	}
}

func TestSSHSyncManagerInboundStateCallsReceive(t *testing.T) {
	cfg := sshSyncManagerTestConfig()
	auth, err := transport.NewAuth(cfg.SharedKey)
	if err != nil {
		t.Fatal(err)
	}
	starter := newFakeSSHProcessStarter()
	received := make(chan sshSyncPublishCall, 1)
	acked := make(chan transport.SSHStreamAckResult, 1)
	manager := newSSHSyncManager(cfg, auth, "m4", func(c clipboard.Content, origin string) {
		received <- sshSyncPublishCall{content: c, origin: origin}
	}, starter)
	manager.reconnect = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)
	start := starter.waitForStart(t)

	go func() {
		remote := transport.NewSSHSyncStream(auth, "linux-b", "m4", start.remoteInput, start.remoteOutput)
		remote.SetHelloNonceCache(transport.NewSSHStreamHelloNonceCache())
		if _, err := remote.ReadHello(ctx, time.Now()); err != nil {
			t.Errorf("remote ReadHello error = %v", err)
			return
		}
		hello := mustDaemonSSHHello(t, auth, "linux-b", "m4")
		if err := remote.WriteHello(ctx, hello); err != nil {
			t.Errorf("remote WriteHello error = %v", err)
			return
		}
		content := clipboard.New(clipboard.KindText, []byte("from linux"), fixedTime)
		content.ID = "clip-remote"
		if err := remote.WriteState(ctx, 1, content, "linux-b"); err != nil {
			t.Errorf("remote WriteState error = %v", err)
			return
		}
		event, err := remote.ReadNext(ctx, time.Now())
		if err != nil {
			t.Errorf("remote ReadNext error = %v", err)
			return
		}
		acked <- event.Ack
		if event.Type != transport.SSHStreamFrameAck {
			t.Errorf("remote event = %#v, want ack", event)
		}
	}()

	select {
	case got := <-received:
		if got.origin != "linux-b" || got.content.ID != "clip-remote" || string(got.content.Bytes) != "from linux" {
			t.Fatalf("received = %#v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inbound state")
	}
	select {
	case ack := <-acked:
		if ack.Seq != 1 || ack.ID != "clip-remote" || ack.Status != "applied" {
			t.Fatalf("ack = %#v, want applied clip-remote", ack)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inbound ack")
	}
}

func TestSSHSyncManagerReplaysPendingStateAfterHandshake(t *testing.T) {
	cfg := sshSyncManagerTestConfig()
	auth, err := transport.NewAuth(cfg.SharedKey)
	if err != nil {
		t.Fatal(err)
	}
	starter := newFakeSSHProcessStarter()
	manager := newSSHSyncManager(cfg, auth, "m4", nil, starter)
	pending := clipboard.New(clipboard.KindText, []byte("saved latest"), fixedTime)
	pending.ID = "clip-saved"
	manager.rememberPending("linux-b", sshOutboundState{content: pending, origin: "m4"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &sshPeerSession{peer: manager.peers[0], send: make(chan sshOutboundState, 1)}
	manager.sessions[session.peer.id] = session
	errCh := make(chan error, 1)
	go func() { errCh <- manager.runPeerOnce(ctx, session) }()
	start := starter.waitForStart(t)
	defer start.close()

	remoteDone := make(chan transport.SSHStreamStateResult, 1)
	allowAck := make(chan struct{})
	go func() {
		remote := transport.NewSSHSyncStream(auth, "linux-b", "m4", start.remoteInput, start.remoteOutput)
		remote.SetHelloNonceCache(transport.NewSSHStreamHelloNonceCache())
		if _, err := remote.ReadHello(ctx, time.Now()); err != nil {
			t.Errorf("remote ReadHello error = %v", err)
			return
		}
		hello := mustDaemonSSHHello(t, auth, "linux-b", "m4")
		if err := remote.WriteHello(ctx, hello); err != nil {
			t.Errorf("remote WriteHello error = %v", err)
			return
		}
		result, err := remote.ReadStateFrame(ctx, time.Now())
		if err != nil {
			t.Errorf("remote ReadStateFrame error = %v", err)
			return
		}
		remoteDone <- result
		<-allowAck
		if err := remote.WriteAck(ctx, result.Seq, result.Content.ID, "applied", ""); err != nil {
			t.Errorf("remote WriteAck error = %v", err)
		}
	}()

	select {
	case result := <-remoteDone:
		if result.Content.ID != "clip-saved" || string(result.Content.Bytes) != "saved latest" {
			t.Fatalf("replayed state = %#v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for replayed pending state")
	}
	if state, ok := manager.pendingState("linux-b"); !ok || state.content.ID != "clip-saved" {
		t.Fatalf("pending before ack = (%#v,%v), want clip-saved present", state, ok)
	}
	close(allowAck)
	waitForPendingAbsent(t, manager, "linux-b")
	cancel()
	select {
	case <-errCh:
	case <-time.After(time.Second):
		t.Fatal("runPeerOnce did not return after cancel")
	}
}

func TestSSHSyncManagerAckTimeoutCancelsAttemptAndKeepsPendingState(t *testing.T) {
	cfg := sshSyncManagerTestConfig()
	auth, err := transport.NewAuth(cfg.SharedKey)
	if err != nil {
		t.Fatal(err)
	}
	starter := newFakeSSHProcessStarter()
	manager := newSSHSyncManager(cfg, auth, "m4", nil, starter)
	manager.ackTimeout = 20 * time.Millisecond
	pending := clipboard.New(clipboard.KindText, []byte("needs ack"), fixedTime)
	pending.ID = "clip-needs-ack"
	manager.rememberPending("linux-b", sshOutboundState{content: pending, origin: "m4"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &sshPeerSession{peer: manager.peers[0], send: make(chan sshOutboundState, 1)}
	manager.sessions[session.peer.id] = session
	errCh := make(chan error, 1)
	go func() { errCh <- manager.runPeerOnce(ctx, session) }()
	start := starter.waitForStart(t)
	defer start.close()

	remoteRead := make(chan struct{})
	go func() {
		remote := transport.NewSSHSyncStream(auth, "linux-b", "m4", start.remoteInput, start.remoteOutput)
		remote.SetHelloNonceCache(transport.NewSSHStreamHelloNonceCache())
		if _, err := remote.ReadHello(ctx, time.Now()); err != nil {
			t.Errorf("remote ReadHello error = %v", err)
			return
		}
		hello := mustDaemonSSHHello(t, auth, "linux-b", "m4")
		if err := remote.WriteHello(ctx, hello); err != nil {
			t.Errorf("remote WriteHello error = %v", err)
			return
		}
		if _, err := remote.ReadStateFrame(ctx, time.Now()); err != nil {
			t.Errorf("remote ReadStateFrame error = %v", err)
			return
		}
		close(remoteRead)
	}()

	select {
	case <-remoteRead:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for remote state read")
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("runPeerOnce error = %v, want context deadline", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runPeerOnce did not return after ack timeout")
	}
	if state, ok := manager.pendingState("linux-b"); !ok || state.content.ID != "clip-needs-ack" {
		t.Fatalf("pending after ack timeout = (%#v,%v), want clip-needs-ack present", state, ok)
	}
}

func TestSSHSyncManagerPublishDoesNotBlockWhenQueueRefillsConcurrently(t *testing.T) {
	manager := &sshSyncManager{
		sessions: map[string]*sshPeerSession{
			"linux-b": {peer: sshSyncPeer{id: "linux-b"}, send: make(chan sshOutboundState, 1)},
		},
	}
	session := manager.sessions["linux-b"]
	session.send <- sshOutboundState{origin: "old"}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		content := clipboard.New(clipboard.KindText, []byte("new"), fixedTime)
		content.ID = "clip-new"
		for {
			select {
			case <-stop:
				return
			default:
				manager.Publish(context.Background(), content, "m4", "")
			}
		}
	}()

	select {
	case <-done:
		t.Fatal("publisher stopped unexpectedly")
	case <-time.After(50 * time.Millisecond):
	}
	close(stop)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("concurrent publisher blocked in Publish")
	}
}

func TestSSHSyncManagerPublishBeforeStartPreservesPendingState(t *testing.T) {
	cfg := sshSyncManagerTestConfig()
	auth, err := transport.NewAuth(cfg.SharedKey)
	if err != nil {
		t.Fatal(err)
	}
	manager := newSSHSyncManager(cfg, auth, "m4", nil, newFakeSSHProcessStarter())
	content := clipboard.New(clipboard.KindText, []byte("before start"), fixedTime)
	content.ID = "clip-before-start"

	manager.Publish(context.Background(), content, "m4", "")

	if got := manager.pending["linux-b"].content.ID; got != "clip-before-start" {
		t.Fatalf("pending before start = %q, want clip-before-start", got)
	}
}

func TestSSHSyncManagerSnapshotTracksRuntimeTransitions(t *testing.T) {
	cfg := sshSyncManagerTestConfig()
	auth, err := transport.NewAuth(cfg.SharedKey)
	if err != nil {
		t.Fatal(err)
	}
	manager := newSSHSyncManager(cfg, auth, "m4", nil, newFakeSSHProcessStarter())
	content := clipboard.New(clipboard.KindText, []byte("tracked"), fixedTime)
	content.ID = "clip-tracked"

	manager.Publish(context.Background(), content, "m4", "")
	pending := manager.Snapshot()["linux-b"]
	if !pending.Pending || pending.Status != "waiting" {
		t.Fatalf("pending snapshot = %#v, want pending waiting", pending)
	}

	manager.markPeerConnected("linux-b", fixedTime)
	syncing := manager.Snapshot()["linux-b"]
	if !syncing.Active || !syncing.Pending || syncing.Status != "syncing" || syncing.LastConnectTS != fixedTime {
		t.Fatalf("connected snapshot = %#v, want active syncing", syncing)
	}

	session := &sshPeerSession{peer: manager.peers[0]}
	manager.mu.Lock()
	manager.sessions["linux-b"] = session
	manager.mu.Unlock()
	manager.clearPendingIfSessionCurrent(session, sshOutboundState{content: content, origin: "m4"})
	manager.markPeerAcked("linux-b", fixedTime.Add(time.Second))
	live := manager.Snapshot()["linux-b"]
	if !live.Active || live.Pending || live.Status != "live" || live.LastAckTS != fixedTime.Add(time.Second) {
		t.Fatalf("acked snapshot = %#v, want live without pending", live)
	}

	manager.markPeerError("linux-b", io.ErrClosedPipe)
	failed := manager.Snapshot()["linux-b"]
	if failed.Active || failed.Status != "attention" || failed.LastError == "" || failed.LastErrorTS.IsZero() {
		t.Fatalf("failed snapshot = %#v, want attention with error", failed)
	}
}

func TestSSHSyncManagerHandshakeTimeoutReturnsToReconnectLoop(t *testing.T) {
	cfg := sshSyncManagerTestConfig()
	auth, err := transport.NewAuth(cfg.SharedKey)
	if err != nil {
		t.Fatal(err)
	}
	starter := newFakeSSHProcessStarter()
	manager := newSSHSyncManager(cfg, auth, "m4", nil, starter)
	manager.handshake = 20 * time.Millisecond
	manager.reconnect = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)
	start := starter.waitForStart(t)
	defer start.close()
	closed := make(chan struct{})
	go func() {
		buf := make([]byte, 1024)
		for {
			if _, err := start.remoteInput.Read(buf); err != nil {
				close(closed)
				return
			}
		}
	}()

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("handshake timeout did not close stalled stream")
	}
}

func TestSSHSyncManagerPreservesPendingStateUntilSuccessfulWrite(t *testing.T) {
	manager := &sshSyncManager{pending: map[string]sshOutboundState{}, sessions: map[string]*sshPeerSession{}}
	session := &sshPeerSession{peer: sshSyncPeer{id: "linux-b"}}
	manager.sessions["linux-b"] = session
	oldState := sshOutboundState{content: clipboard.Content{ID: "old"}}
	newState := sshOutboundState{content: clipboard.Content{ID: "new"}}

	manager.rememberPending("linux-b", oldState)
	manager.clearPendingIfSessionCurrent(session, newState)
	if got := manager.pending["linux-b"].content.ID; got != "old" {
		t.Fatalf("pending after mismatched clear = %q, want old", got)
	}

	manager.clearPendingIfSessionCurrent(session, oldState)
	if _, ok := manager.pending["linux-b"]; ok {
		t.Fatal("pending state was not cleared after matching successful write")
	}
}

func TestSSHSyncManagerAckClearsOnlyMatchingInflightState(t *testing.T) {
	manager := &sshSyncManager{pending: map[string]sshOutboundState{}, sessions: map[string]*sshPeerSession{}}
	session := &sshPeerSession{peer: sshSyncPeer{id: "linux-b"}}
	manager.sessions["linux-b"] = session
	oldState := sshOutboundState{content: clipboard.Content{ID: "old"}}
	newState := sshOutboundState{content: clipboard.Content{ID: "new"}}
	inflight := map[uint64]sshOutboundState{1: oldState}
	manager.rememberPending("linux-b", newState)

	manager.handleSSHStreamAck(session, inflight, transport.SSHStreamAckResult{Seq: 1, ID: "old", Status: "applied"})
	if got := manager.pending["linux-b"].content.ID; got != "new" {
		t.Fatalf("old ack cleared newer pending state = %q, want new", got)
	}
	if _, ok := inflight[1]; ok {
		t.Fatal("acked state remained inflight")
	}

	inflight[2] = newState
	manager.handleSSHStreamAck(session, inflight, transport.SSHStreamAckResult{Seq: 2, ID: "new", Status: "applied"})
	if _, ok := manager.pending["linux-b"]; ok {
		t.Fatal("matching ack did not clear pending state")
	}
}

func TestSSHSyncManagerNonQualifyingAckDoesNotClearPendingState(t *testing.T) {
	manager := &sshSyncManager{pending: map[string]sshOutboundState{}, sessions: map[string]*sshPeerSession{}}
	session := &sshPeerSession{peer: sshSyncPeer{id: "linux-b"}}
	manager.sessions["linux-b"] = session
	state := sshOutboundState{content: clipboard.Content{ID: "clip"}}
	inflight := map[uint64]sshOutboundState{1: state}
	manager.rememberPending("linux-b", state)

	manager.handleSSHStreamAck(session, inflight, transport.SSHStreamAckResult{Seq: 1, ID: "clip", Status: "ignored_older"})

	if got := manager.pending["linux-b"].content.ID; got != "clip" {
		t.Fatalf("non-qualifying ack cleared pending state = %q, want clip", got)
	}
}

func TestSSHSyncManagerRejectedAckDropsPendingState(t *testing.T) {
	manager := &sshSyncManager{pending: map[string]sshOutboundState{}, sessions: map[string]*sshPeerSession{}}
	session := &sshPeerSession{peer: sshSyncPeer{id: "linux-b"}}
	manager.sessions["linux-b"] = session
	state := sshOutboundState{content: clipboard.Content{ID: "clip-poison"}}
	inflight := map[uint64]sshOutboundState{1: state}
	manager.rememberPending("linux-b", state)

	removed := manager.handleSSHStreamAck(session, inflight, transport.SSHStreamAckResult{Seq: 1, ID: "clip-poison", Status: "rejected", Reason: "local_apply_failed"})

	if !removed {
		t.Fatal("rejected ack did not remove inflight state")
	}
	if _, ok := manager.pending["linux-b"]; ok {
		t.Fatal("rejected ack left poison clip in pending; it would be resent on every reconnect")
	}
}

func TestSSHSyncManagerRejectedAckForReplacedPendingKeepsNewerState(t *testing.T) {
	manager := &sshSyncManager{pending: map[string]sshOutboundState{}, sessions: map[string]*sshPeerSession{}}
	session := &sshPeerSession{peer: sshSyncPeer{id: "linux-b"}}
	manager.sessions["linux-b"] = session
	oldState := sshOutboundState{content: clipboard.Content{ID: "clip-old"}}
	inflight := map[uint64]sshOutboundState{1: oldState}
	manager.rememberPending("linux-b", sshOutboundState{content: clipboard.Content{ID: "clip-new"}})

	manager.handleSSHStreamAck(session, inflight, transport.SSHStreamAckResult{Seq: 1, ID: "clip-old", Status: "rejected", Reason: "local_apply_failed"})

	if got := manager.pending["linux-b"].content.ID; got != "clip-new" {
		t.Fatalf("rejection of an old clip cleared newer pending state = %q, want clip-new", got)
	}
}

func TestSSHSyncManagerStaleReplacedSessionAckDoesNotClearPendingState(t *testing.T) {
	cfg := sshSyncManagerTestConfig()
	auth, err := transport.NewAuth(cfg.SharedKey)
	if err != nil {
		t.Fatal(err)
	}
	starter := newFakeSSHProcessStarter()
	manager := newSSHSyncManager(cfg, auth, "m4", nil, starter)
	manager.reconnect = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)
	first := starter.waitForStart(t)
	defer first.close()

	manager.mu.RLock()
	oldSession := manager.sessions["linux-b"]
	manager.mu.RUnlock()
	if oldSession == nil {
		t.Fatal("old session missing before refresh")
	}

	content := clipboard.New(clipboard.KindText, []byte("queued"), fixedTime)
	content.ID = "clip-same-id"
	state := sshOutboundState{content: content, origin: "m4"}
	manager.rememberPending("linux-b", state)

	nextCfg := sshSyncManagerTestConfig()
	nextCfg.SSH.Peers[0].SSHHost = "linux-b-new.example.com"
	manager.Refresh(ctx, nextCfg)
	second := starter.waitForStart(t)
	defer second.close()

	manager.mu.RLock()
	newSession := manager.sessions["linux-b"]
	manager.mu.RUnlock()
	if newSession == nil || newSession == oldSession {
		t.Fatalf("replacement session = %#v, old = %#v; want distinct replacement", newSession, oldSession)
	}
	if manager.markPeerConnectingIfSessionCurrent(oldSession) {
		t.Fatal("stale session marked peer connecting")
	}
	if manager.markPeerConnectedIfSessionCurrent(oldSession, fixedTime) {
		t.Fatal("stale session marked peer connected")
	}
	if manager.markPeerReceivedIfSessionCurrent(oldSession, fixedTime) {
		t.Fatal("stale session marked peer received")
	}
	var staleReceiveCalled bool
	manager.onReceive = func(clipboard.Content, string) {
		staleReceiveCalled = true
	}
	if manager.receiveContentIfSessionCurrent(oldSession, content, "linux-b", fixedTime) {
		t.Fatal("stale session applied received content")
	}
	manager.onReceive = nil
	if staleReceiveCalled {
		t.Fatal("stale session invoked receive callback")
	}
	if manager.markPeerErrorIfSessionCurrent(oldSession, io.ErrClosedPipe) {
		t.Fatal("stale session marked peer errored")
	}
	staleTransitionSnapshot := manager.Snapshot()["linux-b"]
	if staleTransitionSnapshot.Active || !staleTransitionSnapshot.LastConnectTS.IsZero() || !staleTransitionSnapshot.LastRecvTS.IsZero() || staleTransitionSnapshot.LastError != "" {
		t.Fatalf("stale session updated runtime snapshot = %#v, want no connect/receive", staleTransitionSnapshot)
	}
	inflight := map[uint64]sshOutboundState{1: state}
	manager.handleSSHStreamAck(oldSession, inflight, transport.SSHStreamAckResult{Seq: 1, ID: "clip-same-id", Status: "applied"})
	if got, ok := manager.pendingState("linux-b"); !ok || got.content.ID != "clip-same-id" {
		t.Fatalf("pending after stale ack = (%#v,%v), want clip-same-id preserved", got, ok)
	}
	staleSnapshot := manager.Snapshot()["linux-b"]
	if staleSnapshot.Active || !staleSnapshot.LastAckTS.IsZero() {
		t.Fatalf("stale ack updated runtime snapshot = %#v, want no live ack", staleSnapshot)
	}

	inflight[2] = state
	manager.handleSSHStreamAck(newSession, inflight, transport.SSHStreamAckResult{Seq: 2, ID: "clip-same-id", Status: "applied"})
	if _, ok := manager.pendingState("linux-b"); ok {
		t.Fatal("current session ack did not clear matching pending state")
	}
	currentSnapshot := manager.Snapshot()["linux-b"]
	if !currentSnapshot.Active || currentSnapshot.LastAckTS.IsZero() {
		t.Fatalf("current ack runtime snapshot = %#v, want live ack", currentSnapshot)
	}
	if manager.markPeerPendingIfSessionCurrent(oldSession, true) {
		t.Fatal("stale session marked peer pending")
	}
	afterStalePending := manager.Snapshot()["linux-b"]
	if afterStalePending.Pending || afterStalePending.Status != "live" {
		t.Fatalf("stale pending updated runtime snapshot = %#v, want live without pending", afterStalePending)
	}
	if manager.markPeerErrorIfSessionCurrent(oldSession, io.ErrClosedPipe) {
		t.Fatal("stale session marked live peer errored")
	}
	afterStaleError := manager.Snapshot()["linux-b"]
	if !afterStaleError.Active || afterStaleError.Status != "live" || afterStaleError.LastError != "" {
		t.Fatalf("stale error updated runtime snapshot = %#v, want live without error", afterStaleError)
	}
}

func TestSSHSyncManagerWriteFrameTimeoutCancelsAttempt(t *testing.T) {
	manager := &sshSyncManager{frameWriteTimeout: 20 * time.Millisecond}
	released := make(chan struct{})
	canceled := make(chan struct{})

	err := manager.writeSSHFrame(context.Background(), func() {
		close(canceled)
		close(released)
	}, func(context.Context) error {
		<-released
		return io.ErrClosedPipe
	})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("writeSSHFrame error = %v, want context deadline", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("write timeout did not cancel SSH attempt")
	}
}

func TestSSHSyncManagerFailedOldWriteDoesNotOverwriteNewerPendingState(t *testing.T) {
	manager := &sshSyncManager{pending: map[string]sshOutboundState{}, sessions: map[string]*sshPeerSession{}}
	session := &sshPeerSession{peer: sshSyncPeer{id: "linux-b"}}
	manager.sessions["linux-b"] = session
	oldState := sshOutboundState{content: clipboard.Content{ID: "old"}}
	newState := sshOutboundState{content: clipboard.Content{ID: "new"}}

	manager.rememberPending("linux-b", newState)
	manager.rememberPendingIfSessionCurrent(session, oldState)

	if got := manager.pending["linux-b"].content.ID; got != "new" {
		t.Fatalf("pending after failed old write = %q, want new", got)
	}

	manager.sessions["linux-b"] = &sshPeerSession{peer: sshSyncPeer{id: "linux-b"}}
	delete(manager.pending, "linux-b")
	manager.rememberPendingIfSessionCurrent(session, oldState)
	if _, ok := manager.pending["linux-b"]; ok {
		t.Fatal("stale replaced session write reinserted pending state")
	}
}

func TestDrainQueuedDuplicateSSHStateDropsOnlyMatchingClip(t *testing.T) {
	session := &sshPeerSession{send: make(chan sshOutboundState, 1)}
	state := sshOutboundState{content: clipboard.Content{ID: "same"}}
	session.send <- state

	drainQueuedDuplicateSSHState(session, state)

	select {
	case got := <-session.send:
		t.Fatalf("duplicate state remained queued: %#v", got)
	default:
	}

	newer := sshOutboundState{content: clipboard.Content{ID: "newer"}}
	session.send <- newer
	drainQueuedDuplicateSSHState(session, state)
	select {
	case got := <-session.send:
		if got.content.ID != "newer" {
			t.Fatalf("queued state = %#v, want newer", got)
		}
	default:
		t.Fatal("different queued state was dropped")
	}
}

func sshSyncManagerTestConfig() *config.Config {
	return &config.Config{
		Hostname:  "m4",
		SharedKey: config.NewSharedKey(),
		Transport: config.TransportSSH,
		SSH: &config.SSHConfig{
			SyncKey:    "/home/jesse/.config/clipfan/ssh/sync_ed25519",
			KnownHosts: "/home/jesse/.config/clipfan/ssh/known_hosts",
			Peers: []config.SSHPeer{{
				ID:             "linux-b",
				SSHHost:        "linux-b.example.com",
				SSHUser:        "jesse",
				SSHPort:        22,
				Enabled:        true,
				Connect:        true,
				Persistent:     true,
				MigrationState: config.MigrationStateSSHKeysReady,
				Proof:          readySSHProofForTest(),
			}},
		},
	}
}

func readySSHPeerForTest(id string) config.SSHPeer {
	return config.SSHPeer{
		ID:             id,
		SSHHost:        id + ".example.com",
		SSHUser:        "jesse",
		SSHPort:        22,
		Enabled:        true,
		Connect:        true,
		Persistent:     true,
		MigrationState: config.MigrationStateSSHKeysReady,
		Proof:          readySSHProofForTest(),
	}
}

func readySSHProofForTest() config.SSHProof {
	return config.SSHProof{
		AcceptKeyID:        "key-accept-123",
		AcceptGatewayPath:  "/home/jesse/.local/bin/clipfan",
		AcceptVerifiedAt:   "2026-06-03T12:00:00Z",
		AcceptVerifiedBy:   config.ProofVerifiedByRegularSSH,
		ConnectKeyID:       "key-connect-123",
		ConnectGatewayPath: "/home/jesse/.local/bin/clipfan",
		ConnectVerifiedAt:  "2026-06-03T12:00:00Z",
		ConnectVerifiedBy:  config.ProofVerifiedByRegularSSH,
	}
}

func mustDaemonSSHHello(t *testing.T, auth *transport.Auth, hostID string, peerID string) transport.SSHStreamHello {
	t.Helper()
	hello, err := transport.NewSSHStreamHello(auth, transport.SSHStreamPurposeSyncStream, hostID, peerID, time.Now(), "")
	if err != nil {
		t.Fatalf("NewSSHStreamHello error = %v", err)
	}
	return hello
}

func assertSyncStreamCommand(t *testing.T, cmd sshprovision.SSHCommand) {
	t.Helper()
	if len(cmd.Args) == 0 {
		t.Fatal("empty command")
	}
	if got := cmd.Args[len(cmd.Args)-1]; got != sshprovision.SSHGatewaySyncStreamCommand {
		t.Fatalf("last command arg = %q, want sync-stream; args=%#v", got, cmd.Args)
	}
}

func assertDirectSyncStreamCommand(t *testing.T, cmd sshprovision.SSHCommand) {
	t.Helper()
	if len(cmd.Args) == 0 {
		t.Fatal("empty command")
	}
	remote := cmd.Args[len(cmd.Args)-1]
	for _, want := range []string{
		"'ssh-gateway'",
		"'--authorized-peer' 'm4'",
		"'--authorized-key-id' 'key-connect-123'",
		"'--direct-command' 'sync-stream'",
	} {
		if !strings.Contains(remote, want) {
			t.Fatalf("remote command = %q, want %q; args=%#v", remote, want, cmd.Args)
		}
	}
	if strings.TrimSpace(remote) == sshprovision.SSHGatewaySyncStreamCommand {
		t.Fatalf("direct command used raw sync-stream: %#v", cmd.Args)
	}
}

func waitForPendingAbsent(t *testing.T, manager *sshSyncManager, peerID string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			if state, ok := manager.pendingState(peerID); ok {
				t.Fatalf("pending state still present after ack: %#v", state)
			}
			return
		case <-ticker.C:
			if _, ok := manager.pendingState(peerID); !ok {
				return
			}
		}
	}
}

type fakeSSHProcessStarter struct {
	started chan fakeSSHProcessStart
}

type fakeSSHProcessStart struct {
	cmd          sshprovision.SSHCommand
	remoteInput  io.ReadCloser
	remoteOutput io.WriteCloser
}

func (s fakeSSHProcessStart) close() {
	_ = s.remoteInput.Close()
	_ = s.remoteOutput.Close()
}

func newFakeSSHProcessStarter() *fakeSSHProcessStarter {
	return &fakeSSHProcessStarter{started: make(chan fakeSSHProcessStart, 1)}
}

func (s *fakeSSHProcessStarter) Start(_ context.Context, cmd sshprovision.SSHCommand) (sshStartedProcess, error) {
	remoteInput, processStdin := io.Pipe()
	processStdout, remoteOutput := io.Pipe()
	process := &fakeSSHProcess{stdin: processStdin, stdout: processStdout}
	s.started <- fakeSSHProcessStart{cmd: cmd, remoteInput: remoteInput, remoteOutput: remoteOutput}
	return process, nil
}

func (s *fakeSSHProcessStarter) waitForStart(t *testing.T) fakeSSHProcessStart {
	t.Helper()
	select {
	case start := <-s.started:
		return start
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for fake ssh process start")
		return fakeSSHProcessStart{}
	}
}

func TestSSHSyncManagerPublishSkipsOversizedClip(t *testing.T) {
	manager := &sshSyncManager{
		peers:    []sshSyncPeer{{id: "linux-b"}},
		pending:  map[string]sshOutboundState{},
		sessions: map[string]*sshPeerSession{},
	}
	session := &sshPeerSession{peer: sshSyncPeer{id: "linux-b"}, send: make(chan sshOutboundState, 1)}
	manager.sessions["linux-b"] = session

	big := clipboard.Content{ID: "clip-huge", Bytes: make([]byte, transport.MaxSSHStreamPayloadBytes+1)}
	manager.Publish(context.Background(), big, "m4", "")

	if _, ok := manager.pending["linux-b"]; ok {
		t.Fatal("oversized clip was queued as pending; it can never be written to the stream")
	}
	select {
	case state := <-session.send:
		t.Fatalf("oversized clip enqueued to session send: %q", state.content.ID)
	default:
	}
}

type fakeSSHProcess struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	once   sync.Once
	done   chan struct{}
}

func (p *fakeSSHProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *fakeSSHProcess) Stdout() io.ReadCloser { return p.stdout }

func (p *fakeSSHProcess) Wait() error {
	p.once.Do(func() {
		if p.done == nil {
			p.done = make(chan struct{})
		}
		close(p.done)
	})
	return nil
}
