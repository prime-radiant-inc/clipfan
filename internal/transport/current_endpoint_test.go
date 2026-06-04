package transport

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

func TestCurrentEndpointReturnsSignedLocalVisibleCurrent(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil, func() any { return nil })
	setFixedServerTime(srv)
	srv.SetRequiredLocalAuthVersion(AuthVersionRequestHMAC)
	content := clipboard.New(clipboard.KindText, []byte("latest"), time.Unix(1780257600, 0).UTC())
	content.ID = "clip-current"
	srv.SetCurrentFunc(func() CurrentPayload {
		return CurrentPayloadFromContent(content, "linux-b")
	})

	req := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodGet, "/v1/current", "1780257600", "current-get", nil, AuthVersionRequestHMAC)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("current status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	requireVersionedSignedResponse(t, auth, rec, "current-get")
	var payload CurrentPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	got, ok, err := payload.Content()
	if err != nil {
		t.Fatal(err)
	}
	if !ok || payload.Origin != "linux-b" || got.ID != "clip-current" || string(got.Bytes) != "latest" {
		t.Fatalf("payload = %#v content=%#v ok=%v", payload, got, ok)
	}
}

func TestCurrentEndpointRequiresLoopbackSignature(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil, func() any { return nil })
	setFixedServerTime(srv)
	srv.SetCurrentFunc(func() CurrentPayload { return NoCurrentPayload("no_visible_current") })

	unsigned := httptest.NewRequest(http.MethodGet, "/v1/current", nil)
	unsigned.RemoteAddr = "127.0.0.1:1234"
	unsignedRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(unsignedRec, unsigned)
	if unsignedRec.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned current status = %d, want 401", unsignedRec.Code)
	}

	remote := signedRequestWithNonce(t, auth, http.MethodGet, "/v1/current", "remote-current", nil)
	remote.RemoteAddr = "192.0.2.1:1234"
	remoteRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(remoteRec, remote)
	if remoteRec.Code != http.StatusForbidden {
		t.Fatalf("remote current status = %d, want 403", remoteRec.Code)
	}
}

func TestReadLimitedCurrentBodyReportsOversize(t *testing.T) {
	_, err := readLimitedBody(strings.NewReader("123456"), 5, ErrSSHStreamFrameTooLarge)
	if !errors.Is(err, ErrSSHStreamFrameTooLarge) {
		t.Fatalf("readLimitedBody error = %v, want ErrSSHStreamFrameTooLarge", err)
	}
}
