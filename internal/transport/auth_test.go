package transport

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthRoundTrip(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("a-32-byte-test-secret-for-hmac-x"))
	a, err := NewAuth(key)
	if err != nil {
		t.Fatalf("NewAuth: %v", err)
	}
	body := []byte(`{"hello":"world"}`)
	sig := a.SignRequest(http.MethodPost, "/v1/clip", "1780257600", "nonce-1", body)
	if err := a.VerifyRequest(http.MethodPost, "/v1/clip", "1780257600", "nonce-1", body, sig); err != nil {
		t.Fatalf("Verify good sig: %v", err)
	}
	if err := a.VerifyRequest(http.MethodPost, "/v1/clip", "1780257600", "nonce-1", []byte("tampered"), sig); err == nil {
		t.Fatal("expected error on tampered body")
	}
	if err := a.VerifyRequest(http.MethodPost, "/v1/clip", "1780257600", "nonce-1", body, "deadbeef"); err == nil {
		t.Fatal("expected error on wrong sig length")
	}
}

func TestRequestSignatureIsBoundToMethodAndPath(t *testing.T) {
	auth := testAuth(t)
	body := []byte(`{"id":"abc"}`)
	sig := auth.SignRequest(http.MethodPost, "/v1/restore", "1780257600", "nonce-1", body)

	if err := auth.VerifyRequest(http.MethodPost, "/v1/restore", "1780257600", "nonce-1", body, sig); err != nil {
		t.Fatalf("VerifyRequest signed route: %v", err)
	}
	if err := auth.VerifyRequest(http.MethodGet, "/v1/restore", "1780257600", "nonce-1", body, sig); err == nil {
		t.Fatal("expected method change to reject signature")
	}
	if err := auth.VerifyRequest(http.MethodPost, "/v1/config", "1780257600", "nonce-1", body, sig); err == nil {
		t.Fatal("expected path change to reject signature")
	}
}

func TestRequestSignatureRejectsChangedQuery(t *testing.T) {
	auth := testAuth(t)
	sig := auth.SignRequest(http.MethodGet, "/v1/history?limit=10", "1780257600", "nonce-1", nil)

	if err := auth.VerifyRequest(http.MethodGet, "/v1/history?limit=10", "1780257600", "nonce-1", nil, sig); err != nil {
		t.Fatalf("VerifyRequest signed query: %v", err)
	}
	if err := auth.VerifyRequest(http.MethodGet, "/v1/history?limit=20", "1780257600", "nonce-1", nil, sig); err == nil {
		t.Fatal("expected query change to reject signature")
	}
}

func TestNonceCacheRejectsReplayAndExpiresOldEntries(t *testing.T) {
	cache := newNonceCache(2 * time.Minute)
	now := time.Unix(1780257600, 0)

	if cache.accept("", now) {
		t.Fatal("empty nonce accepted")
	}
	if !cache.accept("nonce-1", now) {
		t.Fatal("first nonce use rejected")
	}
	if cache.accept("nonce-1", now.Add(time.Minute)) {
		t.Fatal("replayed nonce accepted within ttl")
	}
	if !cache.accept("nonce-1", now.Add(2*time.Minute+time.Second)) {
		t.Fatal("nonce was not accepted after ttl expired")
	}
}

func TestAuthShortKeyRejected(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("tooshort"))
	if _, err := NewAuth(key); err == nil {
		t.Fatal("expected short-key rejection")
	}
}

func signedRequestWithNonce(t *testing.T, auth *Auth, method, target, nonce string, body []byte) *http.Request {
	t.Helper()
	ts := "1780257600"
	return signedRequestWithTimestampAndNonce(t, auth, method, target, ts, nonce, body)
}

func signedRequestWithTimestampAndNonce(t *testing.T, auth *Auth, method, target, ts, nonce string, body []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Clipfan-Ts", ts)
	req.Header.Set("X-Clipfan-Nonce", nonce)
	req.Header.Set("X-Clipfan-Sig", auth.SignRequest(method, target, ts, nonce, body))
	return req
}

func setFixedServerTime(s *Server) {
	s.now = func() time.Time { return time.Unix(1780257600, 0) }
}
