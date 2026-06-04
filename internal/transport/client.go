package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	return c.PushAsToRecipient(ctx, host, port, host, content, origin)
}

func (c *Client) PushAsToRecipient(ctx context.Context, host string, port int, recipient string, content clipboard.Content, origin string) error {
	if c.peerHTTPRuntimeDisabled && !isLoopbackHost(host) {
		return fmt.Errorf("%w: %s", ErrPeerHTTPRuntimeDisabled, net.JoinHostPort(host, strconv.Itoa(port)))
	}
	env, err := BuildEnvelope(c.auth, content, origin, recipient)
	if err != nil {
		return err
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

func (c *Client) Current(ctx context.Context, host string, port int) (CurrentPayload, error) {
	if !isLoopbackHost(host) {
		return CurrentPayload{}, fmt.Errorf("%w: %s", ErrPeerHTTPRuntimeDisabled, net.JoinHostPort(host, strconv.Itoa(port)))
	}
	target := "/v1/current"
	url := fmt.Sprintf("http://%s%s", net.JoinHostPort(host, strconv.Itoa(port)), target)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return CurrentPayload{}, err
	}
	headers, err := c.auth.SignedRequestHeaders(req.Method, req.URL.RequestURI(), nil, SignedRequestOptions{
		AuthVersion: AuthVersionRequestHMAC,
	})
	if err != nil {
		return CurrentPayload{}, err
	}
	for header, value := range headers {
		req.Header.Set(header, value)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return CurrentPayload{}, err
	}
	defer resp.Body.Close()
	body, err := readLimitedCurrentBody(resp.Body)
	if err != nil {
		return CurrentPayload{}, err
	}
	if resp.StatusCode/100 != 2 {
		return CurrentPayload{}, fmt.Errorf("current from %s: status %d", host, resp.StatusCode)
	}
	authVersion := resp.Header.Get(HeaderAuthVersion)
	if authVersion == "" {
		authVersion = headers[HeaderAuthVersion]
	}
	if err := c.auth.VerifyResponseWithAuthVersion(headers[HeaderNonce], body, resp.Header.Get("X-Clipfan-Response-Sig"), authVersion); err != nil {
		return CurrentPayload{}, err
	}
	var payload CurrentPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return CurrentPayload{}, err
	}
	return payload, nil
}

func readLimitedCurrentBody(reader io.Reader) ([]byte, error) {
	return readLimitedBody(reader, MaxSSHStreamFrameBytes, ErrSSHStreamFrameTooLarge)
}

func readLimitedBody(reader io.Reader, maxBytes int64, tooLarge error) ([]byte, error) {
	limited := io.LimitReader(reader, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, tooLarge
	}
	return body, nil
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
