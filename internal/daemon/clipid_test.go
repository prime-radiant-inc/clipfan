package daemon

import (
	"context"
	"testing"

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

