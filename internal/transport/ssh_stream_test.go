package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

func TestSSHStreamStateFrameRoundTrip(t *testing.T) {
	auth := testAuth(t)
	ts := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	content := clipboard.New(clipboard.KindText, []byte("hello over ssh"), ts)
	content.ID = "clip-ssh-1"
	var buf bytes.Buffer
	writer := NewSSHSyncStream(auth, "m4", "linux-b", &buf, &buf)

	if err := writer.WriteState(context.Background(), 1, content, "m4"); err != nil {
		t.Fatalf("WriteState error = %v", err)
	}

	var raw SSHStreamFrame
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &raw); err != nil {
		t.Fatalf("state frame json error = %v", err)
	}
	if raw.Type != SSHStreamFrameState || raw.Seq != 1 || raw.Sender != "m4" || raw.Clip == nil {
		t.Fatalf("state frame = %#v", raw)
	}

	reader := NewSSHSyncStream(auth, "linux-b", "m4", &buf, io.Discard)
	result, err := reader.ReadStateFrame(context.Background(), ts)
	if err != nil {
		t.Fatalf("ReadStateFrame error = %v", err)
	}
	if result.Seq != 1 || result.Sender != "m4" || result.Origin != "m4" {
		t.Fatalf("state result metadata = %#v", result)
	}
	got := result.Content
	if got.ID != "clip-ssh-1" || got.Kind != clipboard.KindText || string(got.Bytes) != "hello over ssh" || !got.TS.Equal(ts) {
		t.Fatalf("content = %#v", got)
	}
}

func TestSSHStreamOppositePeerInstancesExchangeHelloAndState(t *testing.T) {
	auth := testAuth(t)
	ts := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	var aToB, bToA bytes.Buffer
	a := NewSSHSyncStream(auth, "m4", "linux-b", &bToA, &aToB)
	b := NewSSHSyncStream(auth, "linux-b", "m4", &aToB, &bToA)
	cacheA := NewSSHStreamHelloNonceCache()
	cacheB := NewSSHStreamHelloNonceCache()
	a.SetHelloNonceCache(cacheA)
	b.SetHelloNonceCache(cacheB)

	helloA := mustSSHStreamHello(t, auth, SSHStreamPurposeSyncStream, "m4", "linux-b", ts, "11111111111111111111111111111111")
	if err := a.WriteHello(context.Background(), helloA); err != nil {
		t.Fatalf("a WriteHello error = %v", err)
	}
	gotHelloA, err := b.ReadHello(context.Background(), ts)
	if err != nil {
		t.Fatalf("b ReadHello error = %v", err)
	}
	if gotHelloA.HostID != "m4" || gotHelloA.PeerID != "linux-b" {
		t.Fatalf("b received hello = %#v", gotHelloA)
	}

	helloB := mustSSHStreamHello(t, auth, SSHStreamPurposeSyncStream, "linux-b", "m4", ts, "22222222222222222222222222222222")
	if err := b.WriteHello(context.Background(), helloB); err != nil {
		t.Fatalf("b WriteHello error = %v", err)
	}
	gotHelloB, err := a.ReadHello(context.Background(), ts)
	if err != nil {
		t.Fatalf("a ReadHello error = %v", err)
	}
	if gotHelloB.HostID != "linux-b" || gotHelloB.PeerID != "m4" {
		t.Fatalf("a received hello = %#v", gotHelloB)
	}

	fromA := clipboard.New(clipboard.KindText, []byte("from m4"), ts)
	fromA.ID = "clip-from-a"
	if err := a.WriteState(context.Background(), 1, fromA, "m4"); err != nil {
		t.Fatalf("a WriteState error = %v", err)
	}
	gotA, originA, err := b.ReadState(context.Background(), ts)
	if err != nil {
		t.Fatalf("b ReadState error = %v", err)
	}
	if originA != "m4" || string(gotA.Bytes) != "from m4" {
		t.Fatalf("b received state origin=%q content=%q", originA, gotA.Bytes)
	}

	fromB := clipboard.New(clipboard.KindText, []byte("from linux-b"), ts)
	fromB.ID = "clip-from-b"
	if err := b.WriteState(context.Background(), 1, fromB, "linux-b"); err != nil {
		t.Fatalf("b WriteState error = %v", err)
	}
	gotB, originB, err := a.ReadState(context.Background(), ts)
	if err != nil {
		t.Fatalf("a ReadState error = %v", err)
	}
	if originB != "linux-b" || string(gotB.Bytes) != "from linux-b" {
		t.Fatalf("a received state origin=%q content=%q", originB, gotB.Bytes)
	}
}

