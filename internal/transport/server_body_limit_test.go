package transport

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadSignedRejectsOversizedBodyWith413(t *testing.T) {
	auth := testAuth(t)
	srv := NewServer(":0", auth, func() any { return nil })
	setFixedServerTime(srv)

	body := bytes.Repeat([]byte("x"), 16)
	req := signedRequestWithTimestampAndNonceAndAuthVersion(t, auth, http.MethodPost, "/v1/limit-test", "1780257600", "limit-test", body, AuthVersionRequestHMAC)
	rec := httptest.NewRecorder()
	if signed := srv.readSignedWithRequiredAuthVersion(rec, req, 8, AuthVersionRequestHMAC); signed != nil {
		t.Fatal("oversized body returned a signed payload")
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d, want 413", rec.Code)
	}
}
