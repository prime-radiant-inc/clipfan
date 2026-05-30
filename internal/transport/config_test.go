package transport

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

func TestPostConfigDispatchesMaxHistory(t *testing.T) {
	auth, err := NewAuth("dGVzdC1rZXktMDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(":0", auth, func(clipboard.Content, string) {}, func() any { return nil })
	var got int
	s.SetConfigFunc(func(n int) error { got = n; return nil })

	body := []byte(`{"max_history":123}`)
	req := httptest.NewRequest("POST", "/v1/config", bytes.NewReader(body))
	req.Header.Set("X-Clipfan-Sig", auth.Sign(body))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("signed POST status = %d, want 200", rec.Code)
	}
	if got != 123 {
		t.Fatalf("hook received %d, want 123", got)
	}

	req2 := httptest.NewRequest("POST", "/v1/config", bytes.NewReader(body))
	rec2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned status = %d, want 401", rec2.Code)
	}
}
