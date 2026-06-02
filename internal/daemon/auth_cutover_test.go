package daemon

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
)

func TestConfigV2DaemonRequiresHKDFLocalSignedEndpoints(t *testing.T) {
	sharedKey := config.NewSharedKey()
	version := 2
	d, err := NewWithOptions(&config.Config{
		ConfigVersion: &version,
		Listen:        "127.0.0.1:7853",
		Port:          7853,
		SharedKey:     sharedKey,
		Discovery:     "static",
	}, Options{ListenerBoundaryEnabled: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}

	rawRec := serveSignedDaemonRequest(t, d, http.MethodGet, "/v1/peers", "config-v2-raw-peers", nil, "")
	if rawRec.Code != http.StatusUnauthorized || !strings.Contains(rawRec.Body.String(), "auth_version_mismatch") {
		t.Fatalf("config-v2 raw peers = %d %q, want auth_version_mismatch", rawRec.Code, rawRec.Body.String())
	}

	hkdfRec := serveSignedDaemonRequest(t, d, http.MethodGet, "/v1/peers", "config-v2-hkdf-peers", nil, transport.AuthVersionRequestHMAC)
	if hkdfRec.Code != http.StatusOK {
		t.Fatalf("config-v2 hkdf peers = %d %q, want 200", hkdfRec.Code, hkdfRec.Body.String())
	}
	if got := hkdfRec.Header().Get(transport.HeaderAuthVersion); got != transport.AuthVersionRequestHMAC {
		t.Fatalf("response auth version = %q, want %q", got, transport.AuthVersionRequestHMAC)
	}
}

func TestPreV2DaemonKeepsRawLocalSignedCompatibility(t *testing.T) {
	sharedKey := config.NewSharedKey()
	d, err := NewWithOptions(&config.Config{
		Listen:    "127.0.0.1:7853",
		Port:      7853,
		SharedKey: sharedKey,
		Discovery: "static",
	}, Options{ListenerBoundaryEnabled: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}

	rec := serveSignedDaemonRequest(t, d, http.MethodGet, "/v1/peers", "pre-v2-raw-peers", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("pre-v2 raw peers = %d %q, want 200", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get(transport.HeaderAuthVersion); got != "" {
		t.Fatalf("pre-v2 raw response auth version = %q, want empty", got)
	}
}

func serveSignedDaemonRequest(t *testing.T, d *Daemon, method, target, nonce string, body []byte, authVersion string) *httptest.ResponseRecorder {
	t.Helper()
	headers, err := d.auth.SignedRequestHeaders(method, target, body, transport.SignedRequestOptions{
		Timestamp:   time.Now(),
		Nonce:       nonce,
		AuthVersion: authVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	d.sv.Handler().ServeHTTP(rec, req)
	return rec
}
