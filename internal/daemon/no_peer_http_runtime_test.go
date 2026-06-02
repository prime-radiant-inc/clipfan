package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/prime-radiant-inc/clipfan/internal/clipboard"
	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
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

func TestGeneratedPeerHTTPRuntimeGateSkipsFanout(t *testing.T) {
	if !releaseflags.PeerHTTPRuntimeDisabled {
		t.Skip("requires internal/test generated peer HTTP runtime gate")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	d, err := NewWithOptions(&config.Config{
		Listen:      ":7853",
		SharedKey:   config.NewSharedKey(),
		Discovery:   "static",
		StaticPeers: []string{"peer-host"},
		Port:        7853,
	}, Options{ListenerBoundaryEnabled: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}
	if !d.peerHTTPDisabled {
		t.Fatal("daemon did not consume generated PeerHTTPRuntimeDisabled gate")
	}
	if d.listenerPlan.SafeMode {
		t.Fatal("setup entered safe mode; generated wildcard listener should be repaired without safe mode")
	}
	assertPeerHTTPRuntimeDisabledStopsFanout(t, d)
}

func TestSSHTransportSkipsOffHostHTTPFanoutEvenWhenStaticPeersConfigured(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	d, err := NewWithOptions(&config.Config{
		Listen:      "127.0.0.1:7853",
		SharedKey:   config.NewSharedKey(),
		Discovery:   "static",
		StaticPeers: []string{"peer-host"},
		Port:        7853,
		Transport:   config.TransportSSH,
		SSH:         &config.SSHConfig{},
	}, Options{
		ListenerBoundaryEnabled: boolPtr(true),
		PeerHTTPRuntimeDisabled: boolPtr(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !d.peerHTTPDisabled {
		t.Fatal("transport:ssh did not disable peer HTTP fanout")
	}
	assertPeerHTTPRuntimeDisabledStopsFanout(t, d)
}

func TestSSHTransportPollOnceDoesNotHTTPFanout(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d := newSSHTransportDaemonForTest(t)
	cb := &fakeBackend{current: clipboard.New(clipboard.KindText, []byte("local-copy"), fixedTime)}
	discoveryProbe := &countingDiscoverer{}
	pushProbe := &fakePusher{}
	d.cb = cb
	d.disc = discoveryProbe
	d.cl = pushProbe

	d.pollOnce(context.Background())
	time.Sleep(50 * time.Millisecond)

	if got := discoveryProbe.count(); got != 0 {
		t.Fatalf("transport:ssh poll called discovery %d times", got)
	}
	if got := len(pushProbe.snapshot()); got != 0 {
		t.Fatalf("transport:ssh poll pushed %d times", got)
	}
}

func TestSSHTransportOnReceiveDoesNotHTTPRelay(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d := newSSHTransportDaemonForTest(t)
	discoveryProbe := &countingDiscoverer{}
	pushProbe := &fakePusher{}
	d.cb = &fakeBackend{}
	d.disc = discoveryProbe
	d.cl = pushProbe

	c := clipboard.New(clipboard.KindText, []byte("peer-copy"), fixedTime)
	c.ID = "ssh-transport-peer-copy"
	d.onReceive(c, "peer")
	time.Sleep(50 * time.Millisecond)

	if got := discoveryProbe.count(); got != 0 {
		t.Fatalf("transport:ssh receive called discovery %d times", got)
	}
	if got := len(pushProbe.snapshot()); got != 0 {
		t.Fatalf("transport:ssh receive relayed %d times", got)
	}
}

func newSSHTransportDaemonForTest(t *testing.T) *Daemon {
	t.Helper()
	d, err := NewWithOptions(&config.Config{
		Listen:      "127.0.0.1:7853",
		SharedKey:   config.NewSharedKey(),
		Discovery:   "static",
		StaticPeers: []string{"peer-host"},
		Port:        7853,
		Transport:   config.TransportSSH,
		SSH:         &config.SSHConfig{},
	}, Options{
		ListenerBoundaryEnabled: boolPtr(true),
		PeerHTTPRuntimeDisabled: boolPtr(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !d.peerHTTPDisabled {
		t.Fatal("transport:ssh did not disable peer HTTP fanout")
	}
	return d
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
