package transport

import (
	"bytes"
	"context"
	"encoding/hex"
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
	env := Envelope{
		Origin: c.origin,
		TS:     content.TS,
		Kind:   string(content.Kind),
		SHA256: hex.EncodeToString(content.Hash[:]),
		Body:   EncodeBody(content.Bytes),
	}
	body, err := json.Marshal(env)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s/v1/clip", net.JoinHostPort(host, strconv.Itoa(port)))
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Clipfan-Sig", c.auth.Sign(body))
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
