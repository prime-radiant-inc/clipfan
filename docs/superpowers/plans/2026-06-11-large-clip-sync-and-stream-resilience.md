# Large-Clip Sync and Stream Resilience Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Large clips (up to the existing 64 MiB stream payload limit) sync successfully across the mesh, and a clip that a peer cannot apply never kills the sync stream or poisons the reconnect loop.

**Architecture:** Five independent fixes layered for defense in depth. (1) The remote daemon's `POST /v1/current` body cap rises from 1 MiB to the existing 90 MiB frame limit, and oversized bodies are rejected with an explicit 413 instead of being silently truncated into an HMAC 401. (2) The SSH gateway treats a failed local apply as a per-clip "rejected" ack instead of a fatal stream error, and tolerates transient local-daemon poll failures. (3) The sync client drops a clip from `pending` when a peer rejects it, breaking the infinite resend loop. (4) `Publish` refuses to queue clips larger than the stream payload limit, so an un-sendable clip can never become pending. (5) The daemon captures ssh stderr into its log so gateway-side errors are visible.

**Tech Stack:** Go (daemon/transport/CLI), existing test harnesses in `internal/transport`, `internal/cli`, `internal/daemon`.

**Background (root cause, diagnosed 2026-06-11):** The SSH sync stream carries payloads up to `MaxSSHStreamPayloadBytes` (64 MiB, frame cap `MaxSSHStreamFrameBytes` = 90 MiB), but the receiving gateway's `POST /v1/current` to its local daemon was capped at 1 MiB by `readSignedLocalWithRequiredAuthVersion` (`internal/transport/server.go:887`). `io.LimitReader` silently truncated the body, the request HMAC no longer matched, the daemon returned 401, the gateway acked `rejected/local_apply_failed` and then exited (killing the stream), and the client — for which a `rejected` ack is non-qualifying — kept the clip in `pending` and resent it on every 1-second reconnect, forever. Verified by manual protocol probe: a 2 MiB clip produced `current apply to 127.0.0.1: status 401` + rejected ack + stream death; a 26-byte clip applied cleanly.

---

### Task 1: transport — raise `/v1/current` body cap and reject oversized bodies with 413

The 1 MiB cap in `readSignedLocalWithRequiredAuthVersion` is shared by all signed loopback routes. Keep 1 MiB as the default but let routes pass their own cap, and make every signed read fail loudly (413) instead of silently truncating.

**Files:**
- Modify: `internal/transport/server.go` (`readSignedLocalWithRequiredAuthVersion` ~line 882, `readSignedWithRequiredAuthVersion` ~line 896, `postCurrent` ~line 584)
- Test: `internal/transport/current_endpoint_test.go`, `internal/transport/server_body_limit_test.go` (new)

- [ ] **Step 1: Write the failing test for a >1 MiB clip applying successfully**

Add to `internal/transport/current_endpoint_test.go` (mirror the style of the existing POST test at ~line 70; the helpers `testAuth`, `setFixedServerTime`, `signedRequestWithTimestampAndNonceAndAuthVersion`, `requireVersionedSignedResponse` already exist in this package):

```go
func TestPostCurrentAcceptsLargeClipBody(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, func() any { return nil })
	srv.SetRequiredLocalAuthVersion(AuthVersionRequestHMAC)
	setFixedServerTime(srv)
	var gotContent clipboard.Content
	srv.SetCurrentApply(func(content clipboard.Content, origin string) error {
		gotContent = content
		return nil
	})
	big := bytes.Repeat([]byte("A"), 2<<20) // 2 MiB: over the old 1 MiB cap, far under the frame cap
	content := clipboard.New(clipboard.KindText, big, time.Unix(1780257600, 0).UTC())
	content.ID = "clip-large-apply"
	body, err := json.Marshal(CurrentPayloadFromContent(content, "linux-a"))
	if err != nil {
		t.Fatal(err)
	}

	req := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodPost, "/v1/current", "1780257600", "current-large", body, AuthVersionRequestHMAC)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("large current apply status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	if gotContent.ID != "clip-large-apply" || len(gotContent.Bytes) != len(big) {
		t.Fatalf("applied content = id %q, %d bytes; want clip-large-apply, %d bytes", gotContent.ID, len(gotContent.Bytes), len(big))
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./internal/transport -run TestPostCurrentAcceptsLargeClipBody -v`
Expected: FAIL — status 401 (truncated body breaks the HMAC), not 200.

- [ ] **Step 3: Write the failing test for explicit 413 on oversized bodies**

