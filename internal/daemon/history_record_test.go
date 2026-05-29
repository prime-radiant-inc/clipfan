package daemon

import (
	"context"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/store"
)

func TestOnReceiveRecordsHistory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, _, _ := newTestDaemon(t)

	c := clipboard.New(clipboard.KindText, []byte("hello-history"), fixedTime)
	d.onReceive(c, "flower-garden")

	got, err := store.LoadHistory(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 history entry, got %d", len(got))
	}
	if got[0].Preview != "hello-history" || got[0].Origin != "flower-garden" {
		t.Fatalf("wrong entry: %+v", got[0])
	}
}

func TestConcealedNotRecorded(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, _, _ := newTestDaemon(t)

	c := clipboard.New(clipboard.KindText, []byte("secret"), fixedTime)
	c.Concealed = true
	d.onReceive(c, "m4")

	got, _ := store.LoadHistory(10)
	if len(got) != 0 {
		t.Fatalf("concealed clip must not be recorded, got %+v", got)
	}
}

func TestPollOnceRecordsHistory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, cb, _ := newTestDaemon(t)

	// Seed the fake local clipboard with text, then poll.
	_ = cb.WriteText([]byte("local-copy"))
	d.pollOnce(context.Background())

	got, err := store.LoadHistory(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Preview != "local-copy" {
		t.Fatalf("pollOnce did not record local copy: %+v", got)
	}
	if got[0].Origin != "self" {
		t.Fatalf("local copy origin = %q, want self", got[0].Origin)
	}
}

func TestRestoreWritesClipboardAndFanouts(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, cb, push := newTestDaemon(t)

	// Seed a text entry into history via onReceive.
	c := clipboard.New(clipboard.KindText, []byte("restore-me"), fixedTime)
	d.onReceive(c, "m4")
	waitForPushes(t, push, 1) // the relay from onReceive

	got, _ := store.LoadHistory(10)
	if len(got) == 0 {
		t.Fatal("setup: no history entry")
	}
	id := got[0].ID

	before := len(push.snapshot())
	if err := d.Restore(id); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	waitForPushes(t, push, before+1) // restore must fanout at least once

	// The local clipboard must now hold the restored text.
	cur, _ := cb.Read()
	if string(cur.Bytes) != "restore-me" {
		t.Fatalf("clipboard = %q, want restore-me", cur.Bytes)
	}
}

func TestRestoreUnknownID(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, _, _ := newTestDaemon(t)
	if err := d.Restore("deadbeef"); err == nil {
		t.Fatal("Restore of unknown id should error")
	}
}
