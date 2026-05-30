package transport

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
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
	in := Envelope{Origin: "m4", Kind: "text", ID: "abc123", Body: EncodeBody([]byte("hi"))}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"id":"abc123"`) {
		t.Fatalf("marshaled envelope missing id: %s", raw)
	}
	var out Envelope
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != "abc123" {
		t.Fatalf("round-trip ID = %q, want abc123", out.ID)
	}
}

func TestPushCarriesIDToReceiver(t *testing.T) {
	auth, err := NewAuth("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	var got clipboard.Content
	recv := func(c clipboard.Content, origin string) { got = c }
	srv := NewServer("", auth, recv, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	host, port := splitHostPort(t, ts.URL)
	cl := NewClient(auth, "m4")
	content := clipboard.Content{Kind: clipboard.KindText, Bytes: []byte("hello"), ID: "clip-xyz"}
	if err := cl.PushAs(context.Background(), host, port, content, "m4"); err != nil {
		t.Fatal(err)
	}
	if got.ID != "clip-xyz" {
		t.Fatalf("receiver Content.ID = %q, want clip-xyz", got.ID)
	}
}

func splitHostPort(t *testing.T, rawURL string) (string, int) {
	t.Helper()
	hp := strings.TrimPrefix(rawURL, "http://")
	host, portStr, found := strings.Cut(hp, ":")
	if !found {
		t.Fatalf("no port in %s", rawURL)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("bad port %q: %v", portStr, err)
	}
	return host, port
}
