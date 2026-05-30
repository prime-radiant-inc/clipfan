package daemon

import (
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/discovery"
	"github.com/prime-radiant-inc/clipfan/internal/store"
)

// headlessFake models a host with no usable clipboard backend (no display):
// reads return nothing and writes are no-ops. This is the case where the
// OS-clipboard readback in onReceive cannot register the written-back path, so
// the explicit textPayload registration is the only thing preventing a loop
// when a tmux after-load-buffer hook re-submits the buffer content.
type headlessFake struct{}

func (headlessFake) Read() (clipboard.Content, error) { return clipboard.Content{}, nil }
func (headlessFake) WriteText([]byte) error           { return nil }
func (headlessFake) WriteImage([]byte, string) error  { return nil }

// TestImagePathWritebackDedupedHeadless reproduces the hook-bridge loop: on a
// headless host, receiving an image loads its on-disk PATH into the tmux buffer.
// A tmux after-load-buffer hook re-submits that path as a text clip. Because the
// path's hash differs from the image's hash, it must be registered explicitly
// when loaded, or it would be re-broadcast — an echo loop. With a headless
// backend the OS-clipboard readback is empty, so only the explicit registration
// guards the loop.
func TestImagePathWritebackDedupedHeadless(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	push := &fakePusher{}
	d := &Daemon{
		cb:         headlessFake{},
		disc:       discovery.NewStatic([]string{"peer-host"}, 9999),
		cl:         push,
		origin:     "self",
		peerStatus: map[string]*PeerState{},
		seen:       newSeenSet(),
	}

	img := clipboard.New(clipboard.KindImage, []byte("\x89PNG\r\n\x1a\nDATA"), fixedTime)
	img.ID = "img-1"
	d.onReceive(img, "m4")
	waitForPushes(t, push, 1) // the image relays to the static peer

	entries, err := store.LoadHistory(10)
	if err != nil || len(entries) == 0 || entries[0].ImagePath == "" {
		t.Fatalf("expected an image history entry with a path; got %+v err=%v", entries, err)
	}
	path := entries[0].ImagePath

	before := len(push.snapshot())
	// Exactly what an after-load-buffer hook would submit: the loaded path text,
	// stamped with a fresh ID (clipfan copy mints one). The IsImageStorePath
	// guard — not ID dedup — must drop it.
	pathClip := clipboard.New(clipboard.KindText, []byte(path), fixedTime.Add(time.Second))
	pathClip.ID = "hook-resubmit"
	d.onReceive(pathClip, "self")
	time.Sleep(50 * time.Millisecond)

	if after := len(push.snapshot()); after != before {
		t.Fatalf("image-path writeback re-broadcast %d clip(s); expected dedup (echo loop)", after-before)
	}
}
