package cli

import (
	"net"
	"sort"
)

// enumerateLocalIPv4Addresses returns the host's candidate LAN IPv4 addresses for
// mesh-heal's cross-tailnet fallback: every global-unicast IPv4 interface address
// except link-local and the Tailscale CGNAT range. The CGNAT address is already a
// host's primary mesh address, so it is never a useful LAN fallback. Bridge/docker
// addresses are intentionally kept — they are harmless because mesh-heal verifies a
// candidate's host key before selecting it. Results are deduped and sorted so a heal
// run is deterministic. lister defaults to net.InterfaceAddrs (injected for tests).
func enumerateLocalIPv4Addresses(lister func() ([]net.Addr, error)) ([]string, error) {
	if lister == nil {
		lister = net.InterfaceAddrs
	}
	addrs, err := lister()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []string{}
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		default:
			continue
		}
		ip4 := ip.To4()
		if ip4 == nil || !ip4.IsGlobalUnicast() || ip4.IsLinkLocalUnicast() || tailscaleCGNAT.Contains(ip4) {
			continue
		}
		s := ip4.String()
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out, nil
}
