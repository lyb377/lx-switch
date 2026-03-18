package main

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// IPAllowlistEntry represents an IP allowlist entry
type IPAllowlistEntry struct {
	ID          int64     `json:"id"`
	IPCIDR      string    `json:"ipCidr"`
	Description string    `json:"description"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"createdAt"`
}

// SecuritySettings holds security-related settings
type SecuritySettings struct {
	IPAllowlistEnabled bool     `json:"ipAllowlistEnabled"`
	TrustedProxies     []string `json:"trustedProxies"`
}

// ipAllowlistCache caches the allowlist in memory
type ipAllowlistCache struct {
	entries []IPAllowlistEntry
	mu      sync.RWMutex
}

var allowlistCache ipAllowlistCache

// loadSecuritySettings loads security settings and IP allowlist into memory
func (a *App) loadSecuritySettings() {
	var enabledLoaded *bool
	var trustedLoaded []string

	// Load IP allowlist enabled status from database
	if v, err := a.getState("ip_allowlist_enabled"); err == nil && v != "" {
		enabled := v == "1" || v == "true"
		enabledLoaded = &enabled
	}

	// Load trusted proxies from database
	if v, err := a.getState("trusted_proxies"); err == nil && v != "" {
		var proxies []string
		if err := json.Unmarshal([]byte(v), &proxies); err == nil {
			trustedLoaded = proxies
		}
	}

	if enabledLoaded != nil || trustedLoaded != nil {
		a.settingsMu.Lock()
		if enabledLoaded != nil {
			a.ipAllowlistEnabled = *enabledLoaded
		}
		if trustedLoaded != nil {
			a.trustedProxies = trustedLoaded
		}
		a.settingsMu.Unlock()
	}

	// Load IP allowlist entries
	entries, err := a.listIPAllowlist()
	if err != nil {
		entries = []IPAllowlistEntry{}
	}
	allowlistCache.mu.Lock()
	allowlistCache.entries = entries
	allowlistCache.mu.Unlock()
}

// getRealIP gets the real client IP, supporting X-Forwarded-For trust chain
// This correctly walks backwards through the XFF chain to find the first non-trusted proxy IP
func getRealIP(r *http.Request, trustedProxies []string) string {
	remoteIP, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		remoteIP = strings.TrimSpace(r.RemoteAddr)
	}

	remoteNetIP := net.ParseIP(remoteIP)
	if remoteNetIP == nil {
		return remoteIP
	}

	// Check if RemoteAddr is in trusted proxy list
	if !isIPInCIDRList(remoteNetIP, trustedProxies) {
		// Not a trusted proxy, return RemoteAddr directly
		return remoteIP
	}

	// Cloudflare: when configured, CF-Connecting-IP is the original client IP.
	cfConnectingIP := strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))
	if cfConnectingIP != "" {
		if parsedIP := net.ParseIP(cfConnectingIP); parsedIP != nil {
			return parsedIP.String()
		}
	}

	// Get real IP from X-Forwarded-For chain
	// XFF format: client, proxy1, proxy2, ... (leftmost is original client)
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		ips := strings.Split(xff, ",")
		// Walk backwards from rightmost (most recent) to find first non-trusted IP
		for i := len(ips) - 1; i >= 0; i-- {
			ip := strings.TrimSpace(ips[i])
			if ip == "" {
				continue
			}
			parsedIP := net.ParseIP(ip)
			if parsedIP == nil {
				continue
			}
			// If this IP is not in trusted proxies, it's the real client IP
			if !isIPInCIDRList(parsedIP, trustedProxies) {
				return parsedIP.String()
			}
		}
		// All IPs in chain are trusted, return the leftmost (original client)
		for _, ip := range ips {
			ip = strings.TrimSpace(ip)
			if ip == "" {
				continue
			}
			if parsedIP := net.ParseIP(ip); parsedIP != nil {
				return parsedIP.String()
			}
		}
	}

	// Try X-Real-IP
	xri := strings.TrimSpace(r.Header.Get("X-Real-Ip"))
	if xri != "" {
		if parsedIP := net.ParseIP(xri); parsedIP != nil {
			return parsedIP.String()
		}
	}

	return remoteIP
}

// isIPInCIDRList checks if an IP is in a list of CIDR ranges or exact IPs
func isIPInCIDRList(ip net.IP, list []string) bool {
	for _, cidr := range list {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}
		if isIPInCIDR(ip, cidr) {
			return true
		}
	}
	return false
}

// isIPInCIDR checks if an IP is in a CIDR range
func isIPInCIDR(ip net.IP, cidr string) bool {
	// If it's a plain IP, compare directly
	if parsedIP := net.ParseIP(cidr); parsedIP != nil {
		return ip.Equal(parsedIP)
	}

	// Parse CIDR
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}

	return ipNet.Contains(ip)
}

