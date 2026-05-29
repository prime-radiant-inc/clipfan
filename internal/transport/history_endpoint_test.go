package transport

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testAuth(t *testing.T) *Auth {
	t.Helper()
	// base64 of a 32-byte key.
	a, err := NewAuth("dGVzdC1rZXktMDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")
	if err != nil {
		t.Fatalf("NewAuth: %v", err)
	}
	return a
}

func TestHistoryGETNoSignature(t *testing.T) {
	called := false
	srv := NewServer(":0", testAuth(t), nil, func() any { return nil })
	srv.SetHistory(
		func(limit int) (any, error) { called = true; return []string{"x"}, nil },
		nil, nil, nil,
	)
	req := httptest.NewRequest(http.MethodGet, "/v1/history?limit=10", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !called {
		t.Fatal("history func not invoked")
	}
}

func TestRestoreRequiresSignature(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil, func() any { return nil })
	restored := ""
	srv.SetHistory(
		func(limit int) (any, error) { return nil, nil },
		func(id string) error { restored = id; return nil },
		func(id string, pinned bool) error { return nil },
		func(id string, allUnpinned bool) error { return nil },
	)

	body, _ := json.Marshal(map[string]string{"id": "abc"})
	req := httptest.NewRequest(http.MethodPost, "/v1/restore", bytes.NewReader(body))
	req.Header.Set("X-Clipfan-Sig", auth.Sign(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("signed status = %d, want 200", rec.Code)
	}
	if restored != "abc" {
		t.Fatalf("restore id = %q, want abc", restored)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/v1/restore", bytes.NewReader(body))
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned status = %d, want 401", rec2.Code)
	}
}

func TestPinAndDeleteSigned(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, nil, func() any { return nil })
	var pinnedID string
	var deletedAll bool
	srv.SetHistory(
		func(limit int) (any, error) { return nil, nil },
		func(id string) error { return nil },
		func(id string, pinned bool) error { pinnedID = id; return nil },
		func(id string, allUnpinned bool) error { deletedAll = allUnpinned; return nil },
	)

	pb, _ := json.Marshal(map[string]any{"id": "p1", "pinned": true})
	preq := httptest.NewRequest(http.MethodPost, "/v1/history/pin", bytes.NewReader(pb))
	preq.Header.Set("X-Clipfan-Sig", auth.Sign(pb))
	prec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(prec, preq)
	if prec.Code != http.StatusOK || pinnedID != "p1" {
		t.Fatalf("pin failed: code=%d id=%q", prec.Code, pinnedID)
	}

	db, _ := json.Marshal(map[string]any{"all_unpinned": true})
	dreq := httptest.NewRequest(http.MethodDelete, "/v1/history", bytes.NewReader(db))
	dreq.Header.Set("X-Clipfan-Sig", auth.Sign(db))
	drec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(drec, dreq)
	if drec.Code != http.StatusOK || !deletedAll {
		t.Fatalf("delete-all failed: code=%d all=%v", drec.Code, deletedAll)
	}
}
