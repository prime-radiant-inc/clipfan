package transport

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

func TestSafeModeHealthRemainsUnsigned(t *testing.T) {
	srv := NewServer(":0", testAuth(t), nil, nil)
	srv.SetSafeMode(true)

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "ok\n" {
		t.Fatalf("health response = %d %q, want 200 ok", rec.Code, rec.Body.String())
	}
}

func TestSafeModeRejectsNormalMutationAndClipboardRoutes(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, func(clipboard.Content, string) {
		t.Fatal("safe mode invoked clip receive handler")
	}, func() any {
		t.Fatal("safe mode invoked peers handler")
		return nil
	})
	setFixedServerTime(srv)
	srv.SetSafeMode(true)
	srv.SetHistory(
		func(int) (any, error) {
			t.Fatal("safe mode invoked history handler")
			return nil, nil
		},
		func(string) error {
			t.Fatal("safe mode invoked restore handler")
			return nil
		},
		func(string, bool) error {
			t.Fatal("safe mode invoked pin handler")
			return nil
		},
		func(string, bool) error {
			t.Fatal("safe mode invoked delete handler")
			return nil
		},
	)
	srv.SetConfigFunc(func(int) error {
		t.Fatal("safe mode invoked config mutation handler")
		return nil
	})

	cases := []struct {
		name   string
		method string
		target string
		body   []byte
	}{
		{name: "clip receive", method: http.MethodPost, target: "/v1/clip", body: []byte(`{}`)},
		{name: "history read", method: http.MethodGet, target: "/v1/history?limit=10"},
		{name: "history delete", method: http.MethodDelete, target: "/v1/history", body: []byte(`{}`)},
		{name: "restore", method: http.MethodPost, target: "/v1/restore", body: []byte(`{"id":"x"}`)},
		{name: "pin", method: http.MethodPost, target: "/v1/history/pin", body: []byte(`{"id":"x","pinned":true}`)},
		{name: "config mutation", method: http.MethodPost, target: "/v1/config", body: []byte(`{"max_history":123}`)},
		{name: "current read", method: http.MethodGet, target: "/v1/current"},
		{name: "current watch", method: http.MethodGet, target: "/v1/current/watch"},
		{name: "receive primitive", method: http.MethodPost, target: "/v1/receive", body: []byte(`{}`)},
		{name: "sync stream", method: http.MethodGet, target: "/v1/sync-stream"},
		{name: "gateway reservation", method: http.MethodPost, target: "/v1/gateway/session", body: []byte(`{}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := signedRequestWithNonce(t, auth, tc.method, tc.target, "safe-"+tc.name, tc.body)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			requireSafeModeError(t, rec, http.StatusConflict, "public_listen_requires_confirmation")
		})
	}
}

func TestSafeModeVersionIsSignedLocalOnly(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil, nil)
	setFixedServerTime(srv)
	srv.SetSafeMode(true)
	srv.SetVersionFunc(func() any { return map[string]string{"version": "v1.2.3"} })

	local := signedSafeModeRequest(t, auth, http.MethodGet, "/v1/version", "safe-version", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, local)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "v1.2.3") {
		t.Fatalf("local version response = %d %q, want signed version", rec.Code, rec.Body.String())
	}
	requireVersionedSignedResponse(t, auth, rec, "safe-version")

	remote := signedSafeModeRequest(t, auth, http.MethodGet, "/v1/version", "remote-safe-version", nil)
	remote.RemoteAddr = "192.0.2.10:1234"
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, remote)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("remote safe-mode version = %d, want 403", rec.Code)
	}
}

func TestSafeModeReservesStatusAndLogFamiliesWithoutNormalPeersFallthrough(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil, func() any {
		t.Fatal("safe mode fell through to normal peers handler")
		return nil
	})
	setFixedServerTime(srv)
	srv.SetSafeMode(true)

	for i, target := range []string{"/v1/status", "/v1/peers", "/v1/ssh/logs?peer=local"} {
		nonce := "safe-status-logs-" + string(rune('a'+i))
		req := signedSafeModeRequest(t, auth, http.MethodGet, target, nonce, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		requireSignedSafeModeError(t, auth, rec, nonce, http.StatusServiceUnavailable, "safe_mode_status_unavailable_before_schema")
	}
}

func TestSafeModeListenerRepairRoutesAreUnavailableUntilRepairMilestone(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil, nil)
	setFixedServerTime(srv)
	srv.SetSafeMode(true)

	for _, tc := range []struct {
		method string
		body   []byte
	}{
		{method: http.MethodGet},
		{method: http.MethodPatch, body: []byte(`{"listen":"127.0.0.1:7853"}`)},
	} {
		req := signedSafeModeRequest(t, auth, tc.method, "/v1/config/listener", "safe-listener-"+tc.method, tc.body)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		requireSignedSafeModeError(t, auth, rec, "safe-listener-"+tc.method, http.StatusServiceUnavailable, "listener_repair_unavailable")
	}
}

func TestSafeModeSignedEndpointsRequireHKDFAuthVersion(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil, nil)
	setFixedServerTime(srv)
	srv.SetSafeMode(true)
	srv.SetVersionFunc(func() any { return map[string]string{"version": "v1.2.3"} })

	for i, target := range []string{"/v1/version", "/v1/status", "/v1/peers", "/v1/ssh/logs", "/v1/config/listener"} {
		req := signedRequestWithNonce(t, auth, http.MethodGet, target, "raw-safe-endpoint-"+string(rune('a'+i)), nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("raw safe-mode %s status = %d body=%q, want 401", target, rec.Code, rec.Body.String())
		}
	}

	cases := []struct {
		name string
		req  *http.Request
	}{
		{
			name: "missing auth version",
			req:  signedRequestWithNonce(t, auth, http.MethodGet, "/v1/status", "missing-auth-version", nil),
		},
		{
			name: "old raw key signature with version header",
			req: func() *http.Request {
				req := signedRequestWithNonce(t, auth, http.MethodGet, "/v1/status", "raw-key-with-version", nil)
				req.Header.Set(HeaderAuthVersion, AuthVersionRequestHMAC)
				return req
			}(),
		},
		{
			name: "malformed signature",
			req: func() *http.Request {
				req := signedSafeModeRequest(t, auth, http.MethodGet, "/v1/status", "malformed-safe-signature", nil)
				req.Header.Set(HeaderSignature, "not-hex")
				return req
			}(),
		},
		{
			name: "stale timestamp",
			req:  signedSafeModeRequestWithTimestamp(t, auth, http.MethodGet, "/v1/status", "1780250000", "stale-safe-signature", nil),
		},
		{
			name: "wrong derived key context",
			req:  signedSafeModeRequestWithWrongContext(t, auth, http.MethodGet, "/v1/status", "wrong-context", nil),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, tc.req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d body=%q, want 401", rec.Code, rec.Body.String())
			}
		})
	}
}

func requireSafeModeError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body=%q", rec.Code, status, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"`+code+`"`) {
		t.Fatalf("body = %q, want code %s", rec.Body.String(), code)
	}
}

func requireSignedSafeModeError(t *testing.T, auth *Auth, rec *httptest.ResponseRecorder, nonce string, status int, code string) {
	t.Helper()
	requireSafeModeError(t, rec, status, code)
	requireVersionedSignedResponse(t, auth, rec, nonce)
}

func requireVersionedSignedResponse(t *testing.T, auth *Auth, rec *httptest.ResponseRecorder, requestNonce string) {
	t.Helper()
	if got := rec.Header().Get(HeaderAuthVersion); got != AuthVersionRequestHMAC {
		t.Fatalf("response auth version = %q, want %q", got, AuthVersionRequestHMAC)
	}
	sig := rec.Header().Get("X-Clipfan-Response-Sig")
	if sig == "" {
		t.Fatal("missing X-Clipfan-Response-Sig")
	}
	if err := auth.VerifyResponseWithAuthVersion(requestNonce, rec.Body.Bytes(), sig, AuthVersionRequestHMAC); err != nil {
		t.Fatalf("versioned response signature: %v", err)
	}
}

func signedSafeModeRequest(t *testing.T, auth *Auth, method, target, nonce string, body []byte) *http.Request {
	t.Helper()
	return signedSafeModeRequestWithTimestamp(t, auth, method, target, "1780257600", nonce, body)
}

func signedSafeModeRequestWithTimestamp(t *testing.T, auth *Auth, method, target, ts, nonce string, body []byte) *http.Request {
	t.Helper()
	return signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, method, target, ts, nonce, body, AuthVersionRequestHMAC)
}

func signedSafeModeRequestWithWrongContext(t *testing.T, auth *Auth, method, target, nonce string, body []byte) *http.Request {
	t.Helper()
	ts := "1780257600"
	req := httptest.NewRequest(method, target, strings.NewReader(string(body)))
	req.RemoteAddr = "127.0.0.1:1234"
	wrongKey, err := DeriveKey(auth.key, sshHelloHMACLabel)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	mac := hmac.New(sha256.New, wrongKey)
	writeCanonicalRequestWithAuthVersion(mac, method, target, ts, nonce, body, AuthVersionRequestHMAC)
	req.Header.Set(HeaderAuthVersion, AuthVersionRequestHMAC)
	req.Header.Set(HeaderTimestamp, ts)
	req.Header.Set(HeaderNonce, nonce)
	req.Header.Set(HeaderSignature, hex.EncodeToString(mac.Sum(nil)))
	return req
}