func TestSSHStreamStateRejectsOutOfOrderSequence(t *testing.T) {
	auth := testAuth(t)
	raw := []byte(`{"type":"state","seq":2,"sender":"m4","null_reason":"no_visible_current"}`)
	stream := NewSSHSyncStream(auth, "linux-b", "m4", bytes.NewReader(append(raw, '\n')), io.Discard)

	_, _, err := stream.ReadState(context.Background(), time.Now())
	if !errors.Is(err, ErrSSHStreamUnexpectedFrame) {
		t.Fatalf("out-of-order seq error = %v, want ErrSSHStreamUnexpectedFrame", err)
	}
}

func TestSSHStreamStateRejectsDecryptedPayloadOverLimit(t *testing.T) {
	auth := testAuth(t)
	ts := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	content := clipboard.New(clipboard.KindText, []byte("too large"), ts)
	content.ID = "clip-too-large"
	var buf bytes.Buffer
	writer := NewSSHSyncStream(auth, "m4", "linux-b", &buf, &buf)
	if err := writer.WriteState(context.Background(), 1, content, "m4"); err != nil {
		t.Fatalf("WriteState error = %v", err)
	}
	reader := NewSSHSyncStream(auth, "linux-b", "m4", &buf, io.Discard)
	reader.maxPayloadBytes = 3

	_, _, err := reader.ReadState(context.Background(), ts)
	if !errors.Is(err, ErrSSHStreamPayloadTooLarge) {
		t.Fatalf("payload too large error = %v, want ErrSSHStreamPayloadTooLarge", err)
	}
}

