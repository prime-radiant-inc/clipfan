package discovery

import (
	"context"
	"os"
	"testing"
)

func TestStaticMarksSelf(t *testing.T) {
	host, _ := os.Hostname()
	s := NewStatic([]string{host, "other-host"}, 9999)
	peers, err := s.Peers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 2 {
		t.Fatalf("got %d peers, want 2", len(peers))
	}
	var foundSelf bool
	for _, p := range peers {
		if p.Hostname == host && p.Self {
			foundSelf = true
		}
		if p.Hostname == "other-host" && p.Self {
			t.Fatal("other-host was incorrectly marked self")
		}
	}
	if !foundSelf {
		t.Fatalf("self not marked: %+v", peers)
	}
}

func TestShortName(t *testing.T) {
	cases := map[string]string{
		"paradise-park":       "paradise-park",
		"paradise-park.local": "paradise-park",
		"foo.bar.baz":         "foo",
	}
	for in, want := range cases {
		if got := shortName(in); got != want {
			t.Errorf("shortName(%q) = %q, want %q", in, got, want)
		}
	}
}
