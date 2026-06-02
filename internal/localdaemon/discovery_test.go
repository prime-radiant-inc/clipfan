package localdaemon

import (
	"errors"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

func TestDiscoverUsesConfigDerivedLoopbackForSignedEndpoints(t *testing.T) {
	endpoint, err := Discover(&config.Config{Listen: "127.0.0.1:49123", Port: 7853}, PurposeSigned, Options{})
	if err != nil {
		t.Fatalf("Discover signed loopback: %v", err)
	}
	if endpoint.BaseURL != "http://127.0.0.1:49123" || endpoint.Port != 49123 || endpoint.Source != "config_listen" {
		t.Fatalf("endpoint = %+v", endpoint)
	}
}

func TestDiscoverDoesNotAuthorizeUnsafeListenForSignedEndpoints(t *testing.T) {
	_, err := Discover(&config.Config{Listen: ":49123", Port: 7853}, PurposeSigned, Options{})
	if !errors.Is(err, ErrSignedIdentityUnverified) {
		t.Fatalf("Discover wildcard signed err = %v, want %v", err, ErrSignedIdentityUnverified)
	}

	_, err = Discover(&config.Config{Listen: "0.0.0.0:49123", Port: 7853}, PurposeSigned, Options{})
	if !errors.Is(err, ErrSignedIdentityUnverified) {
		t.Fatalf("Discover public signed err = %v, want %v", err, ErrSignedIdentityUnverified)
	}
}

func TestDiscoverSignedFallbackRequiresIdentityProof(t *testing.T) {
	cfg := &config.Config{Listen: ":49123", Port: 7853}
	_, err := Discover(cfg, PurposeSigned, Options{})
	if !errors.Is(err, ErrSignedIdentityUnverified) {
		t.Fatalf("Discover unproved signed fallback err = %v, want %v", err, ErrSignedIdentityUnverified)
	}

	endpoint, err := Discover(cfg, PurposeSigned, Options{SignedFallbackIdentityProved: true})
	if err != nil {
		t.Fatalf("Discover proved signed fallback: %v", err)
	}
	if endpoint.BaseURL != "http://127.0.0.1:7853" || endpoint.Source != "default_fallback" {
		t.Fatalf("fallback endpoint = %+v", endpoint)
	}
}

func TestDiscoverHealthOnlyUsesConfigPortWithoutSignedAuthorization(t *testing.T) {
	endpoint, err := Discover(&config.Config{Listen: ":49123", Port: 7853}, PurposeHealthOnly, Options{})
	if err != nil {
		t.Fatalf("Discover health-only wildcard: %v", err)
	}
	if endpoint.BaseURL != "http://127.0.0.1:49123" || endpoint.Purpose != PurposeHealthOnly {
		t.Fatalf("health endpoint = %+v", endpoint)
	}

	endpoint, err = Discover(&config.Config{Listen: "bad", Port: 49125}, PurposeHealthOnly, Options{})
	if err != nil {
		t.Fatalf("Discover health-only config port: %v", err)
	}
	if endpoint.BaseURL != "http://127.0.0.1:49125" || endpoint.Source != "config_port" {
		t.Fatalf("config-port endpoint = %+v", endpoint)
	}
}
