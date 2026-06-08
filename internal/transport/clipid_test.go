package transport

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewClipID(t *testing.T) {
	a := NewClipID()
	if len(a) != 32 {
		t.Fatalf("NewClipID len = %d, want 32 hex chars", len(a))
	}
	if a == NewClipID() {
		t.Fatal("two NewClipID() calls returned the same value")
	}
}

func TestEnvelopeIDRoundTrip(t *testing.T) {
	auth := testAuth(t)
	body, nonce, err := auth.SealBody([]byte("hi"))
	if err != nil {
		t.Fatal(err)
	}
	in := Envelope{Origin: "m4", Recipient: "paradise-park", Kind: "text", ID: "abc123", Body: body, Nonce: nonce}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"id":"abc123"`) {
		t.Fatalf("marshaled envelope missing id: %s", raw)
	}
	if !strings.Contains(string(raw), `"recipient":"paradise-park"`) {
		t.Fatalf("marshaled envelope missing recipient: %s", raw)
	}
	var out Envelope
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != "abc123" {
		t.Fatalf("round-trip ID = %q, want abc123", out.ID)
	}
	if out.Recipient != "paradise-park" {
		t.Fatalf("round-trip recipient = %q, want paradise-park", out.Recipient)
	}
}
