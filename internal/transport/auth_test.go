package transport

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
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

func TestResponseSignatureIsBoundToRequestNonceAndBody(t *testing.T) {
	auth := testAuth(t)
	body := []byte(`{"origin":"m4"}`)
	sig := auth.SignResponse("nonce-1", body)

	if err := auth.VerifyResponse("nonce-1", body, sig); err != nil {
		t.Fatalf("VerifyResponse signed body: %v", err)
	}
	if err := auth.VerifyResponse("nonce-2", body, sig); err == nil {
		t.Fatal("expected request nonce change to reject response signature")
	}
	if err := auth.VerifyResponse("nonce-1", []byte(`{"origin":"evil"}`), sig); err == nil {
		t.Fatal("expected body change to reject response signature")
	}
}

func TestHKDFDerivationVectors(t *testing.T) {
	rawKey := []byte("0123456789abcdef0123456789abcdef")
	tests := []struct {
		label string
		want  string
	}{
		{AuthVersionRequestHMAC, "1c23ce9e76df9696c06b04fa7f16dabd200d87043e31f592a91344899161a132"},
		{sshHelloHMACLabel, "0404698459498d07ba8d3f333d6d549b1553a83204a29b6e0e98d79882eb2537"},
		{bodyAEADLabel, "462bb7403a9e46da77af365a4a4eb2e342cdd9ba5755d21586fd4ee1334c0300"},
	}
	for _, tt := range tests {
		got, err := DeriveKey(rawKey, tt.label)
		if err != nil {
			t.Fatalf("DeriveKey(%q): %v", tt.label, err)
		}
		if hex.EncodeToString(got) != tt.want {
			t.Fatalf("DeriveKey(%q) = %x, want %s", tt.label, got, tt.want)
		}
	}
}

func TestVersionedRequestSignatureFixtures(t *testing.T) {
	auth := fixtureAuth(t)
	body := []byte(`{"id":"abc"}`)

	sig, err := auth.SignRequestWithAuthVersion(http.MethodGet, "/v1/history?limit=10", "1780257600", "nonce-1", nil, AuthVersionRequestHMAC)
	if err != nil {
		t.Fatalf("SignRequestWithAuthVersion GET: %v", err)
	}
	if sig != "8443d8072bd22b9ab5752160293eb8a2b9f48e225adbf3b8d6d8ca5439123d24" {
		t.Fatalf("GET versioned signature = %s", sig)
	}

	sig, err = auth.SignRequestWithAuthVersion(http.MethodPost, "/v1/restore", "1780257600", "nonce-2", body, AuthVersionRequestHMAC)
	if err != nil {
		t.Fatalf("SignRequestWithAuthVersion POST: %v", err)
	}
	if sig != "b035d02ef665e8a86b0a763e8f47e3a2492f2fa7cf39f9baa830f260206835aa" {
		t.Fatalf("POST versioned signature = %s", sig)
	}
	if err := auth.VerifyRequestWithAuthVersion(http.MethodPost, "/v1/restore", "1780257600", "nonce-2", body, sig, AuthVersionRequestHMAC); err != nil {
		t.Fatalf("VerifyRequestWithAuthVersion: %v", err)
	}
	if err := auth.VerifyRequestWithAuthVersion(http.MethodPost, "/v1/restore", "1780257600", "nonce-2", body, sig, ""); err == nil {
		t.Fatal("raw-key verification accepted versioned signature")
	}
}

func TestVersionedResponseSignatureFixture(t *testing.T) {
	auth := fixtureAuth(t)
	body := []byte(`{"origin":"m4"}`)

	sig, err := auth.SignResponseWithAuthVersion("nonce-1", body, AuthVersionRequestHMAC)
	if err != nil {
		t.Fatalf("SignResponseWithAuthVersion: %v", err)
	}
	if sig != "71ba47e6d02000e8852c4a7017f2ac1f6e0559d9ccf058e63265243859a2f6b4" {
		t.Fatalf("versioned response signature = %s", sig)
	}
	if err := auth.VerifyResponseWithAuthVersion("nonce-1", body, sig, AuthVersionRequestHMAC); err != nil {
		t.Fatalf("VerifyResponseWithAuthVersion: %v", err)
	}
	if err := auth.VerifyResponseWithAuthVersion("nonce-1", body, sig, ""); err == nil {
		t.Fatal("raw-key verification accepted versioned response signature")
	}
}

func TestSignedRequestHeadersCanEmitDormantAuthVersion(t *testing.T) {
	auth := fixtureAuth(t)
	headers, err := auth.SignedRequestHeaders(http.MethodGet, "/v1/history?limit=10", nil, SignedRequestOptions{
		Timestamp:   time.Unix(1780257600, 0),
		Nonce:       "nonce-1",
		AuthVersion: AuthVersionRequestHMAC,
	})
	if err != nil {
		t.Fatalf("SignedRequestHeaders: %v", err)
	}
	if headers[HeaderAuthVersion] != AuthVersionRequestHMAC {
		t.Fatalf("auth version header = %q", headers[HeaderAuthVersion])
	}
	if headers[HeaderSignature] != "8443d8072bd22b9ab5752160293eb8a2b9f48e225adbf3b8d6d8ca5439123d24" {
		t.Fatalf("signature header = %s", headers[HeaderSignature])
	}
}

