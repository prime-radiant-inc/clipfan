package transport

import (
	"encoding/base64"
	"testing"
)

func TestAuthRoundTrip(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("a-32-byte-test-secret-for-hmac-x"))
	a, err := NewAuth(key)
	if err != nil {
		t.Fatalf("NewAuth: %v", err)
	}
	body := []byte(`{"hello":"world"}`)
	sig := a.Sign(body)
	if err := a.Verify(body, sig); err != nil {
		t.Fatalf("Verify good sig: %v", err)
	}
	if err := a.Verify([]byte("tampered"), sig); err == nil {
		t.Fatal("expected error on tampered body")
	}
	if err := a.Verify(body, "deadbeef"); err == nil {
		t.Fatal("expected error on wrong sig length")
	}
}

func TestAuthShortKeyRejected(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("tooshort"))
	if _, err := NewAuth(key); err == nil {
		t.Fatal("expected short-key rejection")
	}
}
