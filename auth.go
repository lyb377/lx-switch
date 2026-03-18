package main

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

func (a *App) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t := r.Header.Get("X-Admin-Token")
		if t == "" {
			t = r.URL.Query().Get("token")
		}
		if t == "" {
			if c, err := r.Cookie("lx_token"); err == nil {
				t = c.Value
			}
		}
		if t != a.adminToken {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func (a *App) withPageAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("lx_token")
		if err != nil || c.Value != a.adminToken {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

func (a *App) requestSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	if strings.EqualFold(r.Header.Get("X-Forwarded-Ssl"), "on") {
		return true
	}
	return false
}

func clientIP(r *http.Request) string {
	// Simple fallback - use getRealIP with empty trusted proxies for direct connection
	// For trusted proxy scenarios, use getRealIP from security.go with actual trusted proxies
	xff := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
	if xff != "" {
		if ip := net.ParseIP(xff); ip != nil {
			return ip.String()
		}
	}
	h, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		if ip := net.ParseIP(h); ip != nil {
			return ip.String()
		}
	}
	return strings.TrimSpace(r.RemoteAddr)
}

// realClientIP returns the real client IP using trusted proxy chain
func (a *App) realClientIP(r *http.Request) string {
	a.settingsMu.RLock()
	trusted := a.trustedProxies
	a.settingsMu.RUnlock()
	return getRealIP(r, trusted)
}

// getRealIP is defined in security.go

func parseXFF(xff string) []net.IP {
	xff = strings.TrimSpace(xff)
	if xff == "" {
		return nil
	}
	parts := strings.Split(xff, ",")
	out := make([]net.IP, 0, len(parts))
	for _, p := range parts {
		ip := net.ParseIP(strings.TrimSpace(p))
		if ip == nil {
			continue
		}
		out = append(out, ip)
	}
	if out == nil {
		out = []net.IP{}
	}
	return out
}

func ipInCIDRList(ip net.IP, list []string) bool {
	if ip == nil || len(list) == 0 {
		return false
	}
	for _, raw := range list {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if strings.Contains(s, "/") {
			_, n, err := net.ParseCIDR(s)
			if err != nil {
				continue
			}
			if n.Contains(ip) {
				return true
			}
			continue
		}
		p := net.ParseIP(s)
		if p == nil {
			continue
		}
		if p.Equal(ip) {
			return true
		}
	}
	return false
}

// withIPAllowlist is defined in security.go
