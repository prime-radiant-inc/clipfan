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
	var gotPatchPeer string
	var gotPatchBody []byte
	var gotTransitionPeer string
	var gotTransitionBody []byte
	var gotDisablePeer string
	var gotDisableBody []byte
	var gotDeletePeer string
	var gotDeleteBody []byte
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
	s.SetSSHPeerConfigProofPatch(func(peerID string, body []byte) (any, *HandlerError) {
		gotPatchPeer = peerID
		gotPatchBody = append([]byte(nil), body...)
		return map[string]any{"peer_id": peerID, "config_revision": 9}, nil
	})
	s.SetSSHPeerConfigTransition(func(peerID string, body []byte) (any, *HandlerError) {
		gotTransitionPeer = peerID
		gotTransitionBody = append([]byte(nil), body...)
		return map[string]any{"peer_id": peerID, "config_revision": 10}, nil
	})
	s.SetSSHPeerConfigDisable(func(peerID string, body []byte) (any, *HandlerError) {
		gotDisablePeer = peerID
		gotDisableBody = append([]byte(nil), body...)
		return map[string]any{"peer_id": peerID, "config_revision": 11}, nil
	})
	s.SetSSHPeerConfigDelete(func(peerID string, body []byte) (any, *HandlerError) {
		gotDeletePeer = peerID
		gotDeleteBody = append([]byte(nil), body...)
		return map[string]any{"peer_id": peerID, "config_revision": 12}, nil
	})

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

	patchBody := []byte(`{"expected_config_revision":8,"accept_proof":{"key_id":"a4a4a4a4","gateway_path":"/Users/jesse/.local/bin/clipfan","verified_at":"2026-06-01T12:34:56Z","verified_by":"local_file"}}`)
	patchReq := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodPatch, "/v1/config/ssh/peers/fsck/proof", "1780257600", "ssh-peer-proof", patchBody, AuthVersionRequestHMAC)
	patchRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d body=%q, want 200", patchRec.Code, patchRec.Body.String())
	}
	if gotPatchPeer != "fsck" {
		t.Fatalf("patch peer = %q, want fsck", gotPatchPeer)
	}
	if !bytes.Equal(gotPatchBody, patchBody) {
		t.Fatalf("patch body = %q, want %q", gotPatchBody, patchBody)
	}
	if patchRec.Header().Get(HeaderAuthVersion) != AuthVersionRequestHMAC {
		t.Fatalf("PATCH response auth version = %q", patchRec.Header().Get(HeaderAuthVersion))
	}
	if err := auth.VerifyResponseWithAuthVersion("ssh-peer-proof", patchRec.Body.Bytes(), patchRec.Header().Get("X-Clipfan-Response-Sig"), AuthVersionRequestHMAC); err != nil {
		t.Fatalf("PATCH response signature: %v", err)
	}

	transitionBody := []byte(`{"expected_config_revision":9,"from_state":"loopback_unprovisioned","to_state":"ssh_material_staged","reason":"staged","log_id":"peer-log-1780257600"}`)
	transitionReq := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodPost, "/v1/config/ssh/peers/fsck/transition", "1780257600", "ssh-peer-transition", transitionBody, AuthVersionRequestHMAC)
	transitionRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(transitionRec, transitionReq)
	if transitionRec.Code != http.StatusOK {
		t.Fatalf("POST transition status = %d body=%q, want 200", transitionRec.Code, transitionRec.Body.String())
	}
	if gotTransitionPeer != "fsck" {
		t.Fatalf("transition peer = %q, want fsck", gotTransitionPeer)
	}
	if !bytes.Equal(gotTransitionBody, transitionBody) {
		t.Fatalf("transition body = %q, want %q", gotTransitionBody, transitionBody)
	}
	if transitionRec.Header().Get(HeaderAuthVersion) != AuthVersionRequestHMAC {
		t.Fatalf("transition response auth version = %q", transitionRec.Header().Get(HeaderAuthVersion))
	}
	if err := auth.VerifyResponseWithAuthVersion("ssh-peer-transition", transitionRec.Body.Bytes(), transitionRec.Header().Get("X-Clipfan-Response-Sig"), AuthVersionRequestHMAC); err != nil {
		t.Fatalf("transition response signature: %v", err)
	}

	disableBody := []byte(`{"expected_config_revision":10,"reason":"user_disabled"}`)
	disableReq := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodPost, "/v1/config/ssh/peers/fsck/disable", "1780257600", "ssh-peer-disable", disableBody, AuthVersionRequestHMAC)
	disableRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(disableRec, disableReq)
	if disableRec.Code != http.StatusOK {
		t.Fatalf("POST disable status = %d body=%q, want 200", disableRec.Code, disableRec.Body.String())
	}
	if gotDisablePeer != "fsck" {
		t.Fatalf("disable peer = %q, want fsck", gotDisablePeer)
	}
	if !bytes.Equal(gotDisableBody, disableBody) {
		t.Fatalf("disable body = %q, want %q", gotDisableBody, disableBody)
	}
	if disableRec.Header().Get(HeaderAuthVersion) != AuthVersionRequestHMAC {
		t.Fatalf("disable response auth version = %q", disableRec.Header().Get(HeaderAuthVersion))
	}
	if err := auth.VerifyResponseWithAuthVersion("ssh-peer-disable", disableRec.Body.Bytes(), disableRec.Header().Get("X-Clipfan-Response-Sig"), AuthVersionRequestHMAC); err != nil {
		t.Fatalf("disable response signature: %v", err)
	}

	deleteBody := []byte(`{"expected_config_revision":11,"reason":"user_deleted","log_id":"peer-log-1780257600"}`)
	deleteReq := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodDelete, "/v1/config/ssh/peers/fsck", "1780257600", "ssh-peer-delete", deleteBody, AuthVersionRequestHMAC)
	deleteRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d body=%q, want 200", deleteRec.Code, deleteRec.Body.String())
	}
	if gotDeletePeer != "fsck" {
		t.Fatalf("delete peer = %q, want fsck", gotDeletePeer)
	}
	if !bytes.Equal(gotDeleteBody, deleteBody) {
		t.Fatalf("delete body = %q, want %q", gotDeleteBody, deleteBody)
	}
	if deleteRec.Header().Get(HeaderAuthVersion) != AuthVersionRequestHMAC {
		t.Fatalf("delete response auth version = %q", deleteRec.Header().Get(HeaderAuthVersion))
	}
	if err := auth.VerifyResponseWithAuthVersion("ssh-peer-delete", deleteRec.Body.Bytes(), deleteRec.Header().Get("X-Clipfan-Response-Sig"), AuthVersionRequestHMAC); err != nil {
		t.Fatalf("delete response signature: %v", err)
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
	s.SetSSHPeerConfigProofPatch(func(peerID string, body []byte) (any, *HandlerError) {
		return nil, &HandlerError{Status: http.StatusConflict, Code: "proof_mismatch"}
	})
	s.SetSSHPeerConfigTransition(func(peerID string, body []byte) (any, *HandlerError) {
		return nil, &HandlerError{Status: http.StatusBadRequest, Code: "bad_request"}
	})
	s.SetSSHPeerConfigDisable(func(peerID string, body []byte) (any, *HandlerError) {
		return nil, &HandlerError{Status: http.StatusBadRequest, Code: "bad_request"}
	})
	s.SetSSHPeerConfigDelete(func(peerID string, body []byte) (any, *HandlerError) {
		return nil, &HandlerError{Status: http.StatusConflict, Code: "ssh_peer_delete_blocked"}
	})

	remoteGetReq := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodGet, "/v1/config/ssh/peers/fsck", "1780257600", "ssh-peer-remote-get", nil, AuthVersionRequestHMAC)
	remoteGetReq.RemoteAddr = "192.0.2.10:1234"
	remoteGetRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(remoteGetRec, remoteGetReq)
	if remoteGetRec.Code != http.StatusForbidden {
		t.Fatalf("remote GET status = %d, want 403", remoteGetRec.Code)
	}

	remotePatchReq := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodPatch, "/v1/config/ssh/peers/fsck/proof", "1780257600", "ssh-peer-remote-patch", []byte(`{}`), AuthVersionRequestHMAC)
	remotePatchReq.RemoteAddr = "192.0.2.10:1234"
	remotePatchRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(remotePatchRec, remotePatchReq)
	if remotePatchRec.Code != http.StatusForbidden {
		t.Fatalf("remote PATCH status = %d, want 403", remotePatchRec.Code)
	}

	remoteTransitionReq := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodPost, "/v1/config/ssh/peers/fsck/transition", "1780257600", "ssh-peer-remote-transition", []byte(`{}`), AuthVersionRequestHMAC)
	remoteTransitionReq.RemoteAddr = "192.0.2.10:1234"
	remoteTransitionRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(remoteTransitionRec, remoteTransitionReq)
	if remoteTransitionRec.Code != http.StatusForbidden {
		t.Fatalf("remote transition status = %d, want 403", remoteTransitionRec.Code)
	}

	remoteDisableReq := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodPost, "/v1/config/ssh/peers/fsck/disable", "1780257600", "ssh-peer-remote-disable", []byte(`{}`), AuthVersionRequestHMAC)
	remoteDisableReq.RemoteAddr = "192.0.2.10:1234"
	remoteDisableRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(remoteDisableRec, remoteDisableReq)
	if remoteDisableRec.Code != http.StatusForbidden {
		t.Fatalf("remote disable status = %d, want 403", remoteDisableRec.Code)
	}

	remoteDeleteReq := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodDelete, "/v1/config/ssh/peers/fsck", "1780257600", "ssh-peer-remote-delete", []byte(`{}`), AuthVersionRequestHMAC)
	remoteDeleteReq.RemoteAddr = "192.0.2.10:1234"
	remoteDeleteRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(remoteDeleteRec, remoteDeleteReq)
	if remoteDeleteRec.Code != http.StatusForbidden {
		t.Fatalf("remote delete status = %d, want 403", remoteDeleteRec.Code)
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

	patchReq := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodPatch, "/v1/config/ssh/peers/fsck/proof", "1780257600", "ssh-peer-proof-mismatch", []byte(`{}`), AuthVersionRequestHMAC)
	patchRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusConflict || !bytes.Contains(patchRec.Body.Bytes(), []byte("proof_mismatch")) {
		t.Fatalf("PATCH handler error status/body = %d %q", patchRec.Code, patchRec.Body.String())
	}
	if err := auth.VerifyResponseWithAuthVersion("ssh-peer-proof-mismatch", patchRec.Body.Bytes(), patchRec.Header().Get("X-Clipfan-Response-Sig"), AuthVersionRequestHMAC); err != nil {
		t.Fatalf("PATCH error response signature: %v", err)
	}

	transitionReq := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodPost, "/v1/config/ssh/peers/fsck/transition", "1780257600", "ssh-peer-transition-bad", []byte(`{}`), AuthVersionRequestHMAC)
	transitionRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(transitionRec, transitionReq)
	if transitionRec.Code != http.StatusBadRequest || !bytes.Contains(transitionRec.Body.Bytes(), []byte("bad_request")) {
		t.Fatalf("transition handler error status/body = %d %q", transitionRec.Code, transitionRec.Body.String())
	}
	if err := auth.VerifyResponseWithAuthVersion("ssh-peer-transition-bad", transitionRec.Body.Bytes(), transitionRec.Header().Get("X-Clipfan-Response-Sig"), AuthVersionRequestHMAC); err != nil {
		t.Fatalf("transition error response signature: %v", err)
	}

	disableReq := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodPost, "/v1/config/ssh/peers/fsck/disable", "1780257600", "ssh-peer-disable-bad", []byte(`{}`), AuthVersionRequestHMAC)
	disableRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(disableRec, disableReq)
	if disableRec.Code != http.StatusBadRequest || !bytes.Contains(disableRec.Body.Bytes(), []byte("bad_request")) {
		t.Fatalf("disable handler error status/body = %d %q", disableRec.Code, disableRec.Body.String())
	}
	if err := auth.VerifyResponseWithAuthVersion("ssh-peer-disable-bad", disableRec.Body.Bytes(), disableRec.Header().Get("X-Clipfan-Response-Sig"), AuthVersionRequestHMAC); err != nil {
		t.Fatalf("disable error response signature: %v", err)
	}

	deleteReq := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodDelete, "/v1/config/ssh/peers/fsck", "1780257600", "ssh-peer-delete-blocked", []byte(`{}`), AuthVersionRequestHMAC)
	deleteRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusConflict || !bytes.Contains(deleteRec.Body.Bytes(), []byte("ssh_peer_delete_blocked")) {
		t.Fatalf("delete handler error status/body = %d %q", deleteRec.Code, deleteRec.Body.String())
	}
	if err := auth.VerifyResponseWithAuthVersion("ssh-peer-delete-blocked", deleteRec.Body.Bytes(), deleteRec.Header().Get("X-Clipfan-Response-Sig"), AuthVersionRequestHMAC); err != nil {
		t.Fatalf("delete error response signature: %v", err)
	}
}