Create `internal/transport/server_body_limit_test.go`. This unit-tests the shared reader with a tiny cap so we don't allocate 90 MiB in tests:

```go
package transport

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadSignedRejectsOversizedBodyWith413(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, func() any { return nil })
	setFixedServerTime(srv)

	body := bytes.Repeat([]byte("x"), 16)
	req := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodPost, "/v1/limit-test", "1780257600", "limit-test", body, AuthVersionRequestHMAC)
	rec := httptest.NewRecorder()
	if signed := srv.readSignedWithRequiredAuthVersion(rec, req, 8, AuthVersionRequestHMAC); signed != nil {
		t.Fatal("oversized body returned a signed payload")
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d, want 413", rec.Code)
	}
}
```

- [ ] **Step 4: Run it to make sure it fails**

Run: `go test ./internal/transport -run TestReadSignedRejectsOversizedBodyWith413 -v`
Expected: FAIL — currently the body is truncated to 8 bytes and the HMAC check returns 401, not 413.

- [ ] **Step 5: Implement the transport changes**

In `internal/transport/server.go`:

(a) Give the local-signed reader a per-route cap. Replace the existing `readSignedLocalWithRequiredAuthVersion` body (~line 882):

```go
func (s *Server) readSignedLocalWithRequiredAuthVersion(w http.ResponseWriter, r *http.Request, requiredAuthVersion string) *signedPayload {
	return s.readSignedLocalWithRequiredAuthVersionMax(w, r, requiredAuthVersion, 1<<20)
}

func (s *Server) readSignedLocalWithRequiredAuthVersionMax(w http.ResponseWriter, r *http.Request, requiredAuthVersion string, maxBody int64) *signedPayload {
	if !isLoopbackRemote(r.RemoteAddr) {
		http.Error(w, "loopback required", http.StatusForbidden)
		return nil
	}
	return s.readSignedWithRequiredAuthVersion(w, r, maxBody, requiredAuthVersion)
}
```

(b) In `readSignedWithRequiredAuthVersion` (~line 896), replace the silent truncation:

```go
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return nil
	}
```

with an explicit oversize rejection (read one extra byte to detect overflow):

```go
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return nil
	}
	if int64(len(body)) > maxBody {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return nil
	}
```

(c) In `postCurrent` (~line 584), use the frame cap — a `CurrentPayload` JSON envelope around a base64-encoded 64 MiB clip always fits in 90 MiB (base64 inflates by 4/3: ~85.4 MiB + envelope):

```go
func (s *Server) postCurrent(w http.ResponseWriter, r *http.Request) {
	signed := s.readSignedLocalWithRequiredAuthVersionMax(w, r, AuthVersionRequestHMAC, MaxSSHStreamFrameBytes)
```

(rest of the function unchanged).

- [ ] **Step 6: Run the package tests**

Run: `go test ./internal/transport -v -run 'TestPostCurrent|TestReadSigned|TestCurrent'`
Expected: PASS, including the two new tests.

Run: `go test ./internal/transport`
Expected: PASS (no regressions in the rest of the package).

- [ ] **Step 7: Commit**

```bash
git add internal/transport/server.go internal/transport/current_endpoint_test.go internal/transport/server_body_limit_test.go
git commit -m "fix(transport): raise /v1/current body cap to frame limit; 413 instead of silent truncation"
```

---

### Task 2: gateway — a failed local apply must not kill the sync stream

`handleSSHGatewayState` (`internal/cli/ssh_gateway.go:215`) already writes a `rejected/local_apply_failed` ack when the push to the local daemon fails — but then returns the error, which exits `runDefaultSSHGatewaySyncStream` and tears down the whole stream. The rejected ack is the complete per-clip answer; the stream must continue. Also make the 250 ms current-poll (`publishSSHGatewayCurrent`) tolerate transient local-daemon failures (e.g. the daemon restarting) instead of dying on the first error, with a bounded consecutive-failure limit so a permanently broken daemon doesn't leave a zombie stream.

**Files:**
- Modify: `internal/cli/ssh_gateway.go` (`runDefaultSSHGatewaySyncStream` ~line 161-186, `handleSSHGatewayState` ~line 215)
- Test: `internal/cli/ssh_gateway_test.go`

- [ ] **Step 1: Write the failing test for stream survival after a rejected apply**

Add to `internal/cli/ssh_gateway_test.go`, mirroring `TestRunSSHGatewayDefaultSyncStreamBridgesStateToLocalDaemon` (line 167) — same server/config scaffolding (`writeGatewayConfig` helper already exists), but the apply callback fails once and the initiator sends a second state that must still be processed:

