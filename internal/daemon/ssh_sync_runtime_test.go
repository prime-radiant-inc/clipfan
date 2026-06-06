package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/config"
)

func TestSSHTransportPollOncePublishesToSSHRuntimeNotHTTP(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d := newSSHTransportDaemonForTest(t)
	d.origin = "self"
	d.cb = &fakeBackend{current: clipboard.New(clipboard.KindText, []byte("local-copy"), fixedTime)}
	discoveryProbe := &countingDiscoverer{}
	pushProbe := &fakePusher{}
	sshRuntime := &fakeSSHSyncRuntime{}
	d.disc = discoveryProbe
	d.cl = pushProbe
	d.sshSync = sshRuntime

	d.pollOnce(context.Background())

	if got := discoveryProbe.count(); got != 0 {
		t.Fatalf("transport:ssh poll called discovery %d times", got)
	}
	if got := len(pushProbe.snapshot()); got != 0 {
		t.Fatalf("transport:ssh poll pushed HTTP %d times", got)
	}
	calls := sshRuntime.waitForPublishes(t, 1)
	if calls[0].origin != "self" || calls[0].skipOrigin != "" || calls[0].content.ID == "" || string(calls[0].content.Bytes) != "local-copy" {
		t.Fatalf("ssh publish = %#v", calls[0])
	}
	current := d.currentHandler()
	got, ok, err := current.Content()
	if err != nil {
		t.Fatal(err)
	}
	if !ok || current.Origin != "self" || got.ID != calls[0].content.ID || string(got.Bytes) != "local-copy" {
		t.Fatalf("current = %#v content=%#v ok=%v", current, got, ok)
	}
}

func TestSSHTransportConcealedLocalClipIsNotExposedAsCurrent(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d := newSSHTransportDaemonForTest(t)
	d.origin = "self"
	secret := clipboard.New(clipboard.KindText, []byte("secret"), fixedTime)
	secret.Concealed = true
	d.cb = &fakeBackend{current: secret}

	d.pollOnce(context.Background())

	current := d.currentHandler()
	if current.HasCurrent || current.NullReason != "no_visible_current" {
		t.Fatalf("current = %#v, want no visible current", current)
	}
}

func TestSSHTransportOnReceiveRelaysToSSHRuntimeNotHTTP(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d := newSSHTransportDaemonForTest(t)
	d.origin = "self"
	d.cb = &fakeBackend{}
	discoveryProbe := &countingDiscoverer{}
	pushProbe := &fakePusher{}
	sshRuntime := &fakeSSHSyncRuntime{}
	d.disc = discoveryProbe
	d.cl = pushProbe
	d.sshSync = sshRuntime

	c := clipboard.New(clipboard.KindText, []byte("peer-copy"), fixedTime)
	c.ID = "ssh-runtime-peer-copy"
	d.onReceive(c, "peer")

	if got := discoveryProbe.count(); got != 0 {
		t.Fatalf("transport:ssh receive called discovery %d times", got)
	}
	if got := len(pushProbe.snapshot()); got != 0 {
		t.Fatalf("transport:ssh receive relayed HTTP %d times", got)
	}
	calls := sshRuntime.waitForPublishes(t, 1)
	if calls[0].origin != "peer" || calls[0].skipOrigin != "peer" || calls[0].content.ID != "ssh-runtime-peer-copy" {
		t.Fatalf("ssh relay publish = %#v", calls[0])
	}
}

type sshSyncPublishCall struct {
	content    clipboard.Content
	origin     string
	skipOrigin string
}

type fakeSSHSyncRuntime struct {
	mu        sync.Mutex
	started   int
	refreshed int
	calls     []sshSyncPublishCall
}

func (r *fakeSSHSyncRuntime) Start(context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started++
}

func (r *fakeSSHSyncRuntime) Refresh(context.Context, *config.Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshed++
}

func (r *fakeSSHSyncRuntime) Publish(_ context.Context, content clipboard.Content, origin string, skipOrigin string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, sshSyncPublishCall{content: content, origin: origin, skipOrigin: skipOrigin})
}

func (r *fakeSSHSyncRuntime) waitForPublishes(t *testing.T, n int) []sshSyncPublishCall {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		if len(r.calls) >= n {
			out := append([]sshSyncPublishCall(nil), r.calls...)
			r.mu.Unlock()
			return out
		}
		r.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t.Fatalf("timed out waiting for %d ssh publishes; got %d", n, len(r.calls))
	return nil
}
