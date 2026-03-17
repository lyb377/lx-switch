package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// IPAllowlistEntry IP白名单条目
type IPAllowlistEntry struct {
	ID          int64     `json:"id"`
	IPCIDR      string    `json:"ipCidr"`
	Description string    `json:"description"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"createdAt"`
}

// SecuritySettings 安全设置
type SecuritySettings struct {
	IPAllowlistEnabled bool     `json:"ipAllowlistEnabled"`
	TrustedProxies     []string `json:"trustedProxies"`
}

// getRealIP 获取真实客户端IP，支持X-Forwarded-For信任链
func getRealIP(r *http.Request, trustedProxies []string) string {
	remoteIP, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		remoteIP = strings.TrimSpace(r.RemoteAddr)
	}

	// 解析RemoteAddr
	remoteNetIP := net.ParseIP(remoteIP)
	if remoteNetIP == nil {
		return remoteIP
	}

	// 检查RemoteAddr是否在信任代理列表中
	isTrustedProxy := false
	for _, proxy := range trustedProxies {
		if isIPInCIDR(remoteNetIP, proxy) {
			isTrustedProxy = true
			break
		}
	}

	// 如果不是信任代理，直接返回RemoteAddr
	if !isTrustedProxy {
		return remoteIP
	}

	// 从X-Forwarded-For获取真实IP
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		// X-Forwarded-For可能包含多个IP，取第一个（最原始的客户端）
		ips := strings.Split(xff, ",")
		for _, ip := range ips {
			ip = strings.TrimSpace(ip)
			if parsedIP := net.ParseIP(ip); parsedIP != nil {
				return parsedIP.String()
			}
		}
	}

	// 尝试X-Real-IP
	xri := strings.TrimSpace(r.Header.Get("X-Real-Ip"))
	if xri != "" {
		if parsedIP := net.ParseIP(xri); parsedIP != nil {
			return parsedIP.String()
		}
	}

	return remoteIP
}

// isIPInCIDR 检查IP是否在CIDR范围内
func isIPInCIDR(ip net.IP, cidr string) bool {
	// 如果是纯IP，直接比较
	if parsedIP := net.ParseIP(cidr); parsedIP != nil {
		return ip.Equal(parsedIP)
	}

	// 解析CIDR
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}

	return ipNet.Contains(ip)
}

// isIPAllowed 检查IP是否被允许访问
func (a *App) isIPAllowed(ip string) bool {
	if !a.ipAllowlistEnabled {
		return true
	}

	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}

	// 查询数据库中的白名单
	entries, err := a.listIPAllowlist()
	if err != nil {
		// 数据库错误时，为了安全，默认拒绝
		return false
	}

	// 如果没有配置任何白名单，允许所有（或者根据策略拒绝所有）
	if len(entries) == 0 {
		return true
	}

	for _, entry := range entries {
		if !entry.Enabled {
			continue
		}
		if isIPInCIDR(parsedIP, entry.IPCIDR) {
			return true
		}
	}

	return false
}

// withIPAllowlist IP白名单中间件
func (a *App) withIPAllowlist(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 如果白名单未启用，直接放行
		if !a.ipAllowlistEnabled {
			next(w, r)
			return
		}

		// 获取真实IP
		realIP := getRealIP(r, a.trustedProxies)

		// 检查IP是否被允许
		if !a.isIPAllowed(realIP) {
			// 记录审计日志
			_ = a.insertOpAudit("security.ip_blocked", "", "ip="+realIP+" path="+r.URL.Path, realIP, strings.TrimSpace(r.UserAgent()))

			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "access denied: IP not in allowlist",
			})
			return
		}

		next(w, r)
	}
}

// loadSecuritySettings 从数据库加载安全设置
func (a *App) loadSecuritySettings() {
	// 加载IP白名单启用状态
	if v, err := a.getState("ip_allowlist_enabled"); err == nil && v != "" {
		a.ipAllowlistEnabled = v == "1" || v == "true"
	}

	// 加载信任代理列表
	if v, err := a.getState("trusted_proxies"); err == nil && v != "" {
		var proxies []string
		if err := json.Unmarshal([]byte(v), &proxies); err == nil {
			a.trustedProxies = proxies
		}
	}
}

// saveSecuritySettings 保存安全设置到数据库
func (a *App) saveSecuritySettings() error {
	enabled := "false"
	if a.ipAllowlistEnabled {
		enabled = "true"
	}
	if err := a.setState("ip_allowlist_enabled", enabled); err != nil {
		return err
	}

	proxiesJSON, _ := json.Marshal(a.trustedProxies)
	if err := a.setState("trusted_proxies", string(proxiesJSON)); err != nil {
		return err
	}

	return nil
}

