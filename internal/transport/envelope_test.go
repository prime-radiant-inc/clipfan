package transport

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	auth := testAuth(t)
	src := []byte("hello, world")
	body, nonce, err := auth.SealBody(src)
	if err != nil {
		t.Fatal(err)
	}
	env := Envelope{
		Origin: "macbook",
		TS:     time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC),
		Kind:   "text",
		Body:   body,
		Nonce:  nonce,
	}
	out, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var got Envelope
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	bodyBytes, err := got.Bytes(auth)
	if err != nil {
		t.Fatal(err)
	}
	if string(bodyBytes) != "hello, world" {
		t.Fatalf("body mismatch: %q", bodyBytes)
	}
	if got.Origin != "macbook" || got.Kind != "text" {
		t.Fatalf("envelope mismatch: %+v", got)
	}
}

func TestEnvelopeEncryptsBodyAndDecryptsWithSameKey(t *testing.T) {
	auth := testAuth(t)
	plain := []byte("secret clipboard text")
	body, nonce, err := auth.SealBody(plain)
	if err != nil {
		t.Fatal(err)
	}
	if body == EncodeBody(plain) {
		t.Fatal("encrypted body must not equal plaintext base64")
	}
	env := Envelope{Body: body, Nonce: nonce}
	got, err := env.Bytes(auth)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(plain) {
		t.Fatalf("decrypted body = %q, want %q", got, plain)
	}
}

func TestEnvelopeRejectsWrongKey(t *testing.T) {
	auth := testAuth(t)
	wrong, err := NewAuth("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	body, nonce, err := auth.SealBody([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	env := Envelope{Body: body, Nonce: nonce}
	if _, err := env.Bytes(wrong); err == nil {
		t.Fatal("wrong key must not decrypt envelope body")
	}
}
