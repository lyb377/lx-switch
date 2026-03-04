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
