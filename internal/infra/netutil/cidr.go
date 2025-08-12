package netutil

import (
	"net"
)

// MustParseCIDRs parses CIDR strings into []*net.IPNet; invalid entries are ignored.
func MustParseCIDRs(cidrs []string) (out []*net.IPNet) {
	for _, s := range cidrs {
		_, n, err := net.ParseCIDR(s)
		if err == nil && n != nil { out = append(out, n) }
	}
	return
}
