package middleware

import (
	"net"
	"net/http"
)

// AdminGate restricts access to admin endpoints by remote IP against allowed CIDR list.
func AdminGate(allowed []*net.IPNet, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil { host = r.RemoteAddr }
		ip := net.ParseIP(host)
		if ip == nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		for _, n := range allowed {
			if n.Contains(ip) { next.ServeHTTP(w, r); return }
		}
		http.Error(w, "forbidden", http.StatusForbidden)
	})
}
