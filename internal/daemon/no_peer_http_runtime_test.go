package daemon

import (
	"context"
	"testing"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/config"
)

func TestPeerHTTPRuntimeDisabledSkipsFanoutForLegacyStaticConfig(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	d, err := NewWithOptions(&config.Config{
		SharedKey:   config.NewSharedKey(),
		Discovery:   "static",
		StaticPeers: []string{"peer-host"},
		Port:        7853,
	}, Options{
		ListenerBoundaryEnabled: boolPtr(false),
		PeerHTTPRuntimeDisabled: boolPtr(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.cfg.ConfigVersion != nil {
		t.Fatalf("setup wrote config_version = %d, want old config with no transport schema", *d.cfg.ConfigVersion)
	}
	assertPeerHTTPRuntimeDisabledStopsFanout(t, d)
}

func TestPeerHTTPRuntimeDisabledSkipsFanoutForGeneratedWildcardListener(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	d, err := NewWithOptions(&config.Config{
		Listen:      ":7853",
		SharedKey:   config.NewSharedKey(),
		Discovery:   "static",
		StaticPeers: []string{"peer-host"},
		Port:        7853,
	}, Options{
		ListenerBoundaryEnabled: boolPtr(true),
		PeerHTTPRuntimeDisabled: boolPtr(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.listenerPlan.SafeMode {
		t.Fatal("setup entered safe mode; generated wildcard listener should be repaired without safe mode")
	}
	if d.listenerPlan.BindListen != "127.0.0.1:7853" {
		t.Fatalf("BindListen = %q, want generated loopback repair", d.listenerPlan.BindListen)
	}
	assertPeerHTTPRuntimeDisabledStopsFanout(t, d)
}

func assertPeerHTTPRuntimeDisabledStopsFanout(t *testing.T, d *Daemon) {
	t.Helper()
	discoveryProbe := &countingDiscoverer{}
	pushProbe := &fakePusher{}
	d.disc = discoveryProbe
	d.cl = pushProbe
	d.peerStatus = map[string]*PeerState{}

	c := clipboard.New(clipboard.KindText, []byte("local-copy"), fixedTime)
	c.ID = "peer-http-disabled-clip"
	d.fanout(context.Background(), c, "")

	if got := discoveryProbe.count(); got != 0 {
		t.Fatalf("peer HTTP disabled fanout called discovery %d times", got)
	}
	if got := len(pushProbe.snapshot()); got != 0 {
		t.Fatalf("peer HTTP disabled fanout pushed %d times", got)
	}
	if got := len(d.peerStatus); got != 0 {
		t.Fatalf("peer HTTP disabled fanout recorded peer status: %+v", d.peerStatus)
	}
}
