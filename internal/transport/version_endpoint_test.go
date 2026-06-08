package transport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVersionRequiresSignatureAndAllowsRemote(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil)
	setFixedServerTime(srv)
	srv.SetVersionFunc(func() any {
		return map[string]string{"version": "v9.8.7"}
	})

	unsigned := httptest.NewRequest(http.MethodGet, "/v1/version", nil)
	unsigned.RemoteAddr = "192.0.2.1:1234"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, unsigned)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned version status = %d, want 401", rec.Code)
	}

	remote := signedRequestWithNonce(t, auth, http.MethodGet, "/v1/version", "remote-version", nil)
	remote.RemoteAddr = "192.0.2.1:1234"
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, remote)
	if rec.Code != http.StatusOK {
		t.Fatalf("remote signed version status = %d, want 200", rec.Code)
	}
	requireSignedResponse(t, auth, rec, "remote-version")

	var body struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode version response: %v", err)
	}
	if body.Version != "v9.8.7" {
		t.Fatalf("version = %q, want v9.8.7", body.Version)
	}
}