// withIPAllowlist is a middleware that checks if the client IP is in the allowlist
func (a *App) withIPAllowlist(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a.settingsMu.RLock()
		enabled := a.ipAllowlistEnabled
		trusted := a.trustedProxies
		a.settingsMu.RUnlock()

		// Skip if IP allowlist is disabled
		if !enabled {
			next(w, r)
			return
		}

		// Get real IP
		realIP := getRealIP(r, trusted)
		if realIP == "" {
			http.Error(w, "cannot determine client IP", http.StatusForbidden)
			return
		}

		// Check if IP is in allowlist
		allowed := a.isIPAllowed(realIP)
		if !allowed {
			_ = a.insertLoginAudit(realIP, r.UserAgent(), false, "ip_not_allowed")
			_ = a.insertOpAudit("security.ip_blocked", "", "ip="+realIP+" path="+r.URL.Path, realIP, strings.TrimSpace(r.UserAgent()))
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "access denied: IP not in allowlist"})
			return
		}

		next(w, r)
	}
}

// isIPAllowed checks if an IP is in the allowlist
func (a *App) isIPAllowed(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	allowlistCache.mu.RLock()
	entries := allowlistCache.entries
	allowlistCache.mu.RUnlock()

	for _, entry := range entries {
		if !entry.Enabled {
			continue
		}

		// Check if it's a CIDR or single IP
		if strings.Contains(entry.IPCIDR, "/") {
			_, cidr, err := net.ParseCIDR(entry.IPCIDR)
			if err != nil {
				continue
			}
			if cidr.Contains(ip) {
				return true
			}
		} else {
			// Single IP comparison
			allowedIP := net.ParseIP(entry.IPCIDR)
			if allowedIP != nil && ip.Equal(allowedIP) {
				return true
			}
		}
	}

	return false
}

