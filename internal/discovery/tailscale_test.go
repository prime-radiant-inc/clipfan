package discovery

import "testing"

func TestParseTailscalePeersFiltersToAllowedHosts(t *testing.T) {
	raw := []byte(`{
	  "Self": {"HostName": "macbook.tail.ts.net", "Online": true},
	  "Peer": {
	    "a": {"HostName": "allowed-host.tail.ts.net", "Online": true},
	    "b": {"HostName": "other-host.tail.ts.net", "Online": true},
	    "c": {"HostName": "offline-host.tail.ts.net", "Online": false}
	  }
	}`)

	got, err := parseTailscalePeers(raw, 7853, []string{"allowed-host"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want self plus one allowed peer: %+v", len(got), got)
	}
	if got[0].Hostname != "macbook" || !got[0].Self {
		t.Fatalf("first peer should be self: %+v", got[0])
	}
	if got[1].Hostname != "allowed-host" || got[1].Self {
		t.Fatalf("second peer should be allowed-host: %+v", got[1])
	}
}

func TestParseTailscalePeersWithEmptyAllowlistReturnsOnlySelf(t *testing.T) {
	raw := []byte(`{
	  "Self": {"HostName": "macbook.tail.ts.net", "Online": true},
	  "Peer": {
	    "a": {"HostName": "online-host.tail.ts.net", "Online": true}
	  }
	}`)

	got, err := parseTailscalePeers(raw, 7853, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Hostname != "macbook" || !got[0].Self {
		t.Fatalf("empty allowlist should return only self, got %+v", got)
	}
}

func TestTailscaleStoresNormalizedAllowedHosts(t *testing.T) {
	tailscale := NewTailscale(7853, []string{
		"allowed-host.local",
		"other-host.tail.ts.net",
	})

	if len(tailscale.allowed) != 2 {
		t.Fatalf("allowed map len = %d, want 2: %+v", len(tailscale.allowed), tailscale.allowed)
	}
	for _, host := range []string{"allowed-host", "other-host"} {
		if !tailscale.allowed[host] {
			t.Fatalf("allowed map missing normalized host %q: %+v", host, tailscale.allowed)
		}
	}
}
