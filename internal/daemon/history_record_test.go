package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/store"
)

func TestOnReceiveRecordsHistory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, _, _ := newTestDaemon(t)

	c := clipboard.New(clipboard.KindText, []byte("hello-history"), fixedTime)
	c.ID = "hist-1"
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
	c.ID = "secret-1"
	c.Concealed = true
	d.onReceive(c, "m4")

	got, _ := store.LoadHistory(10)
	if len(got) != 0 {
		t.Fatalf("concealed clip must not be recorded, got %+v", got)
	}
}

func TestPollOnceDoesNotFanoutConcealedClip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, cb, push := newTestDaemon(t)

	c := clipboard.New(clipboard.KindText, []byte("password"), fixedTime)
	c.Concealed = true
	cb.current = c

	d.pollOnce(context.Background())
	time.Sleep(50 * time.Millisecond)

	if !d.isEcho(c) {
		t.Fatal("concealed local clip was not adopted for echo suppression")
	}
	if got := len(sshPublishSnapshot(push)); got != 0 {
		t.Fatalf("concealed local clip publish count = %d, want 0", got)
	}
	got, err := store.LoadHistory(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("concealed local clip recorded history: %+v", got)
	}
}

func TestOnReceiveDropsConcealedClip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, cb, push := newTestDaemon(t)

	c := clipboard.New(clipboard.KindText, []byte("password"), fixedTime)
	c.ID = "secret-peer"
	c.Concealed = true
	d.onReceive(c, "peer")
	time.Sleep(50 * time.Millisecond)

	got, err := store.LoadHistory(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("concealed peer clip recorded history: %+v", got)
	}
	state, err := store.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Kind != "" {
		t.Fatalf("concealed peer clip persisted state: %+v", state)
	}
	text, err := store.LoadText()
	if err != nil {
		t.Fatal(err)
	}
	if len(text) != 0 {
		t.Fatalf("concealed peer clip persisted text: %q", text)
	}
	cur, _ := cb.Read()
	if string(cur.Bytes) == "password" {
		t.Fatal("concealed peer clip should not be written to local clipboard")
	}
	if _, ok := d.peerStatus["peer"]; ok {
		t.Fatal("concealed peer clip should not record receive status")
	}
	if got := len(sshPublishSnapshot(push)); got != 0 {
		t.Fatalf("concealed peer clip relayed %d times, want 0", got)
	}
}

func TestOnReceiveConcealedFutureTimestampDoesNotSuppressVisibleClip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, cb, push := newTestDaemon(t)

	secret := clipboard.New(clipboard.KindText, []byte("password"), fixedTime.Add(time.Hour))
	secret.ID = "secret-future"
	secret.Concealed = true
	d.onReceive(secret, "peer")

	visible := clipboard.New(clipboard.KindText, []byte("visible"), fixedTime)
	visible.ID = "visible-earlier"
	d.onReceive(visible, "peer")
	waitForPublishes(t, push, 1)

	got, err := store.LoadHistory(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Preview != "visible" {
		t.Fatalf("visible clip after concealed future timestamp not recorded: %+v", got)
	}
	cur, _ := cb.Read()
	if string(cur.Bytes) != "visible" {
		t.Fatalf("clipboard = %q, want visible", cur.Bytes)
	}
	if got := len(sshPublishSnapshot(push)); got != 1 {
		t.Fatalf("visible clip relay count = %d, want 1", got)
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

func TestRestoreWritesClipboardAndPublishes(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, cb, push := newTestDaemon(t)

	// Seed a text entry into history via onReceive.
	c := clipboard.New(clipboard.KindText, []byte("restore-me"), fixedTime)
	c.ID = "restore-1"
	d.onReceive(c, "m4")
	waitForPublishes(t, push, 1) // the relay from onReceive

	got, _ := store.LoadHistory(10)
	if len(got) == 0 {
		t.Fatal("setup: no history entry")
	}
	id := got[0].ID

	before := len(sshPublishSnapshot(push))
	if err := d.Restore(id); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	waitForPublishes(t, push, before+1)

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
