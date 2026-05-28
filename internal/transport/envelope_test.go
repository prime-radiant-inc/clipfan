package transport

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	src := []byte("hello, world")
	env := Envelope{
		Origin: "macbook",
		TS:     time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC),
		Kind:   "text",
		SHA256: "abc",
		Body:   EncodeBody(src),
	}
	out, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var got Envelope
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	body, err := got.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello, world" {
		t.Fatalf("body mismatch: %q", body)
	}
	if got.Origin != "macbook" || got.Kind != "text" {
		t.Fatalf("envelope mismatch: %+v", got)
	}
}