```go
func TestRunSSHGatewayDefaultSyncStreamSurvivesRejectedApply(t *testing.T) {
	configRoot := t.TempDir()
	stateRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_STATE_HOME", stateRoot)
	sharedKey := config.NewSharedKey()
	auth, err := transport.NewAuth(sharedKey)
	if err != nil {
		t.Fatal(err)
	}
	applied := make(chan string, 2)
	srv := transport.NewServer("127.0.0.1:0", auth, nil)
	srv.SetCurrentApply(func(c clipboard.Content, origin string) error {
		applied <- c.ID
		if c.ID == "clip-poison" {
			return fmt.Errorf("apply refused")
		}
		return nil
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ServeListener(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		<-serveErr
	})
	writeGatewayConfig(t, sharedKey, ln.Addr().(*net.TCPAddr).Port)

	var stdin, stdout, stderr bytes.Buffer
	initiator := transport.NewSSHSyncStream(auth, "m4", "linux-b", bytes.NewReader(nil), &stdin)
	hello, err := transport.NewSSHStreamHello(auth, transport.SSHStreamPurposeSyncStream, "m4", "linux-b", time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := initiator.WriteHello(context.Background(), hello); err != nil {
		t.Fatal(err)
	}
	poison := clipboard.New(clipboard.KindText, []byte("poison"), time.Now().UTC())
	poison.ID = "clip-poison"
	if err := initiator.WriteState(context.Background(), 1, poison, "m4"); err != nil {
		t.Fatal(err)
	}
	good := clipboard.New(clipboard.KindText, []byte("good"), time.Now().UTC())
	good.ID = "clip-good"
	if err := initiator.WriteState(context.Background(), 2, good, "m4"); err != nil {
		t.Fatal(err)
	}

	err = runSSHGateway(
		[]string{"--authorized-peer", "m4", "--authorized-key-id", "key-123456"},
		&stdin,
		&stdout,
		&stderr,
		func(key string) string {
			if key == "SSH_ORIGINAL_COMMAND" {
				return sshprovision.SSHGatewaySyncStreamCommand
			}
			return ""
		},
	)
	if err != nil {
		t.Fatalf("runSSHGateway() error = %v; stderr=%q — rejected apply must not be fatal", err, stderr.String())
	}

	reader := transport.NewSSHSyncStream(auth, "m4", "linux-b", &stdout, io.Discard)
	reader.SetHelloNonceCache(transport.NewSSHStreamHelloNonceCache())
	if _, err := reader.ReadHello(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	first, err := reader.ReadNext(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if first.Type != transport.SSHStreamFrameAck || first.Ack.Seq != 1 || first.Ack.Status != "rejected" || first.Ack.Reason != "local_apply_failed" {
		t.Fatalf("first ack = %#v, want seq 1 rejected/local_apply_failed", first)
	}
	second, err := reader.ReadNext(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("stream did not continue past rejected apply: %v", err)
	}
	if second.Type != transport.SSHStreamFrameAck || second.Ack.Seq != 2 || second.Ack.ID != "clip-good" || second.Ack.Status != "applied" {
		t.Fatalf("second ack = %#v, want seq 2 clip-good applied", second)
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./internal/cli -run TestRunSSHGatewayDefaultSyncStreamSurvivesRejectedApply -v`
Expected: FAIL — `runSSHGateway()` returns the apply error and the second state never gets an ack.

- [ ] **Step 3: Make a rejected apply non-fatal**

In `internal/cli/ssh_gateway.go`, `handleSSHGatewayState` (~line 215), the push-failure branch currently writes the rejected ack and then `return err`. Change it to return nil after the ack is written — the ack IS the error report (the initiating client logs and drops the clip; Task 3):

```go
	if err := pushSSHGatewayStateToLocalDaemon(ctx, client, localHost, localPort, state.Content, state.Origin); err != nil {
		// The rejected ack is the per-clip failure report; the stream itself is
		// healthy, so a clip the local daemon refuses must not tear it down.
		return writeFrame(func(ctx context.Context) error {
			return stream.WriteAck(ctx, state.Seq, state.Content.ID, "rejected", "local_apply_failed")
		})
	}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/cli -run TestRunSSHGatewayDefaultSyncStreamSurvivesRejectedApply -v`
Expected: PASS

- [ ] **Step 5: Write the failing test for poll-failure tolerance**

