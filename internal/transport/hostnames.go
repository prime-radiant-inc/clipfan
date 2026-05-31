package transport

import "strings"

func ShortName(h string) string {
	h = strings.TrimSuffix(h, ".local")
	return strings.SplitN(h, ".", 2)[0]
}

// RecipientMatches returns true only when a signed peer clip was addressed to
// this daemon's normalized identity. Unlike HostsMatch, this intentionally does
// not accept suffix aliases because recipient checks are a security boundary.
func RecipientMatches(recipient, identity string) bool {
	return ShortName(recipient) != "" && ShortName(recipient) == ShortName(identity)
}

// HostsMatch returns true if two hostnames almost certainly identify the same
// physical host. Exact short-name equality is the easy case; the suffix case
// covers Tailscale names like "jesse-paradise-park" for host "paradise-park".
func HostsMatch(a, b string) bool {
	sa, sb := ShortName(a), ShortName(b)
	if sa == "" || sb == "" {
		return false
	}
	if sa == sb {
		return true
	}
	const minMatchLen = 6
	long, short := sb, sa
	if len(sa) > len(sb) {
		long, short = sa, sb
	}
	if len(short) < minMatchLen {
		return false
	}
	return strings.HasSuffix(long, "-"+short)
}
