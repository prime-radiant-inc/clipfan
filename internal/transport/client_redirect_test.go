package transport

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
)

// TestSignedLoopbackGetDoesNotFollowRedirects ensures the signed request headers are
// never forwarded across an HTTP redirect: a redirecting responder must not cause the
// client to re-issue the (nonce-bound) signed GET to the redirect target.
func TestSignedLoopbackGetDoesNotFollowRedirects(t *testing.T) {
	auth := testAuth(t)
	var leakHit int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/peers", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/leak", http.StatusFound)
	})
	mux.HandleFunc("/leak", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&leakHit, 1)
		w.WriteHeader(http.StatusOK)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	host, portStr, err := net.SplitHostPort(ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portStr)

	client := NewClientWithPeerHTTPRuntimeDisabled(auth, "x", true)
	if _, err := client.Peers(context.Background(), host, port); err == nil {
		t.Fatal("expected an error (the redirect must not be followed), got nil")
	}
	if atomic.LoadInt32(&leakHit) != 0 {
		t.Fatal("redirect was followed — signed request headers leaked to the redirect target")
	}
}
