package transport

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

func testAuth(t *testing.T) *Auth {
	t.Helper()
	// base64 of a 32-byte key.
	a, err := NewAuth("dGVzdC1rZXktMDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")
	if err != nil {
		t.Fatalf("NewAuth: %v", err)
	}
	return a
}

// TestHistoryGETRequiresSignature verifies that GET /v1/history is gated behind
// the shared-key signature: a signed request succeeds, an unsigned one is denied.
func TestHistoryGETRequiresSignature(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil, func() any { return nil })
	setFixedServerTime(srv)
	called := false
	srv.SetHistory(
		func(limit int) (any, error) { called = true; return []string{"x"}, nil },
		nil, nil, nil,
	)

	// Signed GET → 200
	req := signedRequestWithNonce(t, auth, http.MethodGet, "/v1/history?limit=10", "history-get", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("signed GET status = %d, want 200", rec.Code)
	}
	if !called {
		t.Fatal("history func not invoked on signed GET")
	}

	// Unsigned GET → 401
	req2 := httptest.NewRequest(http.MethodGet, "/v1/history", nil)
	req2.RemoteAddr = "127.0.0.1:1234"
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned GET status = %d, want 401", rec2.Code)
	}
}

func TestRestoreRequiresSignature(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil, func() any { return nil })
	setFixedServerTime(srv)
	restored := ""
	srv.SetHistory(
		func(limit int) (any, error) { return nil, nil },
		func(id string) error { restored = id; return nil },
		func(id string, pinned bool) error { return nil },
		func(id string, allUnpinned bool) error { return nil },
	)

	body, _ := json.Marshal(map[string]string{"id": "abc"})
	req := signedRequestWithNonce(t, auth, http.MethodPost, "/v1/restore", "restore", body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("signed status = %d, want 200", rec.Code)
	}
	if restored != "abc" {
		t.Fatalf("restore id = %q, want abc", restored)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/v1/restore", bytes.NewReader(body))
	req2.RemoteAddr = "127.0.0.1:1234"
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned status = %d, want 401", rec2.Code)
	}
}

func TestPinAndDeleteSigned(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil, func() any { return nil })
	setFixedServerTime(srv)
	var pinnedID string
	var deletedAll bool
	srv.SetHistory(
		func(limit int) (any, error) { return nil, nil },
		func(id string) error { return nil },
		func(id string, pinned bool) error { pinnedID = id; return nil },
		func(id string, allUnpinned bool) error { deletedAll = allUnpinned; return nil },
	)

	pb, _ := json.Marshal(map[string]any{"id": "p1", "pinned": true})
	preq := signedRequestWithNonce(t, auth, http.MethodPost, "/v1/history/pin", "pin", pb)
	prec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(prec, preq)
	if prec.Code != http.StatusOK || pinnedID != "p1" {
		t.Fatalf("pin failed: code=%d id=%q", prec.Code, pinnedID)
	}

	db, _ := json.Marshal(map[string]any{"all_unpinned": true})
	dreq := signedRequestWithNonce(t, auth, http.MethodDelete, "/v1/history", "delete", db)
	drec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(drec, dreq)
	if drec.Code != http.StatusOK || !deletedAll {
		t.Fatalf("delete-all failed: code=%d all=%v", drec.Code, deletedAll)
	}
}

func TestHistoryRejectsValidRemoteSignature(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil, func() any { return nil })
	setFixedServerTime(srv)
	srv.SetHistory(
		func(limit int) (any, error) { return []string{"x"}, nil },
		nil, nil, nil,
	)

	req := signedRequestWithNonce(t, auth, http.MethodGet, "/v1/history?limit=10", "remote-history", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("remote signed GET status = %d, want 403", rec.Code)
	}
}

