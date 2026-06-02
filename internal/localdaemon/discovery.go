package localdaemon

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/prime-radiant-inc/clipfan/internal/config"
)

const defaultPort = 7853

var ErrSignedIdentityUnverified = errors.New("local_daemon_identity_unverified")

type Purpose string

const (
	PurposeHealthOnly          Purpose = "health_only"
	PurposeSigned              Purpose = "signed"
	PurposeSignedCompatibility Purpose = "signed_compatibility"
)

type Endpoint struct {
	BaseURL string
	Host    string
	Port    int
	Source  string
	Purpose Purpose
}

type Options struct {
	SignedFallbackIdentityProved bool
}

func Discover(cfg *config.Config, purpose Purpose, opts Options) (Endpoint, error) {
	if endpoint, ok := endpointFromConfigListen(cfg, purpose); ok {
		return endpoint, nil
	}
	if isSignedPurpose(purpose) && !opts.SignedFallbackIdentityProved {
		return Endpoint{}, ErrSignedIdentityUnverified
	}
	if purpose == PurposeSigned {
		purpose = PurposeSignedCompatibility
	}
	return endpointFromParts("127.0.0.1", defaultPort, "default_fallback", purpose), nil
}

func endpointFromConfigListen(cfg *config.Config, purpose Purpose) (Endpoint, bool) {
	if cfg == nil || strings.TrimSpace(cfg.Listen) == "" {
		if purpose == PurposeHealthOnly && cfg != nil && validPort(cfg.Port) {
			return endpointFromParts("127.0.0.1", cfg.Port, "config_port", purpose), true
		}
		return Endpoint{}, false
	}
	host, port, err := splitListen(cfg.Listen)
	if err != nil {
		if purpose == PurposeHealthOnly && validPort(cfg.Port) {
			return endpointFromParts("127.0.0.1", cfg.Port, "config_port", purpose), true
		}
		return Endpoint{}, false
	}
	if isSignedPurpose(purpose) && !isLoopbackHost(host) {
		return Endpoint{}, false
	}
	if purpose == PurposeHealthOnly && !isLoopbackHost(host) {
		host = "127.0.0.1"
	}
	return endpointFromParts(host, port, "config_listen", purpose), true
}

func splitListen(listen string) (string, int, error) {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return "", 0, err
	}
	if port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("invalid listen port: %d", port)
	}
	return host, port, nil
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

func validPort(port int) bool {
	return port > 0 && port <= 65535
}

func isSignedPurpose(purpose Purpose) bool {
	return purpose == PurposeSigned || purpose == PurposeSignedCompatibility
}

func endpointFromParts(host string, port int, source string, purpose Purpose) Endpoint {
	u := url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(host, strconv.Itoa(port)),
	}
	return Endpoint{
		BaseURL: u.String(),
		Host:    host,
		Port:    port,
		Source:  source,
		Purpose: purpose,
	}
}
