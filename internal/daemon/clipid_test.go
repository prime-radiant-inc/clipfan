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
	waitForPublishes(t, push, 1)

	if id := sshPublishSnapshot(push)[0].content.ID; id == "" {
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
	waitForPublishes(t, push, 1)

	if id := sshPublishSnapshot(push)[0].content.ID; id != "origin-assigned-id" {
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
	waitForPublishes(t, push, 1) // the relay
	relayed := len(sshPublishSnapshot(push))

	// Clipboard now reads back the exact image bytes we applied.
	cb.current = clipboard.New(clipboard.KindImage, []byte("PNGDATA"), fixedTime)
	d.pollOnce(context.Background())
	time.Sleep(50 * time.Millisecond)

	if extra := len(sshPublishSnapshot(push)) - relayed; extra != 0 {
		t.Fatalf("pollOnce re-broadcast the image echo: %d extra publishes", extra)
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
	waitForPublishes(t, push, 1)
	relayed := len(sshPublishSnapshot(push))

	cb.current = clipboard.New(clipboard.KindText, []byte("brand new user copy"), fixedTime.Add(time.Second))
	d.pollOnce(context.Background())
	waitForPublishes(t, push, relayed+1)

	got := sshPublishSnapshot(push)
	last := got[len(got)-1]
	if last.content.ID == "" {
		t.Fatal("new copy broadcast with empty ID")
	}
}

// The same clip-ID arriving twice (e.g. via two relay paths) is applied and
// relayed only once.
func TestReceiveDedupsByID(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, _, push := newTestDaemon(t)

	mk := func(body string) clipboard.Content {
		c := clipboard.New(clipboard.KindText, []byte(body), fixedTime)
		c.ID = "same-id"
		return c
	}
	d.onReceive(mk("first bytes"), "peer-a")
	waitForPublishes(t, push, 1)
	d.onReceive(mk("different bytes but same id"), "peer-b")
	time.Sleep(50 * time.Millisecond)

	if got := len(sshPublishSnapshot(push)); got != 1 {
		t.Fatalf("same clip-ID applied/relayed %d times, want 1", got)
	}
}

// An envelope with no clip-ID is dropped, not applied or relayed.
func TestReceiveDropsEmptyID(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, cb, push := newTestDaemon(t)
	cb.current = clipboard.New(clipboard.KindText, []byte("SENTINEL"), fixedTime)

	c := clipboard.New(clipboard.KindText, []byte("no id here"), fixedTime)
	// c.ID intentionally empty
	d.onReceive(c, "peer")
	time.Sleep(50 * time.Millisecond)

	if got := len(sshPublishSnapshot(push)); got != 0 {
		t.Fatalf("ID-less envelope relayed %d times, want 0", got)
	}
	if cb.current.Kind != clipboard.KindText || string(cb.current.Bytes) != "SENTINEL" {
		t.Fatal("ID-less envelope was applied to the clipboard")
	}
}

// Bug A: the same local clipboard content must be broadcast once, not re-sent on
// every poll. pollOnce must adopt the clip it broadcast as the current clip.
func TestPollDoesNotRebroadcastSameLocalClip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, cb, push := newTestDaemon(t)
	cb.current = clipboard.New(clipboard.KindText, []byte("local copy"), fixedTime)

	d.pollOnce(context.Background())
	waitForPublishes(t, push, 1)
	// Same content still on the clipboard; a second poll must NOT re-broadcast.
	d.pollOnce(context.Background())
	time.Sleep(50 * time.Millisecond)

	if got := len(sshPublishSnapshot(push)); got != 1 {
		t.Fatalf("re-broadcast the same local clip: %d publishes, want 1", got)
	}
}

// Bug B: received text re-submitted through clipfan copy with a FRESH id must be
// recognised as an echo of what we just wrote and dropped — not re-applied or
// relayed.
func TestReceiveSuppressesReoriginatedText(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, _, push := newTestDaemon(t)

	first := clipboard.New(clipboard.KindText, []byte("hello from tmux"), fixedTime)
	first.ID = "id-1"
	d.onReceive(first, "peer")
	waitForPublishes(t, push, 1)
	relayed := len(sshPublishSnapshot(push))

	// The re-submission POSTs identical bytes with a new id and a slightly later TS.
	resub := clipboard.New(clipboard.KindText, []byte("hello from tmux"), fixedTime.Add(time.Millisecond))
	resub.ID = "id-2-fresh"
	d.onReceive(resub, "self")
	time.Sleep(50 * time.Millisecond)

	if extra := len(sshPublishSnapshot(push)) - relayed; extra != 0 {
		t.Fatalf("re-originated text was relayed again: %d extra publishes", extra)
	}
}
