package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
)

type Client struct {
	http   *http.Client
	auth   *Auth
	origin string
}

func NewClient(auth *Auth, origin string) *Client {
	return &Client{
		http:   &http.Client{Timeout: 5 * time.Second},
		auth:   auth,
		origin: origin,
	}
}

func (c *Client) Push(ctx context.Context, host string, port int, content clipboard.Content) error {
	return c.PushAs(ctx, host, port, content, c.origin)
}

// PushAs sends content but stamps the envelope with `origin` instead of the
// client's own origin. Used by the relay path so the original copy source is
// preserved end-to-end and receivers can short-circuit if they're the origin.
func (c *Client) PushAs(ctx context.Context, host string, port int, content clipboard.Content, origin string) error {
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
	requestNonce := NewClipID()
	if requestNonce == "" {
		return fmt.Errorf("generate request nonce")
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("X-Clipfan-Ts", ts)
	req.Header.Set("X-Clipfan-Nonce", requestNonce)
	req.Header.Set("X-Clipfan-Sig", c.auth.SignRequest(req.Method, req.URL.RequestURI(), ts, requestNonce, raw))
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
