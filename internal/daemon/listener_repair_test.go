package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
)

func TestSafeModeListenerRepairStatusIsSignedAndRedacted(t *testing.T) {
	sharedKey := config.NewSharedKey()
	configPath := writeListenerRepairDaemonConfig(t, `{
		"config_version": 2,
		"config_revision": 7,
		"listen": "0.0.0.0:9000",
		"port": 9000,
		"shared_key": "`+sharedKey+`",
		"static_peers": ["old-box"],
		"ssh": {"peers": [{"id": "future"}]},
		"private_key_path": "/secret/key"
	}`)
	cfg := &config.Config{
		ConfigVersion:  intPtr(2),
		ConfigRevision: uint64Ptr(7),
		Listen:         "0.0.0.0:9000",
		Port:           9000,
		SharedKey:      sharedKey,
		Discovery:      "static",
	}
	d, err := NewWithOptions(cfg, Options{ListenerBoundaryEnabled: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}
	d.configPath = configPath

	rec, nonce := serveSignedDaemonRepair(t, d, http.MethodGet, "/v1/config/listener", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/config/listener = %d %q, want 200", rec.Code, rec.Body.String())
	}
	requireDaemonSignedResponse(t, d.auth, rec, nonce)
	payload := decodeDaemonJSONMap(t, rec)
	for _, key := range []string{"listen", "port", "configured_listen", "effective_repair_listen", "parse_error", "safe_mode", "config_version", "config_revision", "revision_state"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("listener repair payload missing %s: %#v", key, payload)
		}
	}
	for _, forbidden := range []string{"shared_key", "static_peers", "ssh", "private_key_path", "known_hosts_path", "authorized_keys_path"} {
		if _, ok := payload[forbidden]; ok {
			t.Fatalf("listener repair payload exposed %s: %#v", forbidden, payload)
		}
	}
	if payload["listen"] != "0.0.0.0:9000" || payload["effective_repair_listen"] != "127.0.0.1:9000" || payload["revision_state"] != "versioned" {
		t.Fatalf("listener repair payload = %#v", payload)
	}
}