The sync-stream loop calls `publishSSHGatewayCurrent` every 250 ms (`sshGatewayCurrentPollInterval`, a package var — override it in the test); today the first poll error kills the stream. With no `currentFn` wired, the local daemon answers GET `/v1/current` with 503, so every tick fails. The gateway must survive a few failing ticks and still exit cleanly on stdin EOF:

```go
func TestRunSSHGatewayDefaultSyncStreamToleratesCurrentPollFailures(t *testing.T) {
	configRoot := t.TempDir()
	stateRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_STATE_HOME", stateRoot)
	sharedKey := config.NewSharedKey()
	auth, err := transport.NewAuth(sharedKey)
	if err != nil {
		t.Fatal(err)
	}
	// No SetCurrentFunc: every GET /v1/current returns 503, so every poll tick fails.
	srv := transport.NewServer("127.0.0.1:0", auth, nil)
	srv.SetCurrentApply(func(c clipboard.Content, origin string) error { return nil })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ServeListener(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		<-serveErr
	})
	writeGatewayConfig(t, sharedKey, ln.Addr().(*net.TCPAddr).Port)

	oldInterval := sshGatewayCurrentPollInterval
	sshGatewayCurrentPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { sshGatewayCurrentPollInterval = oldInterval })

	// stdin carries a hello, then a pause long enough for several failing poll
	// ticks, then EOF. An io.Pipe gives us the pause.
	pr, pw := io.Pipe()
	initiator := transport.NewSSHSyncStream(auth, "m4", "linux-b", bytes.NewReader(nil), pw)
	hello, err := transport.NewSSHStreamHello(auth, transport.SSHStreamPurposeSyncStream, "m4", "linux-b", time.Now(), "")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = initiator.WriteHello(context.Background(), hello)
		time.Sleep(100 * time.Millisecond) // ~20 failing poll ticks
		_ = pw.Close()
	}()

	var stdout, stderr bytes.Buffer
	err = runSSHGateway(
		[]string{"--authorized-peer", "m4", "--authorized-key-id", "key-123456"},
		pr,
		&stdout,
		&stderr,
		func(key string) string {
			if key == "SSH_ORIGINAL_COMMAND" {
				return sshprovision.SSHGatewaySyncStreamCommand
			}
			return ""
		},
	)
	if err != nil {
		t.Fatalf("runSSHGateway() error = %v — transient poll failures must not be fatal", err)
	}
}
```

- [ ] **Step 6: Run it to make sure it fails**

Run: `go test ./internal/cli -run TestRunSSHGatewayDefaultSyncStreamToleratesCurrentPollFailures -v`
Expected: FAIL — the first failing tick returns its error from `runDefaultSSHGatewaySyncStream`.

- [ ] **Step 7: Implement bounded poll-failure tolerance**

In `runDefaultSSHGatewaySyncStream` (~line 159), add a consecutive-failure counter around the ticker branch. 40 consecutive failures at the 250 ms production interval ≈ 10 s of a continuously unreachable local daemon — at that point the stream is useless and exiting lets the initiator's reconnect loop surface "attention":

```go
	const maxConsecutiveCurrentPollFailures = 40
	pollFailures := 0
```

and change the ticker case:

```go
		case <-ticker.C:
			if err := publishSSHGatewayCurrent(ctx, writeFrame, stream, client, localHost, localPort, identity.PeerID, &seq, sentCurrent); err != nil {
				pollFailures++
				if pollFailures >= maxConsecutiveCurrentPollFailures {
					return fmt.Errorf("local current poll failing persistently: %w", err)
				}
				continue
			}
			pollFailures = 0
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test ./internal/cli -run 'TestRunSSHGatewayDefaultSyncStream' -v`
Expected: PASS — both new tests and the two existing sync-stream tests.

- [ ] **Step 9: Commit**

```bash
git add internal/cli/ssh_gateway.go internal/cli/ssh_gateway_test.go
git commit -m "fix(gateway): rejected apply and transient current-poll failures no longer kill the sync stream"
```

---

### Task 3: sync client — a rejected ack drops the clip from pending

`qualifyingSSHStreamAck` (`internal/daemon/ssh_session.go:700`) treats `rejected` as non-qualifying, so the clip stays in `m.pending` and is resent on every reconnect — the infinite flap loop. A rejection is final for that clip: retrying the identical bytes can never succeed. Drop it from pending (for that peer) and log a warning. Acks like `ignored_older` keep their existing keep-pending semantics — only `rejected` changes.

