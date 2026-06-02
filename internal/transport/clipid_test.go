package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestPushCarriesIDToReceiver(t *testing.T) {
	auth := testAuth(t)
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

func TestPushRejectsOffHostPeerHTTPWhenRuntimeDisabled(t *testing.T) {
	auth := testAuth(t)
	cl := NewClientWithPeerHTTPRuntimeDisabled(auth, "m4", true)
	cl.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("PushAs attempted to dial off-host peer HTTP with runtime disabled")
		return nil, nil
	})
	content := clipboard.Content{Kind: clipboard.KindText, Bytes: []byte("hello"), ID: "clip-disabled"}

	err := cl.PushAs(context.Background(), "peer-host", 7853, content, "m4")
	if !errors.Is(err, ErrPeerHTTPRuntimeDisabled) {
		t.Fatalf("PushAs error = %v, want ErrPeerHTTPRuntimeDisabled", err)
	}
}

func TestPushAllowsLoopbackWhenPeerHTTPRuntimeDisabled(t *testing.T) {
	auth := testAuth(t)
	var got clipboard.Content
	srv := NewServer("", auth, func(c clipboard.Content, origin string) { got = c }, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	_, port := splitHostPort(t, ts.URL)
	cl := NewClientWithPeerHTTPRuntimeDisabled(auth, "m4", true)
	content := clipboard.Content{Kind: clipboard.KindText, Bytes: []byte("hello"), ID: "clip-loopback"}
	if err := cl.PushAs(context.Background(), "127.0.0.1", port, content, "m4"); err != nil {
		t.Fatal(err)
	}
	if got.ID != "clip-loopback" {
		t.Fatalf("receiver Content.ID = %q, want clip-loopback", got.ID)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestPushCarriesConcealedToReceiver(t *testing.T) {
	auth := testAuth(t)
	var got clipboard.Content
	recv := func(c clipboard.Content, origin string) { got = c }
	srv := NewServer("", auth, recv, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	host, port := splitHostPort(t, ts.URL)
	cl := NewClient(auth, "m4")
	content := clipboard.New(clipboard.KindText, []byte("secret"), time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC))
	content.ID = "clip-concealed"
	content.Concealed = true
	if err := cl.PushAs(context.Background(), host, port, content, "m4"); err != nil {
		t.Fatal(err)
	}
	if !got.Concealed {
		t.Fatal("receiver lost concealed metadata")
	}
}

func TestPushBindsEnvelopeToRecipientHost(t *testing.T) {
	auth := testAuth(t)
	called := false
	recv := func(c clipboard.Content, origin string) { called = true }
	srv := NewServer("", auth, recv, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	host, port := splitHostPort(t, ts.URL)
	srv.SetRecipientIdentity(host)
	cl := NewClient(auth, "m4")
	content := clipboard.Content{Kind: clipboard.KindText, Bytes: []byte("hello"), ID: "clip-recipient"}
	if err := cl.PushAs(context.Background(), host, port, content, "m4"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("receiver was not called for matching target host")
	}
}

func TestServerRejectsClipForDifferentRecipient(t *testing.T) {
	auth := testAuth(t)
	called := false
	srv := NewServer("", auth, func(clipboard.Content, string) { called = true }, nil)
	srv.SetRecipientIdentity("paradise-park")
	setFixedServerTime(srv)

	body := signedClipEnvelope(t, auth, "m4", "magic-kingdom")
	req := signedRequestWithNonce(t, auth, "POST", "/v1/clip", "nonce-1", body)
	res := httptest.NewRecorder()

	srv.Handler().ServeHTTP(res, req)

	if res.Code != 403 {
		t.Fatalf("status = %d, want 403", res.Code)
	}
	if called {
		t.Fatal("receiver was called for wrong recipient")
	}
}

func TestServerAcceptsClipForMatchingRecipientShortName(t *testing.T) {
	auth := testAuth(t)
	called := false
	srv := NewServer("", auth, func(clipboard.Content, string) { called = true }, nil)
	srv.SetRecipientIdentity("paradise-park")
	setFixedServerTime(srv)

	body := signedClipEnvelope(t, auth, "m4", "paradise-park.local")
	req := signedRequestWithNonce(t, auth, "POST", "/v1/clip", "nonce-1", body)
	res := httptest.NewRecorder()

	srv.Handler().ServeHTTP(res, req)

	if res.Code != 204 {
		t.Fatalf("status = %d, want 204 body %q", res.Code, res.Body.String())
	}
	if !called {
		t.Fatal("receiver was not called for matching recipient short name")
	}
}

func TestServerRejectsSuffixCollisionRecipient(t *testing.T) {
	auth := testAuth(t)
	called := false
	srv := NewServer("", auth, func(clipboard.Content, string) { called = true }, nil)
	srv.SetRecipientIdentity("server")
	setFixedServerTime(srv)

	body := signedClipEnvelope(t, auth, "m4", "prod-server")
	req := signedRequestWithNonce(t, auth, "POST", "/v1/clip", "nonce-1", body)
	res := httptest.NewRecorder()

	srv.Handler().ServeHTTP(res, req)

	if res.Code != 403 {
		t.Fatalf("status = %d, want 403", res.Code)
	}
	if called {
		t.Fatal("receiver was called for suffix-colliding recipient")
	}
}

func signedClipEnvelope(t *testing.T, auth *Auth, origin, recipient string) []byte {
	t.Helper()
	body, nonce, err := auth.SealBody([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	env := Envelope{
		ID:        "clip-recipient",
		Origin:    origin,
		Recipient: recipient,
		TS:        time.Unix(1780257600, 0),
		Kind:      string(clipboard.KindText),
		Body:      body,
		Nonce:     nonce,
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return raw
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