func TestPeersRequiresSignatureAndLoopback(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil, func() any {
		return map[string]any{"origin": "local", "peers": []string{}}
	})
	setFixedServerTime(srv)

	unsigned := httptest.NewRequest(http.MethodGet, "/v1/peers", nil)
	unsigned.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, unsigned)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned peers status = %d, want 401", rec.Code)
	}

	remote := signedRequestWithNonce(t, auth, http.MethodGet, "/v1/peers", "remote-peers", nil)
	remote.RemoteAddr = "192.0.2.1:1234"
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, remote)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("remote signed peers status = %d, want 403", rec.Code)
	}

	local := signedRequestWithNonce(t, auth, http.MethodGet, "/v1/peers", "local-peers", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, local)
	if rec.Code != http.StatusOK {
		t.Fatalf("local signed peers status = %d, want 200", rec.Code)
	}
}

func TestFutureTimestampReplayRejectedUntilSkewWindowExpires(t *testing.T) {
	auth := testAuth(t)
	now := time.Unix(1780257600, 0)
	received := 0
	srv := NewServer(":0", auth, func(clipboard.Content, string) {
		received++
	}, nil)
	srv.now = func() time.Time { return now }

	sealedBody, bodyNonce, err := auth.SealBody([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(Envelope{
		ID:     "clip-1",
		Origin: "sender",
		TS:     now.UTC(),
		Kind:   "text",
		Body:   sealedBody,
		Nonce:  bodyNonce,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := strconv.FormatInt(now.Add(119*time.Second).Unix(), 10)
	req := signedRequestWithTimestampAndNonce(t, auth, http.MethodPost, "/v1/clip", ts, "future-replay", body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("first signed clip status = %d, want 204", rec.Code)
	}

	now = now.Add(121 * time.Second)
	req = signedRequestWithTimestampAndNonce(t, auth, http.MethodPost, "/v1/clip", ts, "future-replay", body)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("replayed future-skew clip status = %d, want 401", rec.Code)
	}
	if received != 1 {
		t.Fatalf("received clips = %d, want 1", received)
	}
}

func TestPostClipRejectsWrongLengthBodyNonce(t *testing.T) {
	auth := testAuth(t)
	received := 0
	srv := NewServer(":0", auth, func(clipboard.Content, string) {
		received++
	}, nil)
	setFixedServerTime(srv)

	body, err := json.Marshal(Envelope{
		ID:     "clip-bad-nonce",
		Origin: "sender",
		TS:     time.Unix(1780257600, 0).UTC(),
		Kind:   "text",
		Body:   "AA==",
		Nonce:  "AA==",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := signedRequestWithNonce(t, auth, http.MethodPost, "/v1/clip", "bad-body-nonce", body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("wrong-length nonce status = %d, want 400", rec.Code)
	}
	if rec.Body.String() != "decrypt envelope body\n" {
		t.Fatalf("wrong-length nonce response = %q", rec.Body.String())
	}
	if received != 0 {
		t.Fatalf("received clips = %d, want 0", received)
	}
}

func TestPostClipRejectsMalformedCiphertext(t *testing.T) {
	auth := testAuth(t)
	received := 0
	srv := NewServer(":0", auth, func(clipboard.Content, string) {
		received++
	}, nil)
	setFixedServerTime(srv)

	_, nonce, err := auth.SealBody([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(Envelope{
		ID:     "clip-bad-body",
		Origin: "sender",
		TS:     time.Unix(1780257600, 0).UTC(),
		Kind:   "text",
		Body:   EncodeBody([]byte("bad ciphertext")),
		Nonce:  nonce,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := signedRequestWithNonce(t, auth, http.MethodPost, "/v1/clip", "bad-body-ciphertext", body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed ciphertext status = %d, want 400", rec.Code)
	}
	if rec.Body.String() != "decrypt envelope body\n" {
		t.Fatalf("malformed ciphertext response = %q", rec.Body.String())
	}
	if received != 0 {
		t.Fatalf("received clips = %d, want 0", received)
	}
}
