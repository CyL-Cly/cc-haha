package httphook

import (
	"net/netip"
	"strings"
)

func blockedV4(address netip.Addr) bool {
	bytes := address.As4()
	a, b := bytes[0], bytes[1]
	return a == 0 || a == 10 || a == 169 && b == 254 || a == 172 && b >= 16 && b <= 31 || a == 100 && b >= 64 && b <= 127 || a == 192 && b == 168
}

// Keep the Source literal IPv6 checks, including its accepted expanded
// unspecified representation; don't replace them with a broader IsPrivate rule.
func IsBlockedAddress(address string) bool {
	ip, err := netip.ParseAddr(address)
	if err != nil {
		return false
	}
	if ip.Is4() {
		return blockedV4(ip)
	}
	lower := strings.ToLower(address)
	if lower == "::1" {
		return false
	}
	if lower == "::" {
		return true
	}
	// Source parseInt accepts a zone after a hex hextet, but Number rejects a
	// zone after the trailing dotted decimal. Preserve both literal paths.
	if ip.Is4In6() && (ip.Zone() == "" || !strings.Contains(address, ".")) {
		return blockedV4(ip.Unmap())
	}
	if strings.HasPrefix(lower, "fc") || strings.HasPrefix(lower, "fd") {
		return true
	}
	first := strings.Split(lower, ":")[0]
	return len(first) == 4 && first >= "fe80" && first <= "febf"
}

func BlockedAddressMessage(hostname, address string) string {
	return "HTTP hook blocked: " + hostname + " resolves to " + address + " (private/link-local address). Loopback (127.0.0.1, ::1) is allowed for local dev."
}

func AddressFamily(address string) int {
	ip, err := netip.ParseAddr(address)
	if err != nil {
		return 0
	}
	if ip.Is4() {
		return 4
	}
	return 6
}
