package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

// A genuine local copy detected by pollOnce must be broadcast with a freshly
// minted, non-empty clip-ID.
func TestPollMintsClipID(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, cb, push := newTestDaemon(t)
	cb.current = clipboard.New(clipboard.KindText, []byte("hello world"), fixedTime)

	d.pollOnce(context.Background())
	waitForPushes(t, push, 1)

	if id := push.snapshot()[0].id; id == "" {
		t.Fatal("pollOnce broadcast a clip with an empty ID")
	}
}

// A relayed clip keeps the original sender's ID; the relay must not re-mint.
func TestRelayPreservesClipID(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, _, push := newTestDaemon(t)
	c := clipboard.New(clipboard.KindText, []byte("relay me"), fixedTime)
	c.ID = "origin-assigned-id"

	d.onReceive(c, "some-origin")
	waitForPushes(t, push, 1)

	if id := push.snapshot()[0].id; id != "origin-assigned-id" {
		t.Fatalf("relay changed the clip ID to %q, want origin-assigned-id", id)
	}
}

// After applying a received image, a pollOnce that reads the same image bytes
// back must be recognised as an echo and not broadcast.
func TestPollSuppressesImageBytesEcho(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, cb, push := newTestDaemon(t)

	img := clipboard.New(clipboard.KindImage, []byte("PNGDATA"), fixedTime)
	img.ID = "img-1"
	d.onReceive(img, "peer")
	waitForPushes(t, push, 1) // the relay
	relayed := len(push.snapshot())

	// Clipboard now reads back the exact image bytes we applied.
	cb.current = clipboard.New(clipboard.KindImage, []byte("PNGDATA"), fixedTime)
	d.pollOnce(context.Background())
	time.Sleep(50 * time.Millisecond)

	if extra := len(push.snapshot()) - relayed; extra != 0 {
		t.Fatalf("pollOnce re-broadcast the image echo: %d extra pushes", extra)
	}
}

// A genuinely new local copy after a received clip is NOT an echo: it mints a
// new ID and broadcasts.
func TestPollBroadcastsGenuineNewCopy(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, cb, push := newTestDaemon(t)

	first := clipboard.New(clipboard.KindText, []byte("received text"), fixedTime)
	first.ID = "txt-1"
	d.onReceive(first, "peer")
	waitForPushes(t, push, 1)
	relayed := len(push.snapshot())

	cb.current = clipboard.New(clipboard.KindText, []byte("brand new user copy"), fixedTime.Add(time.Second))
	d.pollOnce(context.Background())
	waitForPushes(t, push, relayed+1)

	got := push.snapshot()
	last := got[len(got)-1]
	if last.id == "" {
		t.Fatal("new copy broadcast with empty ID")
	}
}
