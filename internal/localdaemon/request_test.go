package localdaemon

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/transport"
)

func TestNewSignedRequestBuildsVersionedLocalRequest(t *testing.T) {
	auth := fixtureAuth(t)
	endpoint := Endpoint{
		BaseURL: "http://127.0.0.1:49123",
		Host:    "127.0.0.1",
		Port:    49123,
		Source:  "config_listen",
		Purpose: PurposeSigned,
	}
	body := []byte(`{"listen":"127.0.0.1:49123"}`)

	signed, err := NewSignedRequest(context.Background(), endpoint, auth, http.MethodPatch, "/v1/config/listener?expected=7", body, SignedRequestOptions{
		AuthOptions: transport.SignedRequestOptions{
			Timestamp: time.Unix(1780257600, 0),
			Nonce:     "nonce-1",
		},
	})
	if err != nil {
		t.Fatalf("NewSignedRequest: %v", err)
	}
	if signed.Request.URL.String() != "http://127.0.0.1:49123/v1/config/listener?expected=7" {
		t.Fatalf("request URL = %s", signed.Request.URL.String())
	}
	if signed.Nonce != "nonce-1" {
		t.Fatalf("nonce = %q", signed.Nonce)
	}
	if got := signed.Request.Header.Get(transport.HeaderAuthVersion); got != transport.AuthVersionRequestHMAC {
		t.Fatalf("auth version header = %q", got)
	}
	if got := signed.Request.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q", got)
	}
	sig := signed.Request.Header.Get(transport.HeaderSignature)
	if err := auth.VerifyRequestRequiredAuthVersion(http.MethodPatch, "/v1/config/listener?expected=7", "1780257600", "nonce-1", body, sig, transport.AuthVersionRequestHMAC, transport.AuthVersionRequestHMAC); err != nil {
		t.Fatalf("strict verify signed helper request: %v", err)
	}
}

func TestNewSignedRequestAllowsSignedCompatibilityEndpoint(t *testing.T) {
	auth := fixtureAuth(t)
	endpoint := Endpoint{
		BaseURL: "http://127.0.0.1:7853",
		Host:    "127.0.0.1",
		Port:    7853,
		Source:  "default_fallback",
		Purpose: PurposeSignedCompatibility,
	}

	signed, err := NewSignedRequest(context.Background(), endpoint, auth, http.MethodGet, "/v1/peers", nil, SignedRequestOptions{
		AuthOptions: transport.SignedRequestOptions{
			Timestamp: time.Unix(1780257600, 0),
			Nonce:     "compat-nonce",
		},
	})
	if err != nil {
		t.Fatalf("NewSignedRequest compatibility: %v", err)
	}
	if signed.Request.URL.String() != "http://127.0.0.1:7853/v1/peers" {
		t.Fatalf("compat request URL = %s", signed.Request.URL.String())
	}
	if got := signed.Request.Header.Get(transport.HeaderAuthVersion); got != transport.AuthVersionRequestHMAC {
		t.Fatalf("auth version header = %q", got)
	}
}

func TestNewSignedRequestRejectsHealthOnlyEndpoint(t *testing.T) {
	_, err := NewSignedRequest(context.Background(), Endpoint{
		BaseURL: "http://127.0.0.1:49123",
		Host:    "127.0.0.1",
		Port:    49123,
		Source:  "config_listen",
		Purpose: PurposeHealthOnly,
	}, fixtureAuth(t), http.MethodGet, "/v1/status", nil, SignedRequestOptions{})
	if !errors.Is(err, ErrSignedEndpointRequired) {
		t.Fatalf("error = %v, want ErrSignedEndpointRequired", err)
	}
}

func TestNewSignedRequestRejectsNonLocalRequestURI(t *testing.T) {
	auth := fixtureAuth(t)
	endpoint := Endpoint{
		BaseURL: "http://127.0.0.1:49123",
		Host:    "127.0.0.1",
		Port:    49123,
		Source:  "config_listen",
		Purpose: PurposeSigned,
	}
	for _, requestURI := range []string{
		"",
		"v1/peers",
		"//example.com/v1/peers",
		"http://example.com/v1/peers",
		"https://example.com/v1/peers",
	} {
		_, err := NewSignedRequest(context.Background(), endpoint, auth, http.MethodGet, requestURI, nil, SignedRequestOptions{})
		if !errors.Is(err, ErrInvalidRequestURI) {
			t.Fatalf("requestURI %q error = %v, want ErrInvalidRequestURI", requestURI, err)
		}
	}
}

func fixtureAuth(t *testing.T) *transport.Auth {
	t.Helper()
	auth, err := transport.NewAuth("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatalf("NewAuth: %v", err)
	}
	return auth
}
