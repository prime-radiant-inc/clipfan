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
)

var ErrLoopbackRequired = errors.New("loopback_required")

type Client struct {
	http *http.Client
	auth *Auth
}

func NewClient(auth *Auth) *Client {
	return &Client{
		http: &http.Client{
			Timeout: 5 * time.Second,
			// Never follow redirects: the request carries nonce-bound signed headers,
			// and Go's redirect handling would forward them to the redirect target. The
			// daemon never redirects, so returning the 3xx as-is only ever surfaces a
			// misbehaving/forged responder as a non-2xx error.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		auth: auth,
	}
}

func (c *Client) Current(ctx context.Context, host string, port int) (CurrentPayload, error) {
	if !isLoopbackHost(host) {
		return CurrentPayload{}, fmt.Errorf("%w: %s", ErrLoopbackRequired, net.JoinHostPort(host, strconv.Itoa(port)))
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

func (c *Client) ApplyCurrent(ctx context.Context, host string, port int, content clipboard.Content, origin string) error {
	if !isLoopbackHost(host) {
		return fmt.Errorf("%w: %s", ErrLoopbackRequired, net.JoinHostPort(host, strconv.Itoa(port)))
	}
	target := "/v1/current"
	body, err := json.Marshal(CurrentPayloadFromContent(content, origin))
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s%s", net.JoinHostPort(host, strconv.Itoa(port)), target)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	headers, err := c.auth.SignedRequestHeaders(req.Method, req.URL.RequestURI(), body, SignedRequestOptions{
		AuthVersion: AuthVersionRequestHMAC,
	})
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
	responseBody, err := readLimitedBody(resp.Body, maxPeersResponseBytes, ErrSSHStreamFrameTooLarge)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("current apply to %s: status %d", host, resp.StatusCode)
	}
	authVersion := resp.Header.Get(HeaderAuthVersion)
	if authVersion == "" {
		authVersion = headers[HeaderAuthVersion]
	}
	return c.auth.VerifyResponseWithAuthVersion(headers[HeaderNonce], responseBody, resp.Header.Get("X-Clipfan-Response-Sig"), authVersion)
}

func readLimitedCurrentBody(reader io.Reader) ([]byte, error) {
	return readLimitedBody(reader, MaxSSHStreamFrameBytes, ErrSSHStreamFrameTooLarge)
}

const maxPeersResponseBytes = 1 << 20

// Peers performs a signed loopback GET of /v1/peers and returns the verified
// response body for the caller to decode. The /v1/peers payload type lives above
// transport (it carries daemon.PeerState), so this returns raw bytes rather than a
// typed value; the SSH gateway's fleet-snapshot handler decodes it.
func (c *Client) Peers(ctx context.Context, host string, port int) ([]byte, error) {
	return c.signedLoopbackGet(ctx, host, port, "/v1/peers", maxPeersResponseBytes)
}

// signedLoopbackGet issues a signed GET to a loopback daemon endpoint and returns
// the response body after verifying its signature. It mirrors Current's request
// signing but is generic over the target so read-only endpoints can be fetched
// without a bespoke typed method each.
func (c *Client) signedLoopbackGet(ctx context.Context, host string, port int, target string, maxBytes int64) ([]byte, error) {
	if !isLoopbackHost(host) {
		return nil, fmt.Errorf("%w: %s", ErrLoopbackRequired, net.JoinHostPort(host, strconv.Itoa(port)))
	}
	url := fmt.Sprintf("http://%s%s", net.JoinHostPort(host, strconv.Itoa(port)), target)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	headers, err := c.auth.SignedRequestHeaders(req.Method, req.URL.RequestURI(), nil, SignedRequestOptions{
		AuthVersion: AuthVersionRequestHMAC,
	})
	if err != nil {
		return nil, err
	}
	for header, value := range headers {
		req.Header.Set(header, value)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := readLimitedBody(resp.Body, maxBytes, ErrSSHStreamFrameTooLarge)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%s from %s: status %d", target, host, resp.StatusCode)
	}
	authVersion := resp.Header.Get(HeaderAuthVersion)
	if authVersion == "" {
		authVersion = headers[HeaderAuthVersion]
	}
	if err := c.auth.VerifyResponseWithAuthVersion(headers[HeaderNonce], body, resp.Header.Get("X-Clipfan-Response-Sig"), authVersion); err != nil {
		return nil, err
	}
	return body, nil
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
