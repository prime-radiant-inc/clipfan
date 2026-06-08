package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
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

type ListenerPlan struct {
	ConfiguredListen      string
	BindListen            string
	EffectiveRepairListen string
	ParseError            string
	SafeMode              bool
	PeerSyncStarted       bool
}

func GeneratedLoopbackDefaultsEnabled() bool {
	return true
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

func PlanListener(c Config, listenerBoundaryEnabled bool) ListenerPlan {
	listen := c.Listen
	if listen == "" {
		listen = DefaultListen(listenerBoundaryEnabled, c.Port)
	}
	plan := ListenerPlan{
		ConfiguredListen: listen,
		BindListen:       listen,
		PeerSyncStarted:  true,
	}
	if !listenerBoundaryEnabled {
		return plan
	}
	if isLegacyGeneratedListen(listen) {
		plan.BindListen = DefaultListen(true, defaultPort)
		return plan
	}
	host, port, ok := splitListenHostPort(listen)
	if ok && isLoopbackHost(host) {
		plan.BindListen = DefaultListen(true, port)
		return plan
	}
	repairPort := port
	parseError := ""
	if !ok {
		if validPort(c.Port) {
			repairPort = c.Port
		} else {
			repairPort = defaultPort
			parseError = "invalid_listen_port"
		}
	}
	plan.SafeMode = true
	plan.PeerSyncStarted = false
	plan.ParseError = parseError
	plan.EffectiveRepairListen = DefaultListen(true, repairPort)
	plan.BindListen = plan.EffectiveRepairListen
	return plan
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

func splitListenHostPort(listen string) (string, int, bool) {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		return "", 0, false
	}
	port, err := strconv.Atoi(portText)
	if err != nil || !validPort(port) {
		return "", 0, false
	}
	return host, port, true
}

func validPort(port int) bool {
	return port >= 1 && port <= 65535
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
