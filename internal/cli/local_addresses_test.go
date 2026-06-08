package cli

import (
	"errors"
	"net"
	"reflect"
	"testing"
)

func ipNetAddr(s string) net.Addr {
	return &net.IPNet{IP: net.ParseIP(s)}
}

func TestEnumerateLocalIPv4AddressesFiltersAndSorts(t *testing.T) {
	lister := func() ([]net.Addr, error) {
		return []net.Addr{
			ipNetAddr("192.168.1.5"),  // LAN - kept
			ipNetAddr("10.0.0.3"),     // LAN - kept
			ipNetAddr("100.64.1.2"),   // Tailscale CGNAT - excluded (it's the primary)
			ipNetAddr("169.254.1.1"),  // link-local - excluded
			ipNetAddr("127.0.0.1"),    // loopback - excluded
			ipNetAddr("fe80::1"),      // IPv6 link-local - excluded
			ipNetAddr("2001:db8::1"),  // IPv6 - excluded
			ipNetAddr("192.168.1.5"),  // duplicate - collapsed
		}, nil
	}
	got, err := enumerateLocalIPv4Addresses(lister)
	if err != nil {
		t.Fatalf("enumerateLocalIPv4Addresses() error = %v", err)
	}
	want := []string{"10.0.0.3", "192.168.1.5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestEnumerateLocalIPv4AddressesEmpty(t *testing.T) {
	got, err := enumerateLocalIPv4Addresses(func() ([]net.Addr, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty", got)
	}
}

func TestEnumerateLocalIPv4AddressesListerError(t *testing.T) {
	sentinel := errors.New("interface enumeration failed")
	_, err := enumerateLocalIPv4Addresses(func() ([]net.Addr, error) { return nil, sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}
