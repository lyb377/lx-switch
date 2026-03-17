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
	IPAllowlistEnabled bool `json:"ipAllowlistEnabled"`
}

// ipAllowlistCache caches the allowlist in memory
type ipAllowlistCache struct {
	entries []IPAllowlistEntry
	mu      sync.RWMutex
}

var allowlistCache ipAllowlistCache

// loadSecuritySettings loads security settings and IP allowlist into memory
func (a *App) loadSecuritySettings() {
	entries, err := a.listIPAllowlist()
	if err != nil {
		entries = []IPAllowlistEntry{}
	}
	allowlistCache.mu.Lock()
	allowlistCache.entries = entries
	allowlistCache.mu.Unlock()
}

// withIPAllowlist is a middleware that checks if the client IP is in the allowlist
func (a *App) withIPAllowlist(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Skip if IP allowlist is disabled
		if !a.ipAllowlistEnabled {
			next(w, r)
			return
		}

		ip := clientIP(r)
		if ip == "" {
			http.Error(w, "cannot determine client IP", http.StatusForbidden)
			return
		}

		// Check if IP is in allowlist
		allowed := a.isIPAllowed(ip)
		if !allowed {
			_ = a.insertLoginAudit(ip, r.UserAgent(), false, "ip_not_allowed")
			http.Error(w, "access denied", http.StatusForbidden)
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
			"ok":     true,
			"items":  entries,
			"count":  len(entries),
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
			"ok":   true,
			"id":   id,
			"ipCidr": ipCidr,
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

		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})

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

		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleSecuritySettings handles GET/POST for security settings
func (a *App) handleSecuritySettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                 true,
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

		if req.IPAllowlistEnabled != nil {
			a.ipAllowlistEnabled = *req.IPAllowlistEnabled
		}

		if req.TrustedProxies != nil {
			a.trustedProxies = req.TrustedProxies
		}

		_ = a.insertOpAudit("security.settings.update", "security", 
			fmt.Sprintf("ipAllowlistEnabled=%v trustedProxies=%v", a.ipAllowlistEnabled, a.trustedProxies),
			clientIP(r), r.UserAgent())

		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                 true,
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
