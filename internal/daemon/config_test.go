package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
	"github.com/prime-radiant-inc/clipfan/internal/transport"
)

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func TestSetMaxHistoryClampsAndRejects(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	d, _, _ := newTestDaemon(t)

	if err := d.setMaxHistory(0); err == nil {
		t.Fatal("setMaxHistory(0) should error")
	}
	if err := d.setMaxHistory(10); err != nil {
		t.Fatal(err)
	}
	if got := readSavedMax(t); got != 50 {
		t.Fatalf("saved max = %d, want clamped 50", got)
	}
	if err := d.setMaxHistory(99999); err != nil {
		t.Fatal(err)
	}
	if got := readSavedMax(t); got != 5000 {
		t.Fatalf("saved max = %d, want clamped 5000", got)
	}
	if err := d.setMaxHistory(300); err != nil {
		t.Fatal(err)
	}
	if got := readSavedMax(t); got != 300 {
		t.Fatalf("saved max = %d, want 300", got)
	}
}

func TestPeersHandlerIncludesMaxHistory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, _, _ := newTestDaemon(t)
	out := d.peersHandler().(map[string]any)
	if _, ok := out["max_history"]; !ok {
		t.Fatal("peers response missing max_history")
	}
}

func TestPeersHandlerIncludesCurrentConfigRevision(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	body := []byte(`{"config_version":2,"config_revision":17,"shared_key":"` + config.NewSharedKey() + `","hostname":"m4","listen":"127.0.0.1:7853","max_history":50}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	d, _, _ := newTestDaemon(t)
	d.configPath = path

	out := d.peersHandler().(map[string]any)

	if out["revision_state"] != config.RevisionStateVersioned {
		t.Fatalf("revision_state = %#v, want versioned", out["revision_state"])
	}
	if got, ok := out["config_revision"].(*uint64); !ok || got == nil || *got != 17 {
		t.Fatalf("config_revision = %#v, want *17", out["config_revision"])
	}
	if got, ok := out["config_version"].(*int); !ok || got == nil || *got != 2 {
		t.Fatalf("config_version = %#v, want *2", out["config_version"])
	}
}

func TestSetMaxHistoryRejectsConfigV2WhenWritesDisabled(t *testing.T) {
	if releaseflags.ConfigV2WriteEnabled {
		t.Skip("requires generated ConfigV2WriteEnabled=false")
	}
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := filepath.Join(root, "clipfan")
	path := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	before := []byte(`{"config_version":2,"config_revision":1,"shared_key":"` + config.NewSharedKey() + `","max_history":50,"future":{"keep":true}}`)
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}

	d, _, _ := newTestDaemon(t)
	err := d.setMaxHistory(300)
	if !errors.Is(err, config.ErrConfigV2WritesDisabled) {
		t.Fatalf("setMaxHistory error = %v, want ErrConfigV2WritesDisabled", err)
	}
	if !strings.Contains(err.Error(), "config_v2_writes_disabled") {
		t.Fatalf("setMaxHistory error = %v, want stable code", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("v2 config changed\nbefore: %s\nafter: %s", before, after)
	}
}

func TestPostConfigRejectsConfigV2WithSignedFailureWhenWritesDisabled(t *testing.T) {
	if releaseflags.ConfigV2WriteEnabled {
		t.Skip("requires generated ConfigV2WriteEnabled=false")
	}
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := filepath.Join(root, "clipfan")
	path := filepath.Join(dir, "config.json")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sharedKey := config.NewSharedKey()
	before := []byte(`{"config_version":2,"config_revision":1,"shared_key":"` + sharedKey + `","max_history":50,"future":{"keep":true}}`)
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := transport.NewAuth(sharedKey)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"max_history":300}`)
	nonce := "config-v2-disabled"
	req := signedDaemonRequestWithAuthVersion(t, auth, http.MethodPost, "/v1/config", nonce, body, transport.AuthVersionRequestHMAC)
	rec := httptest.NewRecorder()

	d.sv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	responseBody := rec.Body.Bytes()
	if !strings.Contains(string(responseBody), "config_v2_writes_disabled") {
		t.Fatalf("response body = %q, want stable code", string(responseBody))
	}
	if sig := rec.Header().Get("X-Clipfan-Response-Sig"); sig == "" {
		t.Fatal("missing signed response header")
	} else if err := auth.VerifyResponseWithAuthVersion(nonce, responseBody, sig, transport.AuthVersionRequestHMAC); err != nil {
		t.Fatalf("bad response signature: %v", err)
	}
	if d.cfg.MaxHistory != 50 {
		t.Fatalf("daemon cfg MaxHistory = %d, want unchanged 50", d.cfg.MaxHistory)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("v2 config changed\nbefore: %s\nafter: %s", before, after)
	}
}

func readSavedMax(t *testing.T) int {
	t.Helper()
	p := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "clipfan", "config.json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		MaxHistory int `json:"max_history"`
	}
	if err := jsonUnmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	return c.MaxHistory
}

func signedDaemonRequest(t *testing.T, auth *transport.Auth, method, target, nonce string, body []byte) *http.Request {
	return signedDaemonRequestWithAuthVersion(t, auth, method, target, nonce, body, "")
}

func signedDaemonRequestWithAuthVersion(t *testing.T, auth *transport.Auth, method, target, nonce string, body []byte, authVersion string) *http.Request {
	t.Helper()
	headers, err := auth.SignedRequestHeaders(method, target, body, transport.SignedRequestOptions{
		Timestamp:   time.Now(),
		Nonce:       nonce,
		AuthVersion: authVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	return req
}