// initSecurityTables 初始化安全相关数据库表
func (a *App) initSecurityTables() error {
	schema := `
CREATE TABLE IF NOT EXISTS ip_allowlist (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	ip_cidr TEXT NOT NULL,
	description TEXT,
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ip_allowlist_enabled ON ip_allowlist(enabled);
`
	_, err := a.db.Exec(schema)
	return err
}

// listIPAllowlist 列出所有IP白名单条目
func (a *App) listIPAllowlist() ([]IPAllowlistEntry, error) {
	rows, err := a.db.Query(`SELECT id, ip_cidr, description, enabled, created_at FROM ip_allowlist ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []IPAllowlistEntry
	for rows.Next() {
		var it IPAllowlistEntry
		var enabled int
		var ts string
		if err := rows.Scan(&it.ID, &it.IPCIDR, &it.Description, &enabled, &ts); err != nil {
			return nil, err
		}
		it.Enabled = enabled == 1
		it.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		out = append(out, it)
	}

	if out == nil {
		out = []IPAllowlistEntry{}
	}

	return out, nil
}

// addIPAllowlist 添加IP白名单条目
func (a *App) addIPAllowlist(ipCidr, description string) (int64, error) {
	// 验证IP或CIDR格式
	if net.ParseIP(ipCidr) == nil {
		// 尝试解析CIDR
		_, _, err := net.ParseCIDR(ipCidr)
		if err != nil {
			return 0, err
		}
	}

	res, err := a.db.Exec(
		`INSERT INTO ip_allowlist(ip_cidr, description, enabled, created_at) VALUES(?, ?, 1, ?)`,
		ipCidr, description, time.Now().Format(time.RFC3339),
	)
	if err != nil {
		return 0, err
	}

	return res.LastInsertId()
}

// deleteIPAllowlist 删除IP白名单条目
func (a *App) deleteIPAllowlist(id int64) error {
	_, err := a.db.Exec(`DELETE FROM ip_allowlist WHERE id=?`, id)
	return err
}

// toggleIPAllowlist 启用/禁用IP白名单条目
func (a *App) toggleIPAllowlist(id int64, enabled bool) error {
	en := 0
	if enabled {
		en = 1
	}
	_, err := a.db.Exec(`UPDATE ip_allowlist SET enabled=? WHERE id=?`, en, id)
	return err
}

// 处理函数

func (a *App) handleIPAllowlist(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := a.listIPAllowlist()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": list,
			"enabled": a.ipAllowlistEnabled,
		})

	case http.MethodPost:
		var req struct {
			IPCIDR      string `json:"ipCidr"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		req.IPCIDR = strings.TrimSpace(req.IPCIDR)
		if req.IPCIDR == "" {
			http.Error(w, "ipCidr required", 400)
			return
		}

		id, err := a.addIPAllowlist(req.IPCIDR, req.Description)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		_ = a.insertOpAudit("security.ip_allowlist.add", "", "id="+string(rune(id))+" ip="+req.IPCIDR, clientIP(r), strings.TrimSpace(r.UserAgent()))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) handleIPAllowlistByID(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/security/ip-allowlist/")
	if idStr == "" {
		w.WriteHeader(404)
		return
	}

	var id int64
	_, err := fmt.Sscan(idStr, &id)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		if err := a.deleteIPAllowlist(id); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = a.insertOpAudit("security.ip_allowlist.delete", "", "id="+idStr, clientIP(r), strings.TrimSpace(r.UserAgent()))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})

	case http.MethodPut:
		var req struct {
			Enabled *bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		if req.Enabled != nil {
			if err := a.toggleIPAllowlist(id, *req.Enabled); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}

		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) handleSecuritySettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ipAllowlistEnabled": a.ipAllowlistEnabled,
			"trustedProxies":     a.trustedProxies,
		})

	case http.MethodPost:
		var req struct {
			IPAllowlistEnabled *bool    `json:"ipAllowlistEnabled"`
			TrustedProxies     []string `json:"trustedProxies"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		if req.IPAllowlistEnabled != nil {
			a.ipAllowlistEnabled = *req.IPAllowlistEnabled
		}

		if req.TrustedProxies != nil {
			a.trustedProxies = req.TrustedProxies
		}

		_ = a.insertOpAudit("security.settings.update", "", "ipAllowlistEnabled="+fmt.Sprint(a.ipAllowlistEnabled), clientIP(r), strings.TrimSpace(r.UserAgent()))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":                 true,
			"ipAllowlistEnabled": a.ipAllowlistEnabled,
			"trustedProxies":     a.trustedProxies,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
