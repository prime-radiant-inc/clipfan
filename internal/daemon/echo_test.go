package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
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

// newTestDaemon builds a Daemon wired with a fake backend and SSH runtime.
func newTestDaemon(t *testing.T) (*Daemon, *fakeBackend, *fakeSSHSyncRuntime) {
	t.Helper()
	cb := &fakeBackend{}
	sshRuntime := &fakeSSHSyncRuntime{}
	d := &Daemon{
		cb:         cb,
		origin:     "self",
		sshSync:    sshRuntime,
		peerStatus: map[string]*PeerState{},
		seen:       newSeenSet(),
	}
	return d, cb, sshRuntime
}

func sshPublishSnapshot(r *fakeSSHSyncRuntime) []sshSyncPublishCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]sshSyncPublishCall(nil), r.calls...)
}

func waitForPublishes(t *testing.T, r *fakeSSHSyncRuntime, n int) {
	t.Helper()
	r.waitForPublishes(t, n)
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
	img.ID = "img-1"
	d.onReceive(img, "some-origin")
	waitForPublishes(t, push, 1)

	relay := sshPublishSnapshot(push)
	if len(relay) != 1 {
		t.Fatalf("expected 1 relay publish during onReceive, got %d", len(relay))
	}
	if relay[0].content.Kind != clipboard.KindImage {
		t.Fatalf("expected relayed image, got kind %v", relay[0].content.Kind)
	}

	d.pollOnce(ctx)
	// Give any erroneous echo publish work a chance to record.
	time.Sleep(50 * time.Millisecond)

	after := sshPublishSnapshot(push)
	for _, c := range after[len(relay):] {
		if c.content.Kind == clipboard.KindText {
			t.Fatalf("pollOnce echoed the path as text; got publish kind=%v hash=%x", c.content.Kind, c.content.Hash)
		}
	}
	if len(after) != len(relay) {
		t.Fatalf("pollOnce broadcast %d extra publishes; expected 0 (echo suppressed)", len(after)-len(relay))
	}
}

// TestRelayDedup guards against a broadcast storm: receiving the same image hash
// twice must relay only once.
func TestRelayDedup(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, _, push := newTestDaemon(t)

	img := clipboard.New(clipboard.KindImage, []byte("PNGDATA"), fixedTime)
	img.ID = "img-1"
	d.onReceive(img, "some-origin")
	waitForPublishes(t, push, 1)
	d.onReceive(img, "some-origin")
	time.Sleep(50 * time.Millisecond)

	if got := len(sshPublishSnapshot(push)); got != 1 {
		t.Fatalf("expected 1 relay publish for duplicate image, got %d", got)
	}
}
