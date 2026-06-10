package cli

import (
	"net"
	"sort"
	"strings"
)

type localInterfaceAddrs struct {
	Name  string
	Flags net.Flags
	Addrs []net.Addr
}

// enumerateLocalIPv4Addresses returns the host's candidate LAN IPv4 addresses for
// mesh-heal's cross-tailnet fallback: active, non-virtual, global-unicast IPv4
// interface addresses except link-local and the Tailscale CGNAT range. The CGNAT
// address is already a host's primary mesh address, so it is never a useful LAN
// fallback. Results are deduped and sorted so a heal run is deterministic. lister
// is the address-only test seam; production uses interface-aware enumeration so
// bridge/docker/VM addresses are not advertised.
func enumerateLocalIPv4Addresses(lister func() ([]net.Addr, error)) ([]string, error) {
	if lister != nil {
		addrs, err := lister()
		if err != nil {
			return nil, err
		}
		return enumerateLocalIPv4AddressesFromAddrs(addrs), nil
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	interfaces := make([]localInterfaceAddrs, 0, len(ifaces))
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, err
		}
		interfaces = append(interfaces, localInterfaceAddrs{
			Name:  iface.Name,
			Flags: iface.Flags,
			Addrs: addrs,
		})
	}
	return enumerateLocalIPv4AddressesFromInterfaceAddrs(interfaces), nil
}

func enumerateLocalIPv4AddressesFromInterfaceAddrs(ifaces []localInterfaceAddrs) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, iface := range ifaces {
		if !isLANCandidateInterface(iface.Name, iface.Flags) {
			continue
		}
		appendLocalIPv4Addresses(&out, seen, iface.Addrs)
	}
	sort.Strings(out)
	return out
}

func enumerateLocalIPv4AddressesFromAddrs(addrs []net.Addr) []string {
	seen := map[string]bool{}
	out := []string{}
	appendLocalIPv4Addresses(&out, seen, addrs)
	sort.Strings(out)
	return out
}

func appendLocalIPv4Addresses(out *[]string, seen map[string]bool, addrs []net.Addr) {
	for _, a := range addrs {
		ip := localAddrIP(a)
		ip4 := ip.To4()
		if ip4 == nil || !ip4.IsGlobalUnicast() || ip4.IsLinkLocalUnicast() || tailscaleCGNAT.Contains(ip4) {
			continue
		}
		s := ip4.String()
		if !seen[s] {
			seen[s] = true
			*out = append(*out, s)
		}
	}
}

func localAddrIP(addr net.Addr) net.IP {
	switch v := addr.(type) {
	case *net.IPNet:
		return v.IP
	case *net.IPAddr:
		return v.IP
	default:
		return nil
	}
}

func isLANCandidateInterface(name string, flags net.Flags) bool {
	if flags&net.FlagUp == 0 || flags&net.FlagLoopback != 0 || flags&net.FlagPointToPoint != 0 {
		return false
	}
	lower := strings.ToLower(name)
	for _, prefix := range []string{
		"br-",
		"bridge",
		"docker",
		"llw",
		"utun",
		"veth",
		"virbr",
		"vmenet",
		"vmnet",
	} {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	return true
}
