package transport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFleetEndpointReturnsSignedLocalFleetView(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil, func() any { return nil })
	setFixedServerTime(srv)
	srv.SetRequiredLocalAuthVersion(AuthVersionRequestHMAC)
	srv.SetFleetFunc(func() any {
		return map[string]any{
			"origin": "linux-b",
			"hosts":  []map[string]any{{"id": "m4", "reachable": true}},
		}
	})

	req := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodGet, "/v1/fleet", "1780257600", "fleet-get", nil, AuthVersionRequestHMAC)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("fleet status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	requireVersionedSignedResponse(t, auth, rec, "fleet-get")
	var payload struct {
		Origin string `json:"origin"`
		Hosts  []struct {
			ID        string `json:"id"`
			Reachable bool   `json:"reachable"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Origin != "linux-b" || len(payload.Hosts) != 1 || payload.Hosts[0].ID != "m4" || !payload.Hosts[0].Reachable {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestFleetEndpointRequiresLoopbackSignature(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil, func() any { return nil })
	setFixedServerTime(srv)
	srv.SetFleetFunc(func() any { return map[string]any{} })

	unsigned := httptest.NewRequest(http.MethodGet, "/v1/fleet", nil)
	unsigned.RemoteAddr = "127.0.0.1:1234"
	unsignedRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(unsignedRec, unsigned)
	if unsignedRec.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned fleet status = %d, want 401", unsignedRec.Code)
	}

	remote := signedRequestWithNonce(t, auth, http.MethodGet, "/v1/fleet", "remote-fleet", nil)
	remote.RemoteAddr = "192.0.2.1:1234"
	remoteRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(remoteRec, remote)
	if remoteRec.Code != http.StatusForbidden {
		t.Fatalf("remote fleet status = %d, want 403", remoteRec.Code)
	}
}

func TestFleetEndpointNotWiredReturns501(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil, func() any { return nil })
	setFixedServerTime(srv)

	req := signedRequestWithNonce(t, auth, http.MethodGet, "/v1/fleet", "fleet-unwired", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("unwired fleet status = %d, want 501", rec.Code)
	}
}
