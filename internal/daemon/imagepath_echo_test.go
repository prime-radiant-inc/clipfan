package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/store"
)

// TestPollDoesNotBroadcastImageStorePath reproduces the real-world echo-clobber:
// a host's clipboard ends up holding a clipfan image-store path as text (its own
// representation of a received image), and that text is NOT in the seen set — the
// divergence the hash-based echo guard misses (trailing newline / re-read /
// arrival route). pollOnce must recognize the path as a non-broadcastable image
// representation and never fan it out as a text clip.
func TestPollDoesNotBroadcastImageStorePath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx := context.Background()
	d, cb, push := newTestDaemon(t)

	path, err := store.SaveImage([]byte("PNGDATA"))
	if err != nil {
		t.Fatal(err)
	}

	// Clipboard holds the path as text, with a trailing newline so its hash
	// differs from anything the daemon registered — exactly why the hash guard
	// fails in the field.
	cb.current = clipboard.New(clipboard.KindText, []byte(path+"\n"), fixedTime)

	d.pollOnce(ctx)
	time.Sleep(50 * time.Millisecond)

	if got := len(push.snapshot()); got != 0 {
		t.Fatalf("pollOnce broadcast an image-store path as text; expected 0 pushes, got %d", got)
	}
}

// TestReceiveImageStorePathDoesNotClobber covers the other entry point: a peer
// (or our own tmux/clipfan-copy self-inject) delivers an image as a text path via
// onReceive. The daemon must not write that path over the clipboard, and must not
// relay it onward — otherwise it clobbers the real image on every image-capable
// host the path reaches.
func TestReceiveImageStorePathDoesNotClobber(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, cb, push := newTestDaemon(t)

	// The real image is what's currently on our clipboard.
	cb.current = clipboard.New(clipboard.KindImage, []byte("REALIMAGE"), fixedTime)

	path, err := store.SaveImage([]byte("PNGDATA"))
	if err != nil {
		t.Fatal(err)
	}

	// A peer sends us that image represented as a text path.
	d.onReceive(clipboard.New(clipboard.KindText, []byte(path), fixedTime), "peer")
	time.Sleep(50 * time.Millisecond)

	if cb.current.Kind == clipboard.KindText {
		t.Fatalf("onReceive wrote the image-store path over the clipboard")
	}
	if got := len(push.snapshot()); got != 0 {
		t.Fatalf("onReceive relayed an image-store path; expected 0 pushes, got %d", got)
	}
}