func TestSSHStreamWriteStateRejectsPayloadOverLimitBeforeWriting(t *testing.T) {
	auth := testAuth(t)
	ts := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	content := clipboard.New(clipboard.KindText, []byte("too large"), ts)
	content.ID = "clip-too-large"
	var buf bytes.Buffer
	stream := NewSSHSyncStream(auth, "m4", "linux-b", bytes.NewReader(nil), &buf)
	stream.maxPayloadBytes = 3

	err := stream.WriteState(context.Background(), 1, content, "m4")
	if !errors.Is(err, ErrSSHStreamPayloadTooLarge) {
		t.Fatalf("WriteState error = %v, want ErrSSHStreamPayloadTooLarge", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("WriteState wrote %d bytes after rejecting payload", buf.Len())
	}
}

func TestSSHStreamNullStateRoundTrip(t *testing.T) {
	auth := testAuth(t)
	var buf bytes.Buffer
	writer := NewSSHSyncStream(auth, "m4", "linux-b", &buf, &buf)

	if err := writer.WriteNullState(context.Background(), 1, "no_visible_current"); err != nil {
		t.Fatalf("WriteNullState error = %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &raw); err != nil {
		t.Fatalf("null state frame json error = %v", err)
	}
	clip, ok := raw["clip"]
	if !ok || string(clip) != "null" {
		t.Fatalf("null state clip field = %q present=%v, want explicit null", clip, ok)
	}
	reader := NewSSHSyncStream(auth, "linux-b", "m4", &buf, io.Discard)
	result, err := reader.ReadStateFrame(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("ReadStateFrame error = %v", err)
	}
	if result.Seq != 1 || result.Sender != "m4" || result.NullReason != "no_visible_current" || result.Content.ID != "" {
		t.Fatalf("null state result = %#v", result)
	}
}

func TestSSHStreamAcceptsFramesLargerThanDefaultBufioBuffer(t *testing.T) {
	auth := testAuth(t)
	ts := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	body := bytes.Repeat([]byte("x"), 8192)
	content := clipboard.New(clipboard.KindText, body, ts)
	content.ID = "clip-large"
	var buf bytes.Buffer
	writer := NewSSHSyncStream(auth, "m4", "linux-b", &buf, &buf)

	if err := writer.WriteState(context.Background(), 1, content, "m4"); err != nil {
		t.Fatalf("WriteState error = %v", err)
	}
	reader := NewSSHSyncStream(auth, "linux-b", "m4", &buf, io.Discard)
	got, _, err := reader.ReadState(context.Background(), ts)
	if err != nil {
		t.Fatalf("ReadState error = %v", err)
	}
	if !bytes.Equal(got.Bytes, body) {
		t.Fatalf("body length = %d, want %d", len(got.Bytes), len(body))
	}
}

func TestSSHStreamHelloFrameTopLevelSignedShape(t *testing.T) {
	auth := testAuth(t)
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	writer := NewSSHSyncStream(auth, "m4", "linux-b", &buf, &buf)
	hello, err := NewSSHStreamHello(auth, SSHStreamPurposeSyncStream, "m4", "linux-b", now, "1234567890abcdef1234567890abcdef")
	if err != nil {
		t.Fatalf("NewSSHStreamHello error = %v", err)
	}

	if err := writer.WriteHello(context.Background(), hello); err != nil {
		t.Fatalf("WriteHello error = %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &raw); err != nil {
		t.Fatalf("hello frame json error = %v", err)
	}
	for _, key := range []string{"type", "protocols", "purpose", "host_id", "peer_id", "ts", "nonce", "sig"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("hello missing top-level key %q in %#v", key, raw)
		}
	}
	if _, ok := raw["hello"]; ok {
		t.Fatalf("hello frame must not nest hello object: %#v", raw)
	}

	reader := NewSSHSyncStream(auth, "linux-b", "m4", &buf, io.Discard)
	reader.SetHelloNonceCache(NewSSHStreamHelloNonceCache())
	got, err := reader.ReadHello(context.Background(), now)
	if err != nil {
		t.Fatalf("ReadHello error = %v", err)
	}
	if got.Purpose != SSHStreamPurposeSyncStream || got.HostID != "m4" || got.PeerID != "linux-b" || got.TS != now.Unix() || got.Sig == "" {
		t.Fatalf("hello = %#v", got)
	}
}

func TestSSHStreamHelloRejectsMismatchedIdentityAndPurpose(t *testing.T) {
	auth := testAuth(t)
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name    string
		hello   SSHStreamHello
		wantErr bool
	}{
		{
			name:    "wrong host id",
			hello:   mustSSHStreamHello(t, auth, SSHStreamPurposeSyncStream, "other", "linux-b", now, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			wantErr: true,
		},
		{
			name:    "wrong peer id",
			hello:   mustSSHStreamHello(t, auth, SSHStreamPurposeSyncStream, "m4", "other", now, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
			wantErr: true,
		},
		{
			name:    "wrong purpose",
			hello:   mustSSHStreamHello(t, auth, "probe", "m4", "linux-b", now, "cccccccccccccccccccccccccccccccc"),
			wantErr: true,
		},
		{
			name:    "expected identity",
			hello:   mustSSHStreamHello(t, auth, SSHStreamPurposeSyncStream, "m4", "linux-b", now, "dddddddddddddddddddddddddddddddd"),
			wantErr: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(helloFrame(tc.hello))
			if err != nil {
				t.Fatal(err)
			}
			stream := NewSSHSyncStream(auth, "linux-b", "m4", bytes.NewReader(append(raw, '\n')), io.Discard)
			stream.SetHelloNonceCache(NewSSHStreamHelloNonceCache())

			_, err = stream.ReadHello(context.Background(), now)
			if tc.wantErr && !errors.Is(err, ErrSSHStreamInvalidHello) {
				t.Fatalf("ReadHello error = %v, want ErrSSHStreamInvalidHello", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ReadHello error = %v", err)
			}
		})
	}
}

func TestSSHStreamHelloRejectsUnsupportedProtocolList(t *testing.T) {
	auth := testAuth(t)
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	hello := SSHStreamHello{
		Protocols: []int{2},
		Purpose:   SSHStreamPurposeSyncStream,
		HostID:    "m4",
		PeerID:    "linux-b",
		TS:        now.Unix(),
		Nonce:     "ffffffffffffffffffffffffffffffff",
	}
	sig, err := signSSHStreamHello(auth, hello)
	if err != nil {
		t.Fatal(err)
	}
	hello.Sig = sig
	raw, err := json.Marshal(helloFrame(hello))
	if err != nil {
		t.Fatal(err)
	}
	stream := NewSSHSyncStream(auth, "linux-b", "m4", bytes.NewReader(append(raw, '\n')), io.Discard)
	stream.SetHelloNonceCache(NewSSHStreamHelloNonceCache())

	_, err = stream.ReadHello(context.Background(), now)
	if !errors.Is(err, ErrSSHStreamInvalidHello) {
		t.Fatalf("unsupported protocol error = %v, want ErrSSHStreamInvalidHello", err)
	}
}

func TestSSHStreamHelloRejectsReplayedNonceAcrossStreams(t *testing.T) {
	auth := testAuth(t)
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	hello := mustSSHStreamHello(t, auth, SSHStreamPurposeSyncStream, "m4", "linux-b", now, "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
	raw, err := json.Marshal(helloFrame(hello))
	if err != nil {
		t.Fatal(err)
	}
	cache := NewSSHStreamHelloNonceCache()

	first := NewSSHSyncStream(auth, "linux-b", "m4", bytes.NewReader(append(raw, '\n')), io.Discard)
	first.SetHelloNonceCache(cache)
	if _, err := first.ReadHello(context.Background(), now); err != nil {
		t.Fatalf("first ReadHello error = %v", err)
	}

	second := NewSSHSyncStream(auth, "linux-b", "m4", bytes.NewReader(append(raw, '\n')), io.Discard)
	second.SetHelloNonceCache(cache)
	_, err = second.ReadHello(context.Background(), now.Add(time.Second))
	if !errors.Is(err, ErrSSHStreamInvalidHello) {
		t.Fatalf("replayed hello error = %v, want ErrSSHStreamInvalidHello", err)
	}
}

func TestSSHStreamHelloRejectsFutureEdgeReplayWithinValidityWindow(t *testing.T) {
	auth := testAuth(t)
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	hello := mustSSHStreamHello(t, auth, SSHStreamPurposeSyncStream, "m4", "linux-b", now.Add(signatureSkew), "abababababababababababababababab")
	raw, err := json.Marshal(helloFrame(hello))
	if err != nil {
		t.Fatal(err)
	}
	cache := NewSSHStreamHelloNonceCache()

	first := NewSSHSyncStream(auth, "linux-b", "m4", bytes.NewReader(append(raw, '\n')), io.Discard)
	first.SetHelloNonceCache(cache)
	if _, err := first.ReadHello(context.Background(), now); err != nil {
		t.Fatalf("first ReadHello error = %v", err)
	}

	second := NewSSHSyncStream(auth, "linux-b", "m4", bytes.NewReader(append(raw, '\n')), io.Discard)
	second.SetHelloNonceCache(cache)
	_, err = second.ReadHello(context.Background(), now.Add(nonceRetention))
	if !errors.Is(err, ErrSSHStreamInvalidHello) {
		t.Fatalf("future-edge replay error = %v, want ErrSSHStreamInvalidHello", err)
	}
}

func TestSSHStreamHelloNonceCacheEvictsOldestBucketEntries(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	cache := newSSHStreamHelloNonceCacheWithCaps(2, 10)
	bucket := sshStreamHelloNonceBucket("m4", SSHStreamPurposeSyncStream)

	for i, nonce := range []string{"nonce-a", "nonce-b", "nonce-c"} {
		if !cache.remember("m4", SSHStreamPurposeSyncStream, nonce, now.Add(time.Duration(i)*time.Second)) {
			t.Fatalf("remember(%q) rejected", nonce)
		}
	}
	if got := len(cache.seen); got != 2 {
		t.Fatalf("cache size = %d, want 2", got)
	}
	if _, ok := cache.seen[sshStreamHelloNonceKey(bucket, "nonce-a")]; ok {
		t.Fatal("oldest bucket nonce was not evicted")
	}
	if _, ok := cache.seen[sshStreamHelloNonceKey(bucket, "nonce-b")]; !ok {
		t.Fatal("newer bucket nonce-b missing")
	}
	if _, ok := cache.seen[sshStreamHelloNonceKey(bucket, "nonce-c")]; !ok {
		t.Fatal("newer bucket nonce-c missing")
	}
}

func TestSSHStreamHelloNonceCacheEvictsOldestProcessEntries(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	cache := newSSHStreamHelloNonceCacheWithCaps(10, 3)

	for i, entry := range []struct {
		host  string
		nonce string
	}{
		{host: "m4", nonce: "nonce-a"},
		{host: "linux-b", nonce: "nonce-b"},
		{host: "linux-c", nonce: "nonce-c"},
		{host: "linux-d", nonce: "nonce-d"},
	} {
		if !cache.remember(entry.host, SSHStreamPurposeSyncStream, entry.nonce, now.Add(time.Duration(i)*time.Second)) {
			t.Fatalf("remember(%s,%s) rejected", entry.host, entry.nonce)
		}
	}
	if got := len(cache.seen); got != 3 {
		t.Fatalf("cache size = %d, want 3", got)
	}
	if _, ok := cache.seen[sshStreamHelloNonceKey(sshStreamHelloNonceBucket("m4", SSHStreamPurposeSyncStream), "nonce-a")]; ok {
		t.Fatal("oldest process nonce was not evicted")
	}
	if _, ok := cache.seen[sshStreamHelloNonceKey(sshStreamHelloNonceBucket("linux-d", SSHStreamPurposeSyncStream), "nonce-d")]; !ok {
		t.Fatal("newest process nonce missing")
	}
}

func TestSSHStreamHelloRejectsBadSignatureAndReplayWindow(t *testing.T) {
	auth := testAuth(t)
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	hello, err := NewSSHStreamHello(auth, SSHStreamPurposeSyncStream, "m4", "linux-b", now, "1234567890abcdef1234567890abcdef")
	if err != nil {
		t.Fatalf("NewSSHStreamHello error = %v", err)
	}

	badSig := hello
	badSig.Sig = strings.Repeat("0", 64)
	var buf bytes.Buffer
	stream := NewSSHSyncStream(auth, "m4", "linux-b", &buf, io.Discard)
	err = stream.WriteHello(context.Background(), badSig)
	if !errors.Is(err, ErrSSHStreamInvalidHello) {
		t.Fatalf("bad signature error = %v, want ErrSSHStreamInvalidHello", err)
	}

	stale := helloFrame(hello)
	raw, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	stream = NewSSHSyncStream(auth, "linux-b", "m4", bytes.NewReader(append(raw, '\n')), io.Discard)
	_, err = stream.ReadHello(context.Background(), now.Add(10*time.Minute))
	if !errors.Is(err, ErrSSHStreamInvalidHello) {
		t.Fatalf("stale hello error = %v, want ErrSSHStreamInvalidHello", err)
	}
}

func TestSSHStreamRejectsUnknownAndOversizedFrames(t *testing.T) {
	auth := testAuth(t)
	stream := NewSSHSyncStream(auth, "linux-b", "m4", bytes.NewBufferString(`{"type":"surprise"}`+"\n"), io.Discard)
	_, _, err := stream.ReadState(context.Background(), time.Now())
	if !errors.Is(err, ErrSSHStreamUnexpectedFrame) {
		t.Fatalf("unknown frame error = %v, want ErrSSHStreamUnexpectedFrame", err)
	}

	stream = NewSSHSyncStream(auth, "m4", "linux-b", bytes.NewReader([]byte("123456\n")), io.Discard)
	_, err = stream.readFrameLine(context.Background(), 5)
	if !errors.Is(err, ErrSSHStreamFrameTooLarge) {
		t.Fatalf("oversized frame error = %v, want ErrSSHStreamFrameTooLarge", err)
	}
}

func TestSSHStreamReadHelloReturnsOnContextCancellation(t *testing.T) {
	auth := testAuth(t)
	input := newBlockingReadCloser()
	stream := NewSSHSyncStream(auth, "linux-b", "m4", input, io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	go func() {
		_, err := stream.ReadHello(ctx, time.Now())
		errCh <- err
	}()
	input.waitForRead(t)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ReadHello error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ReadHello did not return after context cancellation")
	}
	if !input.closedState() {
		t.Fatal("blocking reader was not closed on context cancellation")
	}
}

func TestSSHStreamRejectsUnknownAndDuplicateTopLevelFields(t *testing.T) {
	auth := testAuth(t)
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{name: "unknown field", raw: `{"type":"state","seq":1,"sender":"m4","null_reason":"no_visible_current","extra":true}`},
		{name: "duplicate field", raw: `{"type":"state","type":"state","seq":1,"sender":"m4","null_reason":"no_visible_current"}`},
		{name: "missing clip", raw: `{"type":"state","seq":1,"sender":"m4","null_reason":"no_visible_current"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := NewSSHSyncStream(auth, "linux-b", "m4", bytes.NewReader([]byte(tc.raw+"\n")), io.Discard)

			_, _, err := stream.ReadState(context.Background(), time.Now())
			if !errors.Is(err, ErrSSHStreamUnexpectedFrame) {
				t.Fatalf("ReadState error = %v, want ErrSSHStreamUnexpectedFrame", err)
			}
		})
	}
}

func TestSSHStreamAcceptsExactMaximumPayloadPlusDelimiter(t *testing.T) {
	auth := testAuth(t)
	stream := NewSSHSyncStream(auth, "m4", "linux-b", bytes.NewReader([]byte("12345\n")), io.Discard)

	line, err := stream.readFrameLine(context.Background(), 5)
	if err != nil {
		t.Fatalf("readFrameLine error = %v", err)
	}
	if string(line) != "12345\n" {
		t.Fatalf("line = %q", line)
	}
}

func TestSSHStreamStateRejectsWrongRecipient(t *testing.T) {
	auth := testAuth(t)
	ts := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	content := clipboard.New(clipboard.KindText, []byte("hello"), ts)
	content.ID = "clip-ssh-1"
	env, err := BuildEnvelope(auth, content, "m4", "other")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(SSHStreamFrame{Type: SSHStreamFrameState, Seq: 1, Sender: "m4", Clip: &env})
	if err != nil {
		t.Fatal(err)
	}
	stream := NewSSHSyncStream(auth, "linux-b", "m4", bytes.NewReader(append(raw, '\n')), io.Discard)

	_, _, err = stream.ReadState(context.Background(), ts)
	if !errors.Is(err, ErrWrongRecipient) {
		t.Fatalf("wrong recipient error = %v, want ErrWrongRecipient", err)
	}
}

func TestSSHStreamStateRejectsUnexpectedSender(t *testing.T) {
	auth := testAuth(t)
	raw := []byte(`{"type":"state","seq":1,"sender":"unknown","null_reason":"no_visible_current"}`)
	stream := NewSSHSyncStream(auth, "linux-b", "m4", bytes.NewReader(append(raw, '\n')), io.Discard)

	_, _, err := stream.ReadState(context.Background(), time.Now())
	if !errors.Is(err, ErrSSHStreamSenderMismatch) {
		t.Fatalf("sender mismatch error = %v, want ErrSSHStreamSenderMismatch", err)
	}
}

func mustSSHStreamHello(t *testing.T, auth *Auth, purpose string, hostID string, peerID string, now time.Time, nonce string) SSHStreamHello {
	t.Helper()
	hello, err := NewSSHStreamHello(auth, purpose, hostID, peerID, now, nonce)
	if err != nil {
		t.Fatalf("NewSSHStreamHello error = %v", err)
	}
	return hello
}

type blockingReadCloser struct {
	closed    chan struct{}
	started   chan struct{}
	startOnce sync.Once
	once      sync.Once
}

func newBlockingReadCloser() *blockingReadCloser {
	return &blockingReadCloser{
		closed:  make(chan struct{}),
		started: make(chan struct{}),
	}
}

func (r *blockingReadCloser) Read([]byte) (int, error) {
	r.startOnce.Do(func() { close(r.started) })
	<-r.closed
	return 0, io.ErrClosedPipe
}

func (r *blockingReadCloser) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

func (r *blockingReadCloser) closedState() bool {
	select {
	case <-r.closed:
		return true
	default:
		return false
	}
}

func (r *blockingReadCloser) waitForRead(t *testing.T) {
	t.Helper()
	select {
	case <-r.started:
	case <-time.After(time.Second):
		t.Fatal("blocking reader was not read")
	}
}
