package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/discovery"
)

var fixedTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fakeBackend simulates the Linux clipboard backend, where WriteImage is
// text-only and stores the on-disk path as text. Read returns whatever was
// last written.
type fakeBackend struct {
	current clipboard.Content
}

func (b *fakeBackend) Read() (clipboard.Content, error) {
	return b.current, nil
}

func (b *fakeBackend) WriteText(body []byte) error {
	b.current = clipboard.New(clipboard.KindText, body, fixedTime)
	return nil
}

func (b *fakeBackend) WriteImage(body []byte, path string) error {
	b.current = clipboard.New(clipboard.KindText, []byte(path), fixedTime)
	return nil
}

// pushCall records a single fanout push.
type pushCall struct {
	host string
	kind clipboard.Kind
	hash [32]byte
}

// fakePusher records every PushAs invocation. fanout pushes concurrently, so
// access is mutex-guarded.
type fakePusher struct {
	mu    sync.Mutex
	calls []pushCall
}

func (p *fakePusher) PushAs(ctx context.Context, host string, port int, content clipboard.Content, origin string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, pushCall{host: host, kind: content.Kind, hash: content.Hash})
	return nil
}

func (p *fakePusher) snapshot() []pushCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]pushCall, len(p.calls))
	copy(out, p.calls)
	return out
}

// newTestDaemon builds a Daemon wired with a fake backend and pusher and a
// single static peer that is not this host, so fanout actually pushes.
func newTestDaemon(t *testing.T) (*Daemon, *fakeBackend, *fakePusher) {
	t.Helper()
	cb := &fakeBackend{}
	push := &fakePusher{}
	d := &Daemon{
		cb:         cb,
		disc:       discovery.NewStatic([]string{"peer-host"}, 9999),
		cl:         push,
		origin:     "self",
		peerStatus: map[string]*PeerState{},
		seen:       newSeenSet(),
	}
	return d, cb, push
}

// waitForPushes spins until the pusher has recorded at least n calls or the
// deadline elapses. fanout launches goroutines, so we cannot read synchronously.
func waitForPushes(t *testing.T, p *fakePusher, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(p.snapshot()) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d pushes; got %d", n, len(p.snapshot()))
}

// TestImageReceiveDoesNotEchoPath reproduces the echo-clobber regression: when a
// host receives an image and its (Linux-style) backend records the on-disk path
// as text, the next poll must NOT re-broadcast that path as new text.
func TestImageReceiveDoesNotEchoPath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx := context.Background()
	d, _, push := newTestDaemon(t)

	img := clipboard.New(clipboard.KindImage, []byte("PNGDATA"), fixedTime)
	d.onReceive(img, "some-origin")
	waitForPushes(t, push, 1)

	relay := push.snapshot()
	if len(relay) != 1 {
		t.Fatalf("expected 1 relay push during onReceive, got %d", len(relay))
	}
	if relay[0].kind != clipboard.KindImage {
		t.Fatalf("expected relayed image, got kind %v", relay[0].kind)
	}

	d.pollOnce(ctx)
	// Give any erroneous echo fanout goroutines a chance to record.
	time.Sleep(50 * time.Millisecond)

	after := push.snapshot()
	for _, c := range after[len(relay):] {
		if c.kind == clipboard.KindText {
			t.Fatalf("pollOnce echoed the path as text; got push kind=%v hash=%x", c.kind, c.hash)
		}
	}
	if len(after) != len(relay) {
		t.Fatalf("pollOnce broadcast %d extra pushes; expected 0 (echo suppressed)", len(after)-len(relay))
	}
}

// TestRelayDedup guards against a broadcast storm: receiving the same image hash
// twice must relay only once.
func TestRelayDedup(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, _, push := newTestDaemon(t)

	img := clipboard.New(clipboard.KindImage, []byte("PNGDATA"), fixedTime)
	d.onReceive(img, "some-origin")
	waitForPushes(t, push, 1)
	d.onReceive(img, "some-origin")
	time.Sleep(50 * time.Millisecond)

	if got := len(push.snapshot()); got != 1 {
		t.Fatalf("expected 1 relay push for duplicate image, got %d", got)
	}
}
