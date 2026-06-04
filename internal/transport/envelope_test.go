package transport

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

func TestBuildAndOpenEnvelopeRoundTrip(t *testing.T) {
	auth := testAuth(t)
	ts := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	content := clipboard.New(clipboard.KindText, []byte("hello"), ts)
	content.ID = "clip-1"

	env, err := BuildEnvelope(auth, content, "m4", "linux-b")
	if err != nil {
		t.Fatalf("BuildEnvelope error = %v", err)
	}
	if env.ID != "clip-1" || env.Origin != "m4" || env.Recipient != "linux-b" || env.Kind != "text" {
		t.Fatalf("Envelope = %#v", env)
	}
	if env.Body == "hello" || env.Nonce == "" {
		t.Fatalf("Envelope body/nonce not sealed: %#v", env)
	}

	got, origin, err := OpenEnvelope(auth, env, "linux-b", ts)
	if err != nil {
		t.Fatalf("OpenEnvelope error = %v", err)
	}
	if origin != "m4" {
		t.Fatalf("origin = %q, want m4", origin)
	}
	if got.ID != "clip-1" || got.Kind != clipboard.KindText || string(got.Bytes) != "hello" || !got.TS.Equal(ts) {
		t.Fatalf("content = %#v", got)
	}
}

func TestBuildEnvelopeEncryptsBodyAndRejectsWrongKey(t *testing.T) {
	auth := testAuth(t)
	wrong, err := NewAuth("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	content := clipboard.New(clipboard.KindText, []byte("secret clipboard text"), ts)
	content.ID = "clip-secret"

	env, err := BuildEnvelope(auth, content, "m4", "linux-b")
	if err != nil {
		t.Fatalf("BuildEnvelope error = %v", err)
	}
	if env.Body == EncodeBody(content.Bytes) {
		t.Fatal("encrypted body must not equal plaintext base64")
	}
	if _, err := env.Bytes(wrong); err == nil {
		t.Fatal("wrong key must not decrypt envelope body")
	}
}

func TestOpenEnvelopeRejectsWrongRecipientAndFutureTimestamp(t *testing.T) {
	auth := testAuth(t)
	ts := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	content := clipboard.New(clipboard.KindText, []byte("hello"), ts.Add(3*time.Minute))
	content.ID = "clip-1"
	env, err := BuildEnvelope(auth, content, "m4", "linux-b")
	if err != nil {
		t.Fatalf("BuildEnvelope error = %v", err)
	}

	_, _, err = OpenEnvelope(auth, env, "mac-a", ts)
	if !errors.Is(err, ErrWrongRecipient) {
		t.Fatalf("wrong recipient error = %v, want ErrWrongRecipient", err)
	}
	_, _, err = OpenEnvelope(auth, env, "linux-b", ts)
	if !errors.Is(err, ErrFutureEnvelopeTimestamp) {
		t.Fatalf("future timestamp error = %v, want ErrFutureEnvelopeTimestamp", err)
	}
}

func TestOpenEnvelopeRejectsMalformedEncryptedBody(t *testing.T) {
	auth := testAuth(t)
	var env Envelope
	if err := json.Unmarshal([]byte(`{"id":"clip-1","origin":"m4","recipient":"linux-b","ts":"2026-06-03T12:00:00Z","kind":"text","body":"not-base64","nonce":"not-base64"}`), &env); err != nil {
		t.Fatal(err)
	}

	_, _, err := OpenEnvelope(auth, env, "linux-b", time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC))
	if !errors.Is(err, ErrEnvelopeDecrypt) {
		t.Fatalf("decrypt error = %v, want ErrEnvelopeDecrypt", err)
	}
}
