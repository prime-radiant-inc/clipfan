package daemon

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/store"
)

func TestSafeModeBindsDerivedLoopbackNotUnsafeConfiguredAddress(t *testing.T) {
	port := freeTCPPort(t)
	d, err := NewWithOptions(&config.Config{
		Listen:    "203.0.113.10:" + port,
		SharedKey: config.NewSharedKey(),
		Discovery: "static",
	}, Options{ListenerBoundaryEnabled: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}
	if !d.listenerPlan.SafeMode {
		t.Fatal("listener plan did not enter safe mode")
	}
	if d.listenerPlan.BindListen != "127.0.0.1:"+port {
		t.Fatalf("BindListen = %q, want derived loopback", d.listenerPlan.BindListen)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	waitForHealth(t, "http://127.0.0.1:"+port+"/v1/health")
	cancel()
	if err := <-errCh; err != nil && !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("Run after cancel = %v", err)
	}
}

func TestSafeModeLoopbackBindConflictFailsClosed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	d, err := NewWithOptions(&config.Config{
		Listen:    net.JoinHostPort("0.0.0.0", strconv.Itoa(port)),
		SharedKey: config.NewSharedKey(),
		Discovery: "static",
	}, Options{ListenerBoundaryEnabled: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = d.Run(ctx)
	if err == nil || !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("Run error = %v, want loopback address-in-use failure", err)
	}
}

func TestSafeModePollOnceDoesNotStartPeerSync(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, cb, push := newTestDaemon(t)
	d.listenerPlan.SafeMode = true
	cb.current = clipboard.New(clipboard.KindText, []byte("local-copy"), fixedTime)

	d.pollOnce(context.Background())
	time.Sleep(50 * time.Millisecond)

	if got := len(push.snapshot()); got != 0 {
		t.Fatalf("safe-mode poll fanout count = %d, want 0", got)
	}
	history, err := store.LoadHistory(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("safe-mode poll recorded history: %+v", history)
	}
}

func TestSafeModeOnReceiveDoesNotApplyOrRelayPeerClip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, cb, push := newTestDaemon(t)
	d.listenerPlan.SafeMode = true
	cb.current = clipboard.New(clipboard.KindText, []byte("sentinel"), fixedTime)
	c := clipboard.New(clipboard.KindText, []byte("peer-copy"), fixedTime)
	c.ID = "peer-copy-1"

	d.onReceive(c, "peer")
	time.Sleep(50 * time.Millisecond)

	if got := len(push.snapshot()); got != 0 {
		t.Fatalf("safe-mode receive relay count = %d, want 0", got)
	}
	cur, _ := cb.Read()
	if string(cur.Bytes) != "sentinel" {
		t.Fatalf("safe-mode receive wrote clipboard = %q, want sentinel", cur.Bytes)
	}
	history, err := store.LoadHistory(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 0 {
		t.Fatalf("safe-mode receive recorded history: %+v", history)
	}
	state, err := store.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Kind != "" {
		t.Fatalf("safe-mode receive persisted state: %+v", state)
	}
	if _, ok := d.peerStatus["peer"]; ok {
		t.Fatal("safe-mode receive recorded peer status")
	}
}

func TestSafeModeRestoreDoesNotFanout(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, _, push := newTestDaemon(t)

	c := clipboard.New(clipboard.KindText, []byte("restore-me"), fixedTime)
	c.ID = "restore-safe-1"
	d.onReceive(c, "peer")
	waitForPushes(t, push, 1)
	history, err := store.LoadHistory(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) == 0 {
		t.Fatal("setup: missing history entry")
	}

	d.listenerPlan.SafeMode = true
	before := len(push.snapshot())
	if err := d.Restore(history[0].ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := len(push.snapshot()); got != before {
		t.Fatalf("safe-mode restore fanout count = %d, want %d", got, before)
	}
}

func waitForHealth(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr == nil && resp.StatusCode == http.StatusOK && string(body) == "ok\n" {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for health at %s", url)
}

func freeTCPPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return strconv.Itoa(port)
}
