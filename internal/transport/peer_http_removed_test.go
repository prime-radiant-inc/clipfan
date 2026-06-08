package transport

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

func TestPeerHTTPClipRouteIsRemoved(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	auth, err := NewAuth(key)
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer("", auth, nil)
	srv.now = func() time.Time { return time.Unix(1780257600, 0) }

	content := clipboard.Content{
		Kind:  clipboard.KindText,
		Bytes: []byte("peer-http-is-gone"),
		ID:    "clip-peer-http-removed",
		TS:    time.Unix(1780257600, 0),
	}
	env, err := BuildEnvelope(auth, content, "node-a", "node-b")
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/clip", bytes.NewReader(body))
	headers, err := auth.SignedRequestHeaders(http.MethodPost, "/v1/clip", body, SignedRequestOptions{
		Timestamp: time.Unix(1780257600, 0),
		Nonce:     "peer-http-removed",
	})
	if err != nil {
		t.Fatal(err)
	}
	for header, value := range headers {
		req.Header.Set(header, value)
	}

	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusNotFound {
		t.Fatalf("POST /v1/clip status = %d, want 404", res.Code)
	}
}