// handleIPAllowlist handles GET (list) and POST (add) for IP allowlist
func (a *App) handleIPAllowlist(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		entries, err := a.listIPAllowlist()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"items":   entries,
			"count":   len(entries),
			"enabled": a.ipAllowlistEnabled,
		})

	case http.MethodPost:
		var req struct {
			IPCIDR      string `json:"ipCidr"`
			Description string `json:"description"`
			Enabled     *bool  `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Validate IP/CIDR
		ipCidr := strings.TrimSpace(req.IPCIDR)
		if ipCidr == "" {
			http.Error(w, "ipCidr required", http.StatusBadRequest)
			return
		}

		// Validate format
		if strings.Contains(ipCidr, "/") {
			_, _, err := net.ParseCIDR(ipCidr)
			if err != nil {
				http.Error(w, "invalid CIDR format", http.StatusBadRequest)
				return
			}
		} else {
			if net.ParseIP(ipCidr) == nil {
				http.Error(w, "invalid IP format", http.StatusBadRequest)
				return
			}
		}

		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}

		now := time.Now().Format(time.RFC3339)
		res, err := a.db.Exec(
			`INSERT INTO ip_allowlist(ip_cidr, description, enabled, created_at) VALUES(?, ?, ?, ?)`,
			ipCidr, req.Description, enabled, now,
		)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		id, _ := res.LastInsertId()
		_ = a.insertOpAudit("security.ip_allowlist.add", "security", fmt.Sprintf("id=%d ipCidr=%s", id, ipCidr), clientIP(r), r.UserAgent())

		// Reload cache
		a.loadSecuritySettings()

		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"id":      id,
			"ipCidr":  ipCidr,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleIPAllowlistByID handles DELETE for a specific IP allowlist entry
func (a *App) handleIPAllowlistByID(w http.ResponseWriter, r *http.Request) {
	// Extract ID from path
	idStr := strings.TrimPrefix(r.URL.Path, "/api/security/ip-allowlist/")
	if idStr == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	var id int64
	if _, err := fmt.Sscan(idStr, &id); err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		// Get entry before delete for audit
		var ipCidr string
		err := a.db.QueryRow(`SELECT ip_cidr FROM ip_allowlist WHERE id = ?`, id).Scan(&ipCidr)
		if err == sql.ErrNoRows {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		_, err = a.db.Exec(`DELETE FROM ip_allowlist WHERE id = ?`, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		_ = a.insertOpAudit("security.ip_allowlist.delete", "security", fmt.Sprintf("id=%d ipCidr=%s", id, ipCidr), clientIP(r), r.UserAgent())

		// Reload cache
		a.loadSecuritySettings()

		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})

	case http.MethodPut:
		// Update entry
		var req struct {
			IPCIDR      string `json:"ipCidr"`
			Description string `json:"description"`
			Enabled     *bool  `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Build update query
		updates := []string{}
		args := []any{}

		if req.IPCIDR != "" {
			ipCidr := strings.TrimSpace(req.IPCIDR)
			// Validate format
			if strings.Contains(ipCidr, "/") {
				_, _, err := net.ParseCIDR(ipCidr)
				if err != nil {
					http.Error(w, "invalid CIDR format", http.StatusBadRequest)
					return
				}
			} else {
				if net.ParseIP(ipCidr) == nil {
					http.Error(w, "invalid IP format", http.StatusBadRequest)
					return
				}
			}
			updates = append(updates, "ip_cidr = ?")
			args = append(args, ipCidr)
		}

		if req.Description != "" {
			updates = append(updates, "description = ?")
			args = append(args, req.Description)
		}

		if req.Enabled != nil {
			updates = append(updates, "enabled = ?")
			args = append(args, *req.Enabled)
		}

		if len(updates) == 0 {
			http.Error(w, "no fields to update", http.StatusBadRequest)
			return
		}

		args = append(args, id)
		query := "UPDATE ip_allowlist SET " + strings.Join(updates, ", ") + " WHERE id = ?"
		_, err := a.db.Exec(query, args...)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		_ = a.insertOpAudit("security.ip_allowlist.update", "security", fmt.Sprintf("id=%d", id), clientIP(r), r.UserAgent())

		// Reload cache
		a.loadSecuritySettings()

		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleSecuritySettings handles GET/POST for security settings
func (a *App) handleSecuritySettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success":            true,
			"ipAllowlistEnabled": a.ipAllowlistEnabled,
			"trustedProxies":     a.trustedProxies,
		})

	case http.MethodPost:
		var req struct {
			IPAllowlistEnabled *bool    `json:"ipAllowlistEnabled"`
			TrustedProxies     []string `json:"trustedProxies"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Validate & normalize trusted proxies (IP or CIDR).
		if req.TrustedProxies != nil {
			normalized := make([]string, 0, len(req.TrustedProxies))
			for _, raw := range req.TrustedProxies {
				s := strings.TrimSpace(raw)
				if s == "" {
					continue
				}
				if strings.Contains(s, "/") {
					if _, _, err := net.ParseCIDR(s); err != nil {
						http.Error(w, "invalid trustedProxies CIDR: "+s, http.StatusBadRequest)
						return
					}
				} else {
					if net.ParseIP(s) == nil {
						http.Error(w, "invalid trustedProxies IP: "+s, http.StatusBadRequest)
						return
					}
				}
				normalized = append(normalized, s)
			}
			if normalized == nil {
				normalized = []string{}
			}
			req.TrustedProxies = normalized
		}

		// Prevent foot-gun: if enabling allowlist, ensure there is at least one enabled entry
		// AND the current request IP will remain allowed under the new trusted proxy settings.
		if req.IPAllowlistEnabled != nil && *req.IPAllowlistEnabled {
			// Ensure cache reflects latest DB changes.
			a.loadSecuritySettings()

			allowlistCache.mu.RLock()
			entries := allowlistCache.entries
			allowlistCache.mu.RUnlock()

			hasEnabled := false
			for _, e := range entries {
				if e.Enabled {
					hasEnabled = true
					break
				}
			}
			if !hasEnabled {
				http.Error(w, "cannot enable allowlist: no enabled ip_allowlist entries", http.StatusBadRequest)
				return
			}

			proposedTrusted := a.trustedProxies
			if req.TrustedProxies != nil {
				proposedTrusted = req.TrustedProxies
			}
			realIP := getRealIP(r, proposedTrusted)
			if realIP == "" || !a.isIPAllowed(realIP) {
				http.Error(w, "cannot enable allowlist: current IP not allowed ("+realIP+")", http.StatusBadRequest)
				return
			}
		}

		if req.IPAllowlistEnabled != nil {
			a.ipAllowlistEnabled = *req.IPAllowlistEnabled
			// Save to database
			enabled := "false"
			if *req.IPAllowlistEnabled {
				enabled = "true"
			}
			_ = a.setState("ip_allowlist_enabled", enabled)
		}

		if req.TrustedProxies != nil {
			a.trustedProxies = req.TrustedProxies
			// Save to database
			proxiesJSON, _ := json.Marshal(req.TrustedProxies)
			_ = a.setState("trusted_proxies", string(proxiesJSON))
		}

		_ = a.insertOpAudit("security.settings.update", "security",
			fmt.Sprintf("ipAllowlistEnabled=%v trustedProxies=%v", a.ipAllowlistEnabled, a.trustedProxies),
			clientIP(r), r.UserAgent())

		// Reload cache after settings update (so new trusted proxies/enable state takes effect immediately).
		a.loadSecuritySettings()

		_ = json.NewEncoder(w).Encode(map[string]any{
			"success":            true,
			"ipAllowlistEnabled": a.ipAllowlistEnabled,
			"trustedProxies":     a.trustedProxies,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// listIPAllowlist returns all IP allowlist entries
func (a *App) listIPAllowlist() ([]IPAllowlistEntry, error) {
	rows, err := a.db.Query(`SELECT id, ip_cidr, description, enabled, created_at FROM ip_allowlist ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []IPAllowlistEntry
	for rows.Next() {
		var e IPAllowlistEntry
		var createdAt string
		var enabled int
		if err := rows.Scan(&e.ID, &e.IPCIDR, &e.Description, &enabled, &createdAt); err != nil {
			return nil, err
		}
		e.Enabled = enabled == 1
		e.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		entries = append(entries, e)
	}

	if entries == nil {
		entries = []IPAllowlistEntry{}
	}
	return entries, nil
}

// secureCompare performs a constant-time comparison to prevent timing attacks
func secureCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
