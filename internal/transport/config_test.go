package transport

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPostConfigDispatchesMaxHistory(t *testing.T) {
	auth, err := NewAuth("dGVzdC1rZXktMDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(":0", auth, func() any { return nil })
	setFixedServerTime(s)
	var got int
	s.SetConfigFunc(func(n int) error { got = n; return nil })

	body := []byte(`{"max_history":123}`)
	req := signedRequestWithNonce(t, auth, http.MethodPost, "/v1/config", "config", body)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("signed POST status = %d, want 200", rec.Code)
	}
	if got != 123 {
		t.Fatalf("hook received %d, want 123", got)
	}

	req2 := httptest.NewRequest("POST", "/v1/config", bytes.NewReader(body))
	req2.RemoteAddr = "127.0.0.1:1234"
	rec2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned status = %d, want 401", rec2.Code)
	}
}

func TestPostConfigPropagatesConfigErrorCode(t *testing.T) {
	auth, err := NewAuth("dGVzdC1rZXktMDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(":0", auth, func() any { return nil })
	setFixedServerTime(s)
	s.SetConfigFunc(func(int) error { return errors.New("config_v2_writes_disabled") })

	body := []byte(`{"max_history":123}`)
	req := signedRequestWithNonce(t, auth, http.MethodPost, "/v1/config", "config", body)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("signed POST status = %d, want 400", rec.Code)
	}
	bodyBytes := rec.Body.Bytes()
	if !strings.Contains(string(bodyBytes), "config_v2_writes_disabled") {
		t.Fatalf("response body = %q, want stable code", string(bodyBytes))
	}
	if sig := rec.Header().Get("X-Clipfan-Response-Sig"); sig == "" {
		t.Fatal("missing signed response header")
	} else if err := auth.VerifyResponse("config", bodyBytes, sig); err != nil {
		t.Fatalf("bad response signature: %v", err)
	}
}
