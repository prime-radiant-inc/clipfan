package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
)

var ErrPeerHTTPRuntimeDisabled = errors.New("peer_http_runtime_disabled")

type Client struct {
	http                    *http.Client
	auth                    *Auth
	origin                  string
	peerHTTPRuntimeDisabled bool
}

func NewClient(auth *Auth, origin string) *Client {
	return NewClientWithPeerHTTPRuntimeDisabled(auth, origin, releaseflags.PeerHTTPRuntimeDisabled)
}

func NewClientWithPeerHTTPRuntimeDisabled(auth *Auth, origin string, disabled bool) *Client {
	return &Client{
		http:                    &http.Client{Timeout: 5 * time.Second},
		auth:                    auth,
		origin:                  origin,
		peerHTTPRuntimeDisabled: disabled,
	}
}

func (c *Client) Push(ctx context.Context, host string, port int, content clipboard.Content) error {
	return c.PushAs(ctx, host, port, content, c.origin)
}

// PushAs sends content but stamps the envelope with `origin` instead of the
// client's own origin. Used by the relay path so the original copy source is
// preserved end-to-end and receivers can short-circuit if they're the origin.
func (c *Client) PushAs(ctx context.Context, host string, port int, content clipboard.Content, origin string) error {
	if c.peerHTTPRuntimeDisabled && !isLoopbackHost(host) {
		return fmt.Errorf("%w: %s", ErrPeerHTTPRuntimeDisabled, net.JoinHostPort(host, strconv.Itoa(port)))
	}
	body, bodyNonce, err := c.auth.SealBody(content.Bytes)
	if err != nil {
		return err
	}
	env := Envelope{
		ID:        content.ID,
		Origin:    origin,
		Recipient: host,
		TS:        content.TS,
		Kind:      string(content.Kind),
		Body:      body,
		Nonce:     bodyNonce,
		Concealed: content.Concealed,
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s/v1/clip", net.JoinHostPort(host, strconv.Itoa(port)))
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	headers, err := c.auth.SignedRequestHeaders(req.Method, req.URL.RequestURI(), raw, SignedRequestOptions{})
	if err != nil {
		return err
	}
	for header, value := range headers {
		req.Header.Set(header, value)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("push to %s: status %d", host, resp.StatusCode)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
