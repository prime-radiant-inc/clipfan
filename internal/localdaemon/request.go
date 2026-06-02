package localdaemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/prime-radiant-inc/clipfan/internal/transport"
)

var ErrSignedEndpointRequired = errors.New("signed_endpoint_required")
var ErrInvalidRequestURI = errors.New("invalid_local_request_uri")

type SignedRequest struct {
	Request *http.Request
	Nonce   string
}

type SignedRequestOptions struct {
	AuthOptions transport.SignedRequestOptions
}

func NewSignedRequest(ctx context.Context, endpoint Endpoint, auth *transport.Auth, method, requestURI string, body []byte, opts SignedRequestOptions) (*SignedRequest, error) {
	if !isSignedPurpose(endpoint.Purpose) {
		return nil, ErrSignedEndpointRequired
	}
	if auth == nil {
		return nil, errors.New("auth required")
	}
	if err := validateLocalRequestURI(requestURI); err != nil {
		return nil, err
	}
	base, err := url.Parse(endpoint.BaseURL)
	if err != nil {
		return nil, err
	}
	target, err := base.Parse(requestURI)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	authOpts := opts.AuthOptions
	authOpts.AuthVersion = transport.AuthVersionRequestHMAC
	headers, err := auth.SignedRequestHeaders(method, requestURI, body, authOpts)
	if err != nil {
		return nil, err
	}
	for header, value := range headers {
		req.Header.Set(header, value)
	}
	nonce := headers[transport.HeaderNonce]
	if nonce == "" {
		return nil, fmt.Errorf("missing request nonce")
	}
	return &SignedRequest{Request: req, Nonce: nonce}, nil
}

func validateLocalRequestURI(requestURI string) error {
	if requestURI == "" || !strings.HasPrefix(requestURI, "/") || strings.HasPrefix(requestURI, "//") {
		return ErrInvalidRequestURI
	}
	parsed, err := url.ParseRequestURI(requestURI)
	if err != nil {
		return err
	}
	if parsed.Scheme != "" || parsed.Host != "" {
		return ErrInvalidRequestURI
	}
	return nil
}
