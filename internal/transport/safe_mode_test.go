package transport

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSafeModeHealthRemainsUnsigned(t *testing.T) {
	srv := NewServer(":0", testAuth(t), nil)
	srv.SetSafeMode(true)

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "ok\n" {
		t.Fatalf("health response = %d %q, want 200 ok", rec.Code, rec.Body.String())
	}
}

func TestSafeModeRejectsNormalMutationAndClipboardRoutes(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, func() any {
		t.Fatal("safe mode invoked peers handler")
		return nil
	})
	setFixedServerTime(srv)
	srv.SetSafeMode(true)
	srv.SetHistory(
		func(int) (any, error) {
			t.Fatal("safe mode invoked history handler")
			return nil, nil
		},
		func(string) error {
			t.Fatal("safe mode invoked restore handler")
			return nil
		},
		func(string, bool) error {
			t.Fatal("safe mode invoked pin handler")
			return nil
		},
		func(string, bool) error {
			t.Fatal("safe mode invoked delete handler")
			return nil
		},
	)
	srv.SetConfigFunc(func(int) error {
		t.Fatal("safe mode invoked config mutation handler")
		return nil
	})

	cases := []struct {
		name   string
		method string
		target string
		body   []byte
	}{
		{name: "current apply", method: http.MethodPost, target: "/v1/current", body: []byte(`{}`)},
		{name: "history read", method: http.MethodGet, target: "/v1/history?limit=10"},
		{name: "history delete", method: http.MethodDelete, target: "/v1/history", body: []byte(`{}`)},
		{name: "restore", method: http.MethodPost, target: "/v1/restore", body: []byte(`{"id":"x"}`)},
		{name: "pin", method: http.MethodPost, target: "/v1/history/pin", body: []byte(`{"id":"x","pinned":true}`)},
		{name: "config mutation", method: http.MethodPost, target: "/v1/config", body: []byte(`{"max_history":123}`)},
		{name: "current read", method: http.MethodGet, target: "/v1/current"},
		{name: "current watch", method: http.MethodGet, target: "/v1/current/watch"},
		{name: "receive primitive", method: http.MethodPost, target: "/v1/receive", body: []byte(`{}`)},
		{name: "sync stream", method: http.MethodGet, target: "/v1/sync-stream"},
		{name: "gateway reservation", method: http.MethodPost, target: "/v1/gateway/session", body: []byte(`{}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := signedRequestWithNonce(t, auth, tc.method, tc.target, "safe-"+tc.name, tc.body)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			requireSafeModeError(t, rec, http.StatusConflict, "public_listen_requires_confirmation")
		})
	}
}

func TestSafeModeVersionIsSignedLocalOnly(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil)
	setFixedServerTime(srv)
	srv.SetSafeMode(true)
	cfgVersion := 2
	cfgRevision := uint64(7)
	srv.SetSafeModeInfo(SafeModeInfo{ConfigVersion: &cfgVersion, ConfigRevision: &cfgRevision})
	srv.SetVersionFunc(func() any { return map[string]string{"version": "v1.2.3"} })

	local := signedSafeModeRequest(t, auth, http.MethodGet, "/v1/version", "safe-version", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, local)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "v1.2.3") {
		t.Fatalf("local version response = %d %q, want signed version", rec.Code, rec.Body.String())
	}
	requireVersionedSignedResponse(t, auth, rec, "safe-version")
	payload := decodeJSONMap(t, rec)
	if payload["safe_mode"] != true || payload["version"] != "v1.2.3" || payload["config_version"] != float64(2) || payload["config_revision"] != float64(7) {
		t.Fatalf("version payload = %#v, want safe-mode version metadata", payload)
	}

	remote := signedSafeModeRequest(t, auth, http.MethodGet, "/v1/version", "remote-safe-version", nil)
	remote.RemoteAddr = "192.0.2.10:1234"
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, remote)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("remote safe-mode version = %d, want 403", rec.Code)
	}
}

func TestSafeModeStatusAndLogFamiliesDoNotFallThroughToNormalPeers(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, func() any {
		t.Fatal("safe mode fell through to normal peers handler")
		return nil
	})
	setFixedServerTime(srv)
	srv.SetSafeMode(true)
	srv.SetVersionFunc(func() any { return map[string]string{"version": "v1.2.3"} })

	for i, target := range []string{"/v1/status", "/v1/peers", "/v1/ssh/logs?peer=local"} {
		nonce := "safe-status-logs-" + string(rune('a'+i))
		req := signedSafeModeRequest(t, auth, http.MethodGet, target, nonce, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%q, want 200", target, rec.Code, rec.Body.String())
		}
		requireVersionedSignedResponse(t, auth, rec, nonce)
	}
}

func TestSafeModeStatusAndPeersExposeMinimalSchema(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, func() any {
		t.Fatal("safe mode fell through to normal peers handler")
		return nil
	})
	setFixedServerTime(srv)
	srv.SetSafeMode(true)
	cfgVersion := 2
	cfgRevision := uint64(9)
	srv.SetSafeModeInfo(SafeModeInfo{
		Origin:                "m4",
		Hostname:              "m4",
		ConfiguredListen:      "0.0.0.0:9000",
		EffectiveRepairListen: "127.0.0.1:9000",
		ParseError:            "invalid_listen_port",
		PeerSyncStarted:       false,
		ConfigVersion:         &cfgVersion,
		ConfigRevision:        &cfgRevision,
		Port:                  9000,
		StaticPeers:           []string{"old-box"},
	})
	srv.SetVersionFunc(func() any { return map[string]string{"version": "v1.2.3"} })

	statusReq := signedSafeModeRequest(t, auth, http.MethodGet, "/v1/status", "safe-status-schema", nil)
	statusRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("status response = %d %q, want 200", statusRec.Code, statusRec.Body.String())
	}
	requireVersionedSignedResponse(t, auth, statusRec, "safe-status-schema")
	status := decodeJSONMap(t, statusRec)
	assertMinimalSafeModeStatus(t, status)

	peersReq := signedSafeModeRequest(t, auth, http.MethodGet, "/v1/peers", "safe-peers-schema", nil)
	peersRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(peersRec, peersReq)
	if peersRec.Code != http.StatusOK {
		t.Fatalf("peers response = %d %q, want 200", peersRec.Code, peersRec.Body.String())
	}
	requireVersionedSignedResponse(t, auth, peersRec, "safe-peers-schema")
	peers := decodeJSONMap(t, peersRec)
	assertMinimalSafeModeStatus(t, peers)
	if peers["origin"] != "m4" || peers["version"] != "v1.2.3" {
		t.Fatalf("peers origin/version = %#v", peers)
	}
	peerRows := peers["peers"].([]any)
	if len(peerRows) != 1 {
		t.Fatalf("safe-mode peers compatibility list = %#v, want one setup row", peerRows)
	}
	peerRow := peerRows[0].(map[string]any)
	if peerRow["hostname"] != "old-box" || peerRow["port"] != float64(9000) || peerRow["last_push_ok"] != false || peerRow["source"] != "static_peers" || peerRow["status"] != "ssh_setup_required" {
		t.Fatalf("safe-mode peer row = %#v", peerRow)
	}
}

func TestSafeModeLogsExposeGlobalEntriesAndRejectPeerScopedLogs(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil)
	setFixedServerTime(srv)
	srv.SetSafeMode(true)
	srv.SetSafeModeInfo(SafeModeInfo{
		StaticPeers:           []string{"old-box"},
		ConfiguredListen:      "0.0.0.0:9000",
		EffectiveRepairListen: "127.0.0.1:9000",
		Port:                  9000,
	})

	globalReq := signedSafeModeRequest(t, auth, http.MethodGet, "/v1/ssh/logs?limit=50", "safe-global-logs", nil)
	globalRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(globalRec, globalReq)
	if globalRec.Code != http.StatusOK {
		t.Fatalf("global logs = %d %q, want 200", globalRec.Code, globalRec.Body.String())
	}
	requireVersionedSignedResponse(t, auth, globalRec, "safe-global-logs")
	global := decodeJSONMap(t, globalRec)
	if global["peer_id"] != "local" || global["safe_mode"] != true || global["truncated"] != false {
		t.Fatalf("global log metadata = %#v", global)
	}
	if global["safe_mode_schema"] != "safe_mode_v1" ||
		global["listener_repair_status"] != "needs_repair" ||
		global["last_failure_phase"] != "listener_safe_mode" ||
		global["safe_mode_logs_available"] != true {
		t.Fatalf("global log app-facing status fields = %#v", global)
	}
	for _, forbidden := range []string{"ssh", "transport_health", "ssh_peers", "runtime_health", "peer_health", "remote_shared_key", "shared_key"} {
		if _, ok := global[forbidden]; ok {
			t.Fatalf("safe-mode logs exposed forbidden field %s: %#v", forbidden, global)
		}
	}
	entries := global["entries"].([]any)
	if len(entries) < 2 {
		t.Fatalf("entries = %#v, want listener and legacy static peer entries", entries)
	}
	for _, raw := range entries {
		entry := raw.(map[string]any)
		for _, key := range []string{"ts", "source", "durable", "log_id", "phase", "code", "message"} {
			if _, ok := entry[key]; !ok {
				t.Fatalf("entry %#v missing %s", entry, key)
			}
		}
		for _, forbidden := range []string{"shared_key", "nonce", "clip_id", "private_key_path", "argv"} {
			if _, ok := entry[forbidden]; ok {
				t.Fatalf("entry %#v exposed forbidden field %s", entry, forbidden)
			}
		}
	}

	peerReq := signedSafeModeRequest(t, auth, http.MethodGet, "/v1/ssh/logs?peer=old-box", "safe-peer-logs", nil)
	peerRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(peerRec, peerReq)
	requireSignedSafeModeError(t, auth, peerRec, "safe-peer-logs", http.StatusServiceUnavailable, "ssh_peer_logs_unavailable_before_schema")
	peerPayload := decodeJSONMap(t, peerRec)
	if peerPayload["safe_mode_schema"] != "safe_mode_v1" ||
		peerPayload["listener_repair_status"] != "needs_repair" ||
		peerPayload["last_failure_phase"] != "listener_safe_mode" ||
		peerPayload["safe_mode_logs_available"] != true {
		t.Fatalf("peer log app-facing status fields = %#v", peerPayload)
	}
	if entries := peerPayload["entries"].([]any); len(entries) != 0 {
		t.Fatalf("peer-scoped entries = %#v, want none", entries)
	}
}

func TestSafeModeListenerRepairRoutesAreUnavailableWhenUnwired(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil)
	setFixedServerTime(srv)
	srv.SetSafeMode(true)

	for _, tc := range []struct {
		method string
		body   []byte
	}{
		{method: http.MethodGet},
		{method: http.MethodPatch, body: []byte(`{"listen":"127.0.0.1:7853"}`)},
	} {
		req := signedSafeModeRequest(t, auth, tc.method, "/v1/config/listener", "safe-listener-"+tc.method, tc.body)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		requireSignedSafeModeError(t, auth, rec, "safe-listener-"+tc.method, http.StatusServiceUnavailable, "listener_repair_unavailable")
	}
}

func TestSafeModeListenerRepairRoutesUseSignedHandlerSeam(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil)
	setFixedServerTime(srv)
	srv.SetSafeMode(true)
	patchBody := []byte(`{"expected_revision_state":"versioned","expected_config_revision":7,"listen":"127.0.0.1:9000","port":9000}`)
	patchCalled := false
	srv.SetListenerRepair(
		func() (any, *HandlerError) {
			return map[string]any{
				"listen":                  "0.0.0.0:9000",
				"port":                    9000,
				"configured_listen":       "0.0.0.0:9000",
				"effective_repair_listen": "127.0.0.1:9000",
				"parse_error":             "",
				"safe_mode":               true,
				"config_version":          2,
				"config_revision":         7,
				"revision_state":          "versioned",
			}, nil
		},
		func(body []byte) (any, *HandlerError) {
			patchCalled = true
			if !bytes.Equal(body, patchBody) {
				t.Fatalf("patch body = %q, want %q", body, patchBody)
			}
			return map[string]any{
				"listen":                  "127.0.0.1:9000",
				"port":                    9000,
				"previous_listen":         "0.0.0.0:9000",
				"configured_listen":       "127.0.0.1:9000",
				"effective_repair_listen": "127.0.0.1:9000",
				"parse_error":             "",
				"safe_mode":               false,
				"config_version":          2,
				"config_revision":         8,
				"revision_state":          "versioned",
			}, nil
		},
	)

	getReq := signedSafeModeRequest(t, auth, http.MethodGet, "/v1/config/listener", "safe-listener-get", nil)
	getRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET listener repair = %d %q, want 200", getRec.Code, getRec.Body.String())
	}
	requireVersionedSignedResponse(t, auth, getRec, "safe-listener-get")
	getPayload := decodeJSONMap(t, getRec)
	if getPayload["listen"] != "0.0.0.0:9000" || getPayload["effective_repair_listen"] != "127.0.0.1:9000" || getPayload["revision_state"] != "versioned" {
		t.Fatalf("GET listener repair payload = %#v", getPayload)
	}

	patchReq := signedSafeModeRequest(t, auth, http.MethodPatch, "/v1/config/listener", "safe-listener-patch", patchBody)
	patchRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("PATCH listener repair = %d %q, want 200", patchRec.Code, patchRec.Body.String())
	}
	requireVersionedSignedResponse(t, auth, patchRec, "safe-listener-patch")
	if !patchCalled {
		t.Fatal("PATCH listener repair handler was not called")
	}
	patchPayload := decodeJSONMap(t, patchRec)
	if patchPayload["listen"] != "127.0.0.1:9000" || patchPayload["previous_listen"] != "0.0.0.0:9000" || patchPayload["config_revision"] != float64(8) {
		t.Fatalf("PATCH listener repair payload = %#v", patchPayload)
	}
}

func TestSafeModeListenerRepairHandlerErrorsAreSigned(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil)
	setFixedServerTime(srv)
	srv.SetSafeMode(true)
	srv.SetListenerRepair(
		func() (any, *HandlerError) {
			return nil, &HandlerError{Status: http.StatusConflict, Code: "config_revision_conflict"}
		},
		func([]byte) (any, *HandlerError) {
			return nil, &HandlerError{Status: http.StatusBadRequest, Code: "unknown_field"}
		},
	)

	getReq := signedSafeModeRequest(t, auth, http.MethodGet, "/v1/config/listener", "safe-listener-get-error", nil)
	getRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getRec, getReq)
	requireSignedSafeModeError(t, auth, getRec, "safe-listener-get-error", http.StatusConflict, "config_revision_conflict")

	patchReq := signedSafeModeRequest(t, auth, http.MethodPatch, "/v1/config/listener", "safe-listener-patch-error", []byte(`{"shared_key":"secret"}`))
	patchRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(patchRec, patchReq)
	requireSignedSafeModeError(t, auth, patchRec, "safe-listener-patch-error", http.StatusBadRequest, "unknown_field")
}

func TestSafeModeSignedEndpointsRequireHKDFAuthVersion(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil)
	setFixedServerTime(srv)
	srv.SetSafeMode(true)
	srv.SetVersionFunc(func() any { return map[string]string{"version": "v1.2.3"} })

	for i, target := range []string{"/v1/version", "/v1/status", "/v1/peers", "/v1/ssh/logs", "/v1/config/listener"} {
		req := signedRequestWithNonce(t, auth, http.MethodGet, target, "raw-safe-endpoint-"+string(rune('a'+i)), nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("raw safe-mode %s status = %d body=%q, want 401", target, rec.Code, rec.Body.String())
		}
	}

	cases := []struct {
		name string
		req  *http.Request
	}{
		{
			name: "missing auth version",
			req:  signedRequestWithNonce(t, auth, http.MethodGet, "/v1/status", "missing-auth-version", nil),
		},
		{
			name: "old raw key signature with version header",
			req: func() *http.Request {
				req := signedRequestWithNonce(t, auth, http.MethodGet, "/v1/status", "raw-key-with-version", nil)
				req.Header.Set(HeaderAuthVersion, AuthVersionRequestHMAC)
				return req
			}(),
		},
		{
			name: "malformed signature",
			req: func() *http.Request {
				req := signedSafeModeRequest(t, auth, http.MethodGet, "/v1/status", "malformed-safe-signature", nil)
				req.Header.Set(HeaderSignature, "not-hex")
				return req
			}(),
		},
		{
			name: "stale timestamp",
			req:  signedSafeModeRequestWithTimestamp(t, auth, http.MethodGet, "/v1/status", "1780250000", "stale-safe-signature", nil),
		},
		{
			name: "wrong derived key context",
			req:  signedSafeModeRequestWithWrongContext(t, auth, http.MethodGet, "/v1/status", "wrong-context", nil),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, tc.req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d body=%q, want 401", rec.Code, rec.Body.String())
			}
		})
	}
}

func requireSafeModeError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body=%q", rec.Code, status, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"`+code+`"`) {
		t.Fatalf("body = %q, want code %s", rec.Body.String(), code)
	}
}

func assertMinimalSafeModeStatus(t *testing.T, payload map[string]any) {
	t.Helper()
	if payload["status"] != "safe_mode_signed_repair" ||
		payload["listener_repair_status"] != "needs_repair" ||
		payload["last_failure_phase"] != "listener_safe_mode" ||
		payload["safe_mode_logs_available"] != true ||
		payload["hostname"] != "m4" ||
		payload["configured_listen"] != "0.0.0.0:9000" ||
		payload["effective_repair_listen"] != "127.0.0.1:9000" ||
		payload["parse_error"] != "invalid_listen_port" ||
		payload["safe_mode"] != true ||
		payload["safe_mode_schema"] != "safe_mode_v1" ||
		payload["peer_sync_started"] != false ||
		payload["config_version"] != float64(2) ||
		payload["config_revision"] != float64(9) {
		t.Fatalf("safe-mode status payload = %#v", payload)
	}
	for _, forbidden := range []string{"ssh", "transport_health", "ssh_peers", "runtime_health", "peer_health", "remote_shared_key", "shared_key"} {
		if _, ok := payload[forbidden]; ok {
			t.Fatalf("safe-mode payload exposed forbidden field %s: %#v", forbidden, payload)
		}
	}
	suggestions := payload["peer_setup_suggestions"].([]any)
	if len(suggestions) != 1 {
		t.Fatalf("peer setup suggestions = %#v, want one", suggestions)
	}
	suggestion := suggestions[0].(map[string]any)
	if suggestion["hostname"] != "old-box" || suggestion["source"] != "static_peers" || suggestion["status"] != "ssh_setup_required" {
		t.Fatalf("peer setup suggestion = %#v", suggestion)
	}
	logIDs := payload["log_ids"].([]any)
	if len(logIDs) < 2 {
		t.Fatalf("log_ids = %#v, want listener and peer setup IDs", logIDs)
	}
}

func decodeJSONMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode JSON %q: %v", rec.Body.String(), err)
	}
	return payload
}