**Files:**
- Modify: `internal/daemon/ssh_session.go` (`handleSSHStreamAck` ~line 687)
- Test: `internal/daemon/ssh_session_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/daemon/ssh_session_test.go`, next to `TestSSHSyncManagerNonQualifyingAckDoesNotClearPendingState` (line 552), in the same style:

```go
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
```

- [ ] **Step 2: Run them to make sure they fail**

Run: `go test ./internal/daemon -run 'TestSSHSyncManagerRejectedAck' -v`
Expected: the first test FAILS ("rejected ack left poison clip in pending"); the second already passes (`clearPendingIfSessionCurrent` matches by content ID) — keeping it pins the guard.

- [ ] **Step 3: Implement the drop**

In `internal/daemon/ssh_session.go`, replace `handleSSHStreamAck` (~line 687):

```go
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/daemon -run 'TestSSHSyncManager' -v`
Expected: PASS — both new tests and all existing ack tests (including `NonQualifyingAckDoesNotClearPendingState`, which uses `ignored_older` and must be untouched).

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/ssh_session.go internal/daemon/ssh_session_test.go
git commit -m "fix(daemon): drop clip from pending on peer rejection instead of resending forever"
```

---

### Task 4: sync client — never queue a clip the stream cannot carry

A clip over `MaxSSHStreamPayloadBytes` (64 MiB) would fail `WriteState` with `ErrSSHStreamPayloadTooLarge`, get re-remembered as pending, and poison the reconnect loop locally — same flap, no remote involved. Refuse it at `Publish` with a warning. (The gateway's reverse-direction poll already has this exact guard in `publishSSHGatewayCurrent`, `internal/cli/ssh_gateway.go:255`.)

**Files:**
- Modify: `internal/daemon/ssh_session.go` (`Publish` ~line 180)
- Test: `internal/daemon/ssh_session_test.go`

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./internal/daemon -run TestSSHSyncManagerPublishSkipsOversizedClip -v`
Expected: FAIL — the clip is queued today.

- [ ] **Step 3: Implement the guard**

At the top of `Publish` in `internal/daemon/ssh_session.go` (~line 180), before taking the lock:

