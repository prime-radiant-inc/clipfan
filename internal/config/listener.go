package config

import (
	"fmt"

	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
)

const (
	defaultPort         = 7853
	legacyDefaultListen = ":7853"
	loopbackHost        = "127.0.0.1"
)

type ListenerMigrationPolicy struct {
	GeneratedLoopbackListenEnabled bool
	ConfigV2WriteEnabled           bool
}

func GeneratedLoopbackDefaultsEnabled() bool {
	return releaseflags.PeerHTTPRuntimeDisabled && releaseflags.ConfigV2WriteEnabled
}

func DefaultListen(loopbackDefault bool, port int) string {
	if !loopbackDefault {
		return legacyDefaultListen
	}
	if port == 0 {
		port = defaultPort
	}
	return fmt.Sprintf("%s:%d", loopbackHost, port)
}

func ApplyGeneratedListenMigration(c *Config, enabled bool) bool {
	if c == nil || !enabled || !isLegacyGeneratedListen(c.Listen) {
		return false
	}
	c.Listen = DefaultListen(true, defaultPort)
	if c.Port == 0 {
		c.Port = defaultPort
	}
	return true
}

func isLegacyGeneratedListen(listen string) bool {
	// Milestone 1a's legacy-default compatibility rule treats these exact
	// default-port values as generated defaults. Non-default wildcards are not
	// migrated here; they enter the later safe-mode path.
	switch listen {
	case legacyDefaultListen, "0.0.0.0:7853", "[::]:7853":
		return true
	default:
		return false
	}
}