func TestSafeModeListenerRepairStatusRejectsUnsafeConfigMode(t *testing.T) {
	sharedKey := config.NewSharedKey()
	configPath := writeListenerRepairDaemonConfig(t, `{
		"config_version": 2,
		"config_revision": 7,
		"listen": "0.0.0.0:9000",
		"port": 9000,
		"shared_key": "`+sharedKey+`"
	}`)
	if err := os.Chmod(configPath, 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := NewWithOptions(&config.Config{
		ConfigVersion:  intPtr(2),
		ConfigRevision: uint64Ptr(7),
		Listen:         "0.0.0.0:9000",
		Port:           9000,
		SharedKey:      sharedKey,
		Discovery:      "static",
	}, Options{ListenerBoundaryEnabled: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}
	d.configPath = configPath

	rec, nonce := serveSignedDaemonRepair(t, d, http.MethodGet, "/v1/config/listener", nil)
	requireDaemonSignedError(t, d.auth, rec, nonce, http.StatusConflict, "config_file_unsafe")
}

func TestSafeModeListenerRepairPatchFailsClosedWhileGeneratedGateFalse(t *testing.T) {
	sharedKey := config.NewSharedKey()
	configPath := writeListenerRepairDaemonConfig(t, `{
		"listen": "0.0.0.0:9000",
		"port": 9000,
		"shared_key": "`+sharedKey+`",
		"max_history": 50
	}`)
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewWithOptions(&config.Config{
		Listen:    "0.0.0.0:9000",
		Port:      9000,
		SharedKey: sharedKey,
		Discovery: "static",
	}, Options{ListenerBoundaryEnabled: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}
	d.configPath = configPath

	body := []byte(`{"expected_config_revision":null,"expected_revision_state":"pre_v2","listen":"127.0.0.1:9000","port":9000,"previous_listen":"0.0.0.0:9000"}`)
	rec, nonce := serveSignedDaemonRepair(t, d, http.MethodPatch, "/v1/config/listener", body)
	requireDaemonSignedError(t, d.auth, rec, nonce, http.StatusServiceUnavailable, "config_v2_writes_disabled")
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("disabled listener repair changed config\nbefore=%s\nafter=%s", before, after)
	}
}

func TestSafeModeListenerRepairPatchRejectsForbiddenFields(t *testing.T) {
	sharedKey := config.NewSharedKey()
	configPath := writeListenerRepairDaemonConfig(t, `{
		"listen": "0.0.0.0:9000",
		"port": 9000,
		"shared_key": "`+sharedKey+`"
	}`)
	d, err := NewWithOptions(&config.Config{
		Listen:    "0.0.0.0:9000",
		Port:      9000,
		SharedKey: sharedKey,
		Discovery: "static",
	}, Options{ListenerBoundaryEnabled: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}
	d.configPath = configPath

	body := []byte(`{"expected_config_revision":null,"expected_revision_state":"pre_v2","listen":"127.0.0.1:9000","port":9000,"shared_key":"secret"}`)
	rec, nonce := serveSignedDaemonRepair(t, d, http.MethodPatch, "/v1/config/listener", body)
	requireDaemonSignedError(t, d.auth, rec, nonce, http.StatusBadRequest, "unknown_field")
}

func TestSafeModeListenerRepairPatchRejectsMalformedBodies(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{name: "syntax", body: []byte(`{`)},
		{name: "array", body: []byte(`[]`)},
		{name: "null", body: []byte(`null`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sharedKey := config.NewSharedKey()
			configPath := writeListenerRepairDaemonConfig(t, `{
				"listen": "0.0.0.0:9000",
				"port": 9000,
				"shared_key": "`+sharedKey+`"
			}`)
			d, err := NewWithOptions(&config.Config{
				Listen:    "0.0.0.0:9000",
				Port:      9000,
				SharedKey: sharedKey,
				Discovery: "static",
			}, Options{ListenerBoundaryEnabled: boolPtr(true)})
			if err != nil {
				t.Fatal(err)
			}
			d.configPath = configPath

			rec, nonce := serveSignedDaemonRepair(t, d, http.MethodPatch, "/v1/config/listener", tc.body)
			requireDaemonSignedError(t, d.auth, rec, nonce, http.StatusBadRequest, "bad_request")
		})
	}
}

func writeListenerRepairDaemonConfig(t *testing.T, body string) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	path := config.Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func serveSignedDaemonRepair(t *testing.T, d *Daemon, method, target string, body []byte) (*httptest.ResponseRecorder, string) {
	t.Helper()
	now := time.Now().UTC()
	headers, err := d.auth.SignedRequestHeaders(method, target, body, transport.SignedRequestOptions{
		Timestamp:   now,
		Nonce:       "daemon-listener-repair-" + strings.ToLower(method),
		AuthVersion: transport.AuthVersionRequestHMAC,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	d.sv.Handler().ServeHTTP(rec, req)
	return rec, headers[transport.HeaderNonce]
}

func requireDaemonSignedError(t *testing.T, auth *transport.Auth, rec *httptest.ResponseRecorder, nonce string, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body=%q", rec.Code, status, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"`+code+`"`) {
		t.Fatalf("body = %q, want code %s", rec.Body.String(), code)
	}
	requireDaemonSignedResponse(t, auth, rec, nonce)
}

func requireDaemonSignedResponse(t *testing.T, auth *transport.Auth, rec *httptest.ResponseRecorder, nonce string) {
	t.Helper()
	if got := rec.Header().Get(transport.HeaderAuthVersion); got != transport.AuthVersionRequestHMAC {
		t.Fatalf("response auth version = %q, want %q", got, transport.AuthVersionRequestHMAC)
	}
	sig := rec.Header().Get("X-Clipfan-Response-Sig")
	if sig == "" {
		t.Fatal("missing X-Clipfan-Response-Sig")
	}
	if err := auth.VerifyResponseWithAuthVersion(nonce, rec.Body.Bytes(), sig, transport.AuthVersionRequestHMAC); err != nil {
		t.Fatalf("response signature: %v", err)
	}
}

func decodeDaemonJSONMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode JSON %q: %v", rec.Body.String(), err)
	}
	return payload
}

func intPtr(v int) *int { return &v }

func uint64Ptr(v uint64) *uint64 { return &v }