func requireSignedSafeModeError(t *testing.T, auth *Auth, rec *httptest.ResponseRecorder, nonce string, status int, code string) {
	t.Helper()
	requireSafeModeError(t, rec, status, code)
	requireVersionedSignedResponse(t, auth, rec, nonce)
}

func requireVersionedSignedResponse(t *testing.T, auth *Auth, rec *httptest.ResponseRecorder, requestNonce string) {
	t.Helper()
	if got := rec.Header().Get(HeaderAuthVersion); got != AuthVersionRequestHMAC {
		t.Fatalf("response auth version = %q, want %q", got, AuthVersionRequestHMAC)
	}
	sig := rec.Header().Get("X-Clipfan-Response-Sig")
	if sig == "" {
		t.Fatal("missing X-Clipfan-Response-Sig")
	}
	if err := auth.VerifyResponseWithAuthVersion(requestNonce, rec.Body.Bytes(), sig, AuthVersionRequestHMAC); err != nil {
		t.Fatalf("versioned response signature: %v", err)
	}
}

func signedSafeModeRequest(t *testing.T, auth *Auth, method, target, nonce string, body []byte) *http.Request {
	t.Helper()
	return signedSafeModeRequestWithTimestamp(t, auth, method, target, "1780257600", nonce, body)
}

func signedSafeModeRequestWithTimestamp(t *testing.T, auth *Auth, method, target, ts, nonce string, body []byte) *http.Request {
	t.Helper()
	return signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, method, target, ts, nonce, body, AuthVersionRequestHMAC)
}

func signedSafeModeRequestWithWrongContext(t *testing.T, auth *Auth, method, target, nonce string, body []byte) *http.Request {
	t.Helper()
	ts := "1780257600"
	req := httptest.NewRequest(method, target, strings.NewReader(string(body)))
	req.RemoteAddr = "127.0.0.1:1234"
	wrongKey, err := DeriveKey(auth.key, sshHelloHMACLabel)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	mac := hmac.New(sha256.New, wrongKey)
	writeCanonicalRequestWithAuthVersion(mac, method, target, ts, nonce, body, AuthVersionRequestHMAC)
	req.Header.Set(HeaderAuthVersion, AuthVersionRequestHMAC)
	req.Header.Set(HeaderTimestamp, ts)
	req.Header.Set(HeaderNonce, nonce)
	req.Header.Set(HeaderSignature, hex.EncodeToString(mac.Sum(nil)))
	return req
}