func TestRequiredAuthVersionAcceptsNewClientAndRejectsOldRawClient(t *testing.T) {
	auth := fixtureAuth(t)
	body := []byte(`{"max_history":125}`)

	versionedSig, err := auth.SignRequestWithAuthVersion(http.MethodPatch, "/v1/config/listener", "1780257600", "nonce-1", body, AuthVersionRequestHMAC)
	if err != nil {
		t.Fatalf("SignRequestWithAuthVersion: %v", err)
	}
	if err := auth.VerifyRequestRequiredAuthVersion(http.MethodPatch, "/v1/config/listener", "1780257600", "nonce-1", body, versionedSig, AuthVersionRequestHMAC, AuthVersionRequestHMAC); err != nil {
		t.Fatalf("new client strict verify: %v", err)
	}

	rawSig := auth.SignRequest(http.MethodPatch, "/v1/config/listener", "1780257600", "nonce-2", body)
	if err := auth.VerifyRequestRequiredAuthVersion(http.MethodPatch, "/v1/config/listener", "1780257600", "nonce-2", body, rawSig, "", AuthVersionRequestHMAC); !errors.Is(err, ErrAuthVersionMismatch) {
		t.Fatalf("old raw client error = %v, want ErrAuthVersionMismatch", err)
	}
	if err := auth.VerifyRequestRequiredAuthVersion(http.MethodPatch, "/v1/config/listener", "1780257600", "nonce-2", body, rawSig, AuthVersionRequestHMAC, AuthVersionRequestHMAC); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("wrong-key versioned client error = %v, want ErrBadSignature", err)
	}
}

func TestServerAcceptsHKDFRequestAuthWithoutRejectingRawKeyClients(t *testing.T) {
	auth := fixtureAuth(t)
	s := NewServer(":0", auth, func(c clipboard.Content, origin string) {}, func() any {
		return map[string]any{"origin": "m4"}
	})
	setFixedServerTime(s)

	versioned := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodGet, "/v1/peers", "1780257600", "hkdf-peers", nil, AuthVersionRequestHMAC)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, versioned)
	if rec.Code != http.StatusOK {
		t.Fatalf("versioned peers status = %d body=%q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get(HeaderAuthVersion) != AuthVersionRequestHMAC {
		t.Fatalf("response auth version = %q", rec.Header().Get(HeaderAuthVersion))
	}
	if err := auth.VerifyResponseWithAuthVersion("hkdf-peers", rec.Body.Bytes(), rec.Header().Get("X-Clipfan-Response-Sig"), AuthVersionRequestHMAC); err != nil {
		t.Fatalf("versioned response signature: %v", err)
	}

	raw := signedRequestWithTimestampAndNonce(t, auth, http.MethodGet, "/v1/peers", "1780257600", "raw-peers", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, raw)
	if rec.Code != http.StatusOK {
		t.Fatalf("raw peers status = %d body=%q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get(HeaderAuthVersion) != "" {
		t.Fatalf("raw response unexpectedly set auth version %q", rec.Header().Get(HeaderAuthVersion))
	}
	if err := auth.VerifyResponse("raw-peers", rec.Body.Bytes(), rec.Header().Get("X-Clipfan-Response-Sig")); err != nil {
		t.Fatalf("raw response signature: %v", err)
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
	return signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, method, target, ts, nonce, body, "")
}

func signedRequestWithTimestampAndNonceAndAuthVersion(t *testing.T, auth *Auth, method, target, ts, nonce string, body []byte, authVersion string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	headers, err := auth.SignedRequestHeaders(method, target, body, SignedRequestOptions{
		Timestamp:   mustUnix(t, ts),
		Nonce:       nonce,
		AuthVersion: authVersion,
	})
	if err != nil {
		t.Fatalf("SignedRequestHeaders: %v", err)
	}
	for header, value := range headers {
		req.Header.Set(header, value)
	}
	return req
}

func mustUnix(t *testing.T, ts string) time.Time {
	t.Helper()
	unixTs, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		t.Fatalf("bad helper timestamp %q: %v", ts, err)
	}
	return time.Unix(unixTs, 0)
}

func setFixedServerTime(s *Server) {
	s.now = func() time.Time { return time.Unix(1780257600, 0) }
}

func fixtureAuth(t *testing.T) *Auth {
	t.Helper()
	auth, err := NewAuth("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatalf("NewAuth: %v", err)
	}
	return auth
}
