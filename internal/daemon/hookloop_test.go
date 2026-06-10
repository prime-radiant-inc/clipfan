package daemon

import (
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/store"
)

// headlessFake models a host with no usable clipboard backend (no display):
// reads return nothing and writes are no-ops. This is the case where the
// OS-clipboard readback in onReceive cannot register the written-back path, so
// the explicit textPayload registration is the only thing preventing a loop
// when clipfan copy re-submits the buffer content.
type headlessFake struct{}

func (headlessFake) Read() (clipboard.Content, error) { return clipboard.Content{}, nil }
func (headlessFake) WriteText([]byte) error           { return nil }
func (headlessFake) WriteImage([]byte, string) error  { return nil }

// TestImagePathWritebackDedupedHeadless covers a re-submit loop: on a headless
// host, receiving an image loads its on-disk PATH into the tmux buffer. If that
// path is re-submitted as a text clip, its hash differs from the image's hash,
// so it must be registered explicitly when loaded or it would be re-broadcast.
// With a headless backend the OS-clipboard readback is empty, so only the
// explicit registration guards the loop.
func TestImagePathWritebackDedupedHeadless(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	sshRuntime := &fakeSSHSyncRuntime{}
	d := &Daemon{
		cb:         headlessFake{},
		origin:     "self",
		sshSync:    sshRuntime,
		peerStatus: map[string]*PeerState{},
		seen:       newSeenSet(),
	}

	img := clipboard.New(clipboard.KindImage, []byte("\x89PNG\r\n\x1a\nDATA"), fixedTime)
	img.ID = "img-1"
	d.onReceive(img, "m4")
	waitForPublishes(t, sshRuntime, 1)

	entries, err := store.LoadHistory(10)
	if err != nil || len(entries) == 0 || entries[0].ImagePath == "" {
		t.Fatalf("expected an image history entry with a path; got %+v err=%v", entries, err)
	}
	path := entries[0].ImagePath

	before := len(sshPublishSnapshot(sshRuntime))
	// Exactly what a buffer re-submit would include: the loaded path text, stamped
	// with a fresh ID (clipfan copy mints one). The IsImageStorePath guard — not
	// ID dedup — must drop it.
	pathClip := clipboard.New(clipboard.KindText, []byte(path), fixedTime.Add(time.Second))
	pathClip.ID = "hook-resubmit"
	d.onReceive(pathClip, "self")
	time.Sleep(50 * time.Millisecond)

	if after := len(sshPublishSnapshot(sshRuntime)); after != before {
		t.Fatalf("image-path writeback re-broadcast %d clip(s); expected dedup (echo loop)", after-before)
	}
}