```go
func (m *sshSyncManager) Publish(ctx context.Context, content clipboard.Content, origin string, skipOrigin string) {
	if len(content.Bytes) > transport.MaxSSHStreamPayloadBytes {
		slog.Warn("clip exceeds ssh stream payload limit; not syncing to peers", "clip", content.ID, "bytes", len(content.Bytes), "limit", transport.MaxSSHStreamPayloadBytes)
		return
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/daemon -v -run 'TestSSHSyncManagerPublish'`
Expected: PASS — new test plus the existing Publish tests.

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/ssh_session.go internal/daemon/ssh_session_test.go
git commit -m "fix(daemon): refuse to queue clips larger than the ssh stream payload limit"
```

---

### Task 5: observability — capture ssh stderr into the daemon log

`execSSHProcessStarter.Start` (`internal/daemon/ssh_session.go:776`) never wires `Stderr`, so `os/exec` discards it — the remote gateway's exit message (e.g. `clipfan ssh-gateway: current apply to 127.0.0.1: status 401`) was invisible during the whole incident. Stream stderr lines into `slog` at Warn, bounded per line.

**Files:**
- Modify: `internal/daemon/ssh_session.go` (`execSSHProcessStarter.Start` ~line 776)
- Test: `internal/daemon/ssh_session_test.go`

- [ ] **Step 1: Write the failing test for target extraction**

The log line should carry the `user@host` target so flaps are attributable per peer:

```go
func TestSSHTargetFromArgsFindsUserHost(t *testing.T) {
	args := []string{"ssh", "-F", "/dev/null", "-o", "BatchMode=yes", "-p", "22", "jesse@linux-b.example.com", "sync-stream"}
	if got := sshTargetFromArgs(args); got != "jesse@linux-b.example.com" {
		t.Fatalf("sshTargetFromArgs = %q, want jesse@linux-b.example.com", got)
	}
	if got := sshTargetFromArgs([]string{"ssh"}); got != "" {
		t.Fatalf("sshTargetFromArgs with no target = %q, want empty", got)
	}
}
```

- [ ] **Step 2: Write the failing test for stderr capture**

`execSSHProcessStarter` runs a real argv, so a shell one-liner exercises the capture end-to-end. Capture slog output via a swapped default handler:

```go
func TestExecSSHProcessStarterLogsStderr(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	proc, err := execSSHProcessStarter{}.Start(context.Background(), sshprovision.SSHCommand{
		Args: []string{"/bin/sh", "-c", "echo gateway-exploded >&2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = proc.Stdin().Close()
	_ = proc.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(buf.String(), "gateway-exploded") {
		if time.Now().After(deadline) {
			t.Fatalf("stderr line never reached slog; log output: %q", buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
```

- [ ] **Step 3: Run both to make sure they fail**

Run: `go test ./internal/daemon -run 'TestSSHTargetFromArgs|TestExecSSHProcessStarterLogsStderr' -v`
Expected: FAIL — `sshTargetFromArgs` undefined; stderr is currently discarded.

- [ ] **Step 4: Implement stderr capture**

In `internal/daemon/ssh_session.go`, add (near `execSSHProcessStarter`, ~line 774; add `bufio` and `strings` to imports):

```go
// sshTargetFromArgs extracts the user@host argument from an ssh argv for log
// attribution. Returns "" when no such argument exists.
func sshTargetFromArgs(args []string) string {
	for _, arg := range args {
		if strings.Contains(arg, "@") {
			return arg
		}
	}
	return ""
}

// logSSHStderr surfaces the remote gateway's stderr (its only error channel)
// into the daemon log, one bounded line at a time.
func logSSHStderr(r io.Reader, target string) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4096), 4096)
	for scanner.Scan() {
		slog.Warn("ssh stderr", "target", target, "line", scanner.Text())
	}
}
```

and in `execSSHProcessStarter.Start`, after the stdout pipe is created and before `process.Start()`:

```go
	stderr, err := process.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
```

then after the successful `process.Start()`:

```go
	go logSSHStderr(stderr, sshTargetFromArgs(cmd.Args))
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/daemon -run 'TestSSHTargetFromArgs|TestExecSSHProcessStarterLogsStderr' -v`
Expected: PASS

Run: `go test ./internal/daemon`
Expected: PASS (full package, no regressions).

- [ ] **Step 6: Commit**

```bash
git add internal/daemon/ssh_session.go internal/daemon/ssh_session_test.go
git commit -m "feat(daemon): log ssh stderr so remote gateway failures are visible"
```

---

### Task 6: full verification and changelog

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Run the whole test suite**

Run: `go test ./...`
Expected: PASS, every package.

- [ ] **Step 2: Run go vet**

Run: `go vet ./...`
Expected: no output.

- [ ] **Step 3: Add the changelog entry**

At the top of `CHANGELOG.md`, above the `## [1.0.6]` section:

```markdown
## [Unreleased]

### Fixed

- Large clips (up to the 64 MiB stream payload limit) now sync: the daemon's `/v1/current` apply endpoint accepts bodies up to the stream frame limit instead of silently truncating at 1 MiB and failing signature verification with a 401.
- A clip a peer cannot apply no longer kills the sync stream or poisons the reconnect loop: the gateway answers with a `rejected` ack and keeps serving, and the sending daemon drops the rejected clip from its pending queue instead of resending it on every reconnect.
- The gateway now tolerates transient local-daemon poll failures (up to ~10 s) instead of tearing down the stream on the first error.
- Clips larger than the stream payload limit are skipped at publish time with a logged warning instead of wedging the per-peer send queue.
- The daemon now logs the stderr of its ssh sync processes, so remote gateway errors (previously discarded) are visible in the daemon log.
```

- [ ] **Step 4: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs: changelog for large-clip sync and stream resilience fixes"
```

---

## Deployment / ops notes (not code tasks)

- **Fleet upgrade order matters for the full benefit but nothing regresses meanwhile.** Against a not-yet-upgraded peer (v1.0.0/v1.0.2), a >1 MiB clip still gets 401-rejected remotely and that gateway still exits — but the upgraded sender receives the `rejected` ack before the EOF, drops the clip from pending (Task 3), and reconnects once into a stable session instead of flapping forever. Once the peer is upgraded, large clips apply (Task 1) and its gateway survives rejections (Task 2).
- **flower-garden is a separate, config-level problem:** `flower-garden.local` no longer resolves from m4 (mDNS name gone), so that peer loops connecting/attention every ~5 s regardless of these fixes. Its peer entry needs re-pointing (Tailscale address / mesh-heal), and the daemons on the fleet are running older binaries (m4 v1.0.2 running, peers v1.0.0) — roll out the fixed build and restart.
- Clips over 64 MiB intentionally do not sync (logged warning). Raising `MaxSSHStreamPayloadBytes` is out of scope.
