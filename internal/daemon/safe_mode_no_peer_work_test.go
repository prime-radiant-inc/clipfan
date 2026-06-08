package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/discovery"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
)

func TestSafeModeStatusAndLogsDoNotStartPeerOrClipboardWork(t *testing.T) {
	cfg := &config.Config{
		Listen:      "0.0.0.0:9000",
		SharedKey:   config.NewSharedKey(),
		Discovery:   "static",
		StaticPeers: []string{"legacy-box"},
		Port:        9000,
	}
	d, err := NewWithOptions(cfg, Options{ListenerBoundaryEnabled: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}
	if !d.listenerPlan.SafeMode {
		t.Fatal("setup did not enter safe mode")
	}
	discoveryProbe := &countingDiscoverer{}
	clipboardProbe := &countingClipboardBackend{}
	sshRuntime := &fakeSSHSyncRuntime{}
	d.disc = discoveryProbe
	d.cb = clipboardProbe
	d.sshSync = sshRuntime
	d.peerStatus["runtime-peer"] = &PeerState{Hostname: "runtime-peer", Port: 9999, LastPushOK: true}
	d.current = currentClip{id: "runtime-current-clip", kind: clipboard.KindText, imagePath: "/tmp/runtime-current-image"}

	cases := []struct {
		target string
		status int
	}{
		{target: "/v1/status", status: http.StatusOK},
		{target: "/v1/peers", status: http.StatusOK},
		{target: "/v1/ssh/logs", status: http.StatusOK},
		{target: "/v1/ssh/logs?peer=requested-peer", status: http.StatusServiceUnavailable},
	}
	for i, tc := range cases {
		req := signedDaemonSafeModeRequest(t, d.auth, http.MethodGet, tc.target, fmt.Sprintf("no-peer-work-%d", i))
		rec := httptest.NewRecorder()
		d.sv.Handler().ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("%s status = %d body=%q, want %d", tc.target, rec.Code, rec.Body.String(), tc.status)
		}
		assertSafeModeBodyExcludesRuntimeState(t, rec.Body.String())
		if tc.target == "/v1/peers" {
			var payload struct {
				Peers []struct {
					Hostname string `json:"hostname"`
				} `json:"peers"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode peers payload: %v", err)
			}
			for _, peer := range payload.Peers {
				if peer.Hostname == "runtime-peer" {
					t.Fatalf("safe-mode peers exposed runtime peer state: %#v", payload.Peers)
				}
			}
		}
	}
	if got := discoveryProbe.count(); got != 0 {
		t.Fatalf("safe-mode status/logs called discovery %d times", got)
	}
	if got := clipboardProbe.count(); got != 0 {
		t.Fatalf("safe-mode status/logs called clipboard backend %d times", got)
	}
	if got := len(sshPublishSnapshot(sshRuntime)); got != 0 {
		t.Fatalf("safe-mode status/logs started publish work %d times", got)
	}
}

func assertSafeModeBodyExcludesRuntimeState(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{
		"runtime-peer",
		"runtime-current-clip",
		"runtime-current-image",
		"last_push_ts",
		"last_recv_ts",
		"transport_health",
		"ssh.peers",
		"current_watch",
		"gateway",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("safe-mode body exposed forbidden runtime marker %q: %s", forbidden, body)
		}
	}
}

type countingDiscoverer struct {
	mu    sync.Mutex
	calls int
}

func (d *countingDiscoverer) Peers(context.Context) ([]discovery.Peer, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	return nil, fmt.Errorf("unexpected discovery call")
}

func (d *countingDiscoverer) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

type countingClipboardBackend struct {
	mu    sync.Mutex
	calls int
}

func (b *countingClipboardBackend) Read() (clipboard.Content, error) {
	b.record()
	return clipboard.Content{}, fmt.Errorf("unexpected clipboard read")
}

func (b *countingClipboardBackend) WriteText([]byte) error {
	b.record()
	return fmt.Errorf("unexpected clipboard text write")
}

func (b *countingClipboardBackend) WriteImage([]byte, string) error {
	b.record()
	return fmt.Errorf("unexpected clipboard image write")
}

func (b *countingClipboardBackend) record() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
}

func (b *countingClipboardBackend) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

func signedDaemonSafeModeRequest(t *testing.T, auth *transport.Auth, method, target, nonce string) *http.Request {
	t.Helper()
	headers, err := auth.SignedRequestHeaders(method, target, nil, transport.SignedRequestOptions{
		Timestamp:   time.Now(),
		Nonce:       nonce,
		AuthVersion: transport.AuthVersionRequestHMAC,
	})
	if err != nil {
		t.Fatalf("SignedRequestHeaders: %v", err)
	}
	req := httptest.NewRequest(method, target, strings.NewReader(""))
	req.RemoteAddr = "127.0.0.1:1234"
	for header, value := range headers {
		req.Header.Set(header, value)
	}
	return req
}
