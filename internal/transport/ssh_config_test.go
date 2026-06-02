package transport

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

func TestSSHPeerConfigRoutesRequireHKDFAndDispatch(t *testing.T) {
	auth := fixtureAuth(t)
	s := NewServer(":0", auth, func(clipboard.Content, string) {}, func() any { return nil })
	setFixedServerTime(s)

	var gotReadPeer string
	var gotPutPeer string
	var gotPutBody []byte
	s.SetSSHPeerConfig(
		func(peerID string) (any, *HandlerError) {
			gotReadPeer = peerID
			return map[string]any{"peer_id": peerID, "config_revision": 7}, nil
		},
		func(peerID string, body []byte) (any, *HandlerError) {
			gotPutPeer = peerID
			gotPutBody = append([]byte(nil), body...)
			return map[string]any{"peer_id": peerID, "config_revision": 8}, nil
		},
	)

	getReq := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodGet, "/v1/config/ssh/peers/fsck", "1780257600", "ssh-peer-get", nil, AuthVersionRequestHMAC)
	getRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d body=%q, want 200", getRec.Code, getRec.Body.String())
	}
	if gotReadPeer != "fsck" {
		t.Fatalf("read peer = %q, want fsck", gotReadPeer)
	}
	if getRec.Header().Get(HeaderAuthVersion) != AuthVersionRequestHMAC {
		t.Fatalf("GET response auth version = %q", getRec.Header().Get(HeaderAuthVersion))
	}
	if err := auth.VerifyResponseWithAuthVersion("ssh-peer-get", getRec.Body.Bytes(), getRec.Header().Get("X-Clipfan-Response-Sig"), AuthVersionRequestHMAC); err != nil {
		t.Fatalf("GET response signature: %v", err)
	}

	body := []byte(`{"expected_config_revision":7,"peer":{"id":"fsck"}}`)
	putReq := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodPut, "/v1/config/ssh/peers/fsck", "1780257600", "ssh-peer-put", body, AuthVersionRequestHMAC)
	putRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%q, want 200", putRec.Code, putRec.Body.String())
	}
	if gotPutPeer != "fsck" {
		t.Fatalf("put peer = %q, want fsck", gotPutPeer)
	}
	if !bytes.Equal(gotPutBody, body) {
		t.Fatalf("put body = %q, want %q", gotPutBody, body)
	}
	if putRec.Header().Get(HeaderAuthVersion) != AuthVersionRequestHMAC {
		t.Fatalf("PUT response auth version = %q", putRec.Header().Get(HeaderAuthVersion))
	}

	rawReq := signedRequestWithTimestampAndNonce(t, auth, http.MethodGet, "/v1/config/ssh/peers/fsck", "1780257600", "ssh-peer-raw", nil)
	rawRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rawRec, rawReq)
	if rawRec.Code != http.StatusUnauthorized || !bytes.Contains(rawRec.Body.Bytes(), []byte("auth_version_mismatch")) {
		t.Fatalf("raw GET status/body = %d %q, want auth_version_mismatch", rawRec.Code, rawRec.Body.String())
	}
}

func TestSSHPeerConfigRoutesRejectRemoteAndMapHandlerErrors(t *testing.T) {
	auth := fixtureAuth(t)
	s := NewServer(":0", auth, func(clipboard.Content, string) {}, func() any { return nil })
	setFixedServerTime(s)
	s.SetSSHPeerConfig(
		func(peerID string) (any, *HandlerError) {
			return nil, &HandlerError{Status: http.StatusNotFound, Code: "ssh_peer_not_found"}
		},
		func(peerID string, body []byte) (any, *HandlerError) {
			t.Fatal("unexpected put handler")
			return nil, nil
		},
	)

	remoteReq := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodGet, "/v1/config/ssh/peers/fsck", "1780257600", "ssh-peer-remote", nil, AuthVersionRequestHMAC)
	remoteReq.RemoteAddr = "192.0.2.10:1234"
	remoteRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(remoteRec, remoteReq)
	if remoteRec.Code != http.StatusForbidden {
		t.Fatalf("remote GET status = %d, want 403", remoteRec.Code)
	}

	req := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodGet, "/v1/config/ssh/peers/missing", "1780257600", "ssh-peer-missing", nil, AuthVersionRequestHMAC)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || !bytes.Contains(rec.Body.Bytes(), []byte("ssh_peer_not_found")) {
		t.Fatalf("handler error status/body = %d %q", rec.Code, rec.Body.String())
	}
	if err := auth.VerifyResponseWithAuthVersion("ssh-peer-missing", rec.Body.Bytes(), rec.Header().Get("X-Clipfan-Response-Sig"), AuthVersionRequestHMAC); err != nil {
		t.Fatalf("error response signature: %v", err)
	}
}
