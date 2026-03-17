package main

import (
	"encoding/json"
	"fmt"
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

func getRealIP(r *http.Request, trustedProxies []string) string {
	remote := parseRemoteAddrIP(r.RemoteAddr)
	if remote == nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	if !ipInCIDRList(remote, trustedProxies) {
		return remote.String()
	}
	chain := parseXFF(r.Header.Get("X-Forwarded-For"))
	for i := len(chain) - 1; i >= 0; i-- {
		if !ipInCIDRList(chain[i], trustedProxies) {
			return chain[i].String()
		}
	}
	if len(chain) > 0 {
		return chain[0].String()
	}
	return remote.String()
}

func parseRemoteAddrIP(remoteAddr string) net.IP {
	remoteAddr = strings.TrimSpace(remoteAddr)
	if remoteAddr == "" {
		return nil
	}
	h, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		if ip := net.ParseIP(strings.TrimSpace(h)); ip != nil {
			return ip
		}
	}
	if ip := net.ParseIP(remoteAddr); ip != nil {
		return ip
	}
	return nil
}

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

func (a *App) withIPAllowlist(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a.settingsMu.RLock()
		enabled := a.ipAllowlistEnabled
		trusted := a.trustedProxies
		a.settingsMu.RUnlock()
		if !enabled {
			next(w, r)
			return
		}
		realIPStr := getRealIP(r, trusted)
		realIP := net.ParseIP(strings.TrimSpace(realIPStr))
		if realIP == nil {
			_ = a.insertOpAudit("security.ip_blocked", strings.TrimSpace(realIPStr), fmt.Sprintf("reason=invalid_ip method=%s path=%s", r.Method, r.URL.Path), strings.TrimSpace(realIPStr), strings.TrimSpace(r.UserAgent()))
			w.WriteHeader(http.StatusForbidden)
			return
		}
		ok, err := a.ipAllowed(realIP)
		if err != nil {
			_ = a.insertOpAudit("security.ip_blocked", realIP.String(), fmt.Sprintf("reason=check_failed method=%s path=%s", r.Method, r.URL.Path), realIP.String(), strings.TrimSpace(r.UserAgent()))
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if !ok {
			detail := fmt.Sprintf("method=%s path=%s", r.Method, r.URL.Path)
			xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
			if xff != "" {
				detail += " xff=" + trimForAudit(xff)
			}
			_ = a.insertOpAudit("security.ip_blocked", realIP.String(), detail, realIP.String(), strings.TrimSpace(r.UserAgent()))
			w.WriteHeader(http.StatusForbidden)
			if strings.HasPrefix(r.URL.Path, "/api/") {
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
				return
			}
			_, _ = w.Write([]byte("forbidden"))
			return
		}
		next(w, r)
	}
}
