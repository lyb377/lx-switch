package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type App struct {
	db                  *sql.DB
	dataDir             string
	backupDir           string
	adminToken          string
	ipAllowlistEnabled  bool
	trustedProxies      []string
	failed              map[string]*attemptState
	mu                  sync.Mutex
	settingsMu          sync.RWMutex
	maxAttempts         int
	window              time.Duration
	lockout             time.Duration
	maxLoginBodyBytes   int64
	auditRetentionDays  int
	auditCleanupEnabled bool
}

type attemptState struct {
	Count       int
	FirstFailed time.Time
	LockUntil   time.Time
	LastFailed  time.Time
}

type Provider struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Target    string    `json:"target"` // openclaw|claude|codex|gemini
	BaseURL   string    `json:"baseUrl"`
	APIKey    string    `json:"apiKey"`
	Model     string    `json:"model"`
	Notes     string    `json:"notes"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ActivateReq struct {
	ProviderID int64 `json:"providerId"`
}

type LoginAudit struct {
	ID        int64     `json:"id"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"userAgent"`
	Success   bool      `json:"success"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"createdAt"`
}

type LoginAuditQuery struct {
	Limit  int
	Offset int
	From   string
	To     string
}

type OpAuditQuery struct {
	Limit  int
	Offset int
	Action string
	Target string
	From   string
	To     string
}

type ProviderQuery struct {
	Search string
	Target string
}

type OpAudit struct {
	ID        int64     `json:"id"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Detail    string    `json:"detail"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"userAgent"`
	CreatedAt time.Time `json:"createdAt"`
}

type SaveReq struct {
	Name    string `json:"name"`
	Target  string `json:"target"`
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
	Model   string `json:"model"`
	Notes   string `json:"notes"`
}

type importApplyResult struct {
	Mode        string           `json:"mode"`
	DryRun      bool             `json:"dryRun,omitempty"`
	Imported    int              `json:"imported"`
	Overwritten int              `json:"overwritten"`
	Skipped     int              `json:"skipped"`
	Details     []map[string]any `json:"details"`
	DetailCount int              `json:"detailCount"`
}

type CCImportRowReport struct {
	RowIndex     int    `json:"rowIndex"`
	Name         string `json:"name"`
	AppType      string `json:"appType"`
	MappedTarget string `json:"mappedTarget"`
	Status       string `json:"status"` // importable|skipped
	Reason       string `json:"reason"`
	BaseURL      string `json:"baseUrl"`
	Model        string `json:"model"`
}

type CCImportMappingReport struct {
	TotalRows      int                 `json:"totalRows"`
	ImportableRows int                 `json:"importableRows"`
	SkippedRows    int                 `json:"skippedRows"`
	TargetMapped   map[string]int      `json:"targetMapped"`
	Rows           []CCImportRowReport `json:"rows"`
}

type ProviderTestReq struct {
	ProviderID int64  `json:"providerId"`
	BaseURL    string `json:"baseUrl"`
	APIKey     string `json:"apiKey"`
}

type ProviderTestResult struct {
	ProviderID int64  `json:"providerId"`
	Name       string `json:"name"`
	Target     string `json:"target"`
	OK         bool   `json:"ok"`
	StatusCode int    `json:"statusCode"`
	Detail     string `json:"detail"`
}

type MetricsSummary struct {
	Window     string                 `json:"window"`
	Login      LoginMetrics           `json:"login"`
	Operations map[string]OpMetrics   `json:"operations"`
	ByTarget   map[string]int         `json:"byTarget"`
}

type LoginMetrics struct {
	Total       int     `json:"total"`
	Success     int     `json:"success"`
	Failed      int     `json:"failed"`
	SuccessRate float64 `json:"successRate"`
	UniqueIPs   int     `json:"uniqueIPs"`
}

type OpMetrics struct {
	Total       int     `json:"total"`
	Failed      int     `json:"failed"`
	FailureRate float64 `json:"failureRate"`
}

func main() {
	app, listen, err := newAppFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	srv := &http.Server{Addr: listen, Handler: logging(app.newMux())}
	log.Printf("lx-switch listening on %s, data=%s", listen, app.dataDir)
	if app.adminToken == "change-me-please" {
		log.Printf("WARNING: LX_SWITCH_TOKEN is default, please change it in service env")
	}
	log.Fatal(srv.ListenAndServe())
}

func newAppFromEnv() (*App, string, error) {
	dataDir := getenv("LX_SWITCH_DATA_DIR", "/var/lib/lx-switch")
	listen := getenv("LX_SWITCH_LISTEN", ":18777")
	token := os.Getenv("LX_SWITCH_TOKEN")
	if strings.TrimSpace(token) == "" {
		token = "change-me-please"
	}
	ipAllowlistEnabled := getenvBool("LX_SWITCH_IP_ALLOWLIST_ENABLED", false)
	trustedProxies := parseCommaList(os.Getenv("LX_SWITCH_TRUSTED_PROXIES"))

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, "", err
	}
	backupDir := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return nil, "", err
	}

	dbPath := filepath.Join(dataDir, "lx-switch.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, "", err
	}

	app := &App{
		db:                  db,
		dataDir:             dataDir,
		backupDir:           backupDir,
		adminToken:          token,
		ipAllowlistEnabled:  ipAllowlistEnabled,
		trustedProxies:      trustedProxies,
		failed:              map[string]*attemptState{},
		maxAttempts:         getenvInt("LX_SWITCH_MAX_LOGIN_ATTEMPTS", 6),
		window:              time.Duration(getenvInt("LX_SWITCH_LOGIN_WINDOW_SEC", 300)) * time.Second,
		lockout:             time.Duration(getenvInt("LX_SWITCH_LOGIN_LOCK_SEC", 900)) * time.Second,
		maxLoginBodyBytes:   int64(getenvInt("LX_SWITCH_LOGIN_MAX_BODY", 4096)),
		auditRetentionDays:  getenvInt("LX_SWITCH_AUDIT_RETENTION_DAYS", 30),
		auditCleanupEnabled: getenvBool("LX_SWITCH_AUDIT_CLEANUP_ENABLED", true),
	}
	if err := app.initDB(); err != nil {
		return nil, "", err
	}
	if err := app.initRBACSchema(); err != nil {
		return nil, "", err
	}
	app.loadSecuritySettings()
	if app.auditCleanupEnabled {
		go app.startAuditCleanupLoop()
	}
	go app.startSessionCleanupLoop()
	return app, listen, nil
}

func (a *App) newMux() *http.ServeMux {
	mux := http.NewServeMux()
	// Static files (Vite build output)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
	// Source files for Vite dev mode
	mux.Handle("/src/", http.StripPrefix("/src/", http.FileServer(http.Dir("web/src"))))
	mux.HandleFunc("/", a.withIPAllowlist(a.withPageAuth(a.handleIndex)))
	mux.HandleFunc("/login", a.withIPAllowlist(a.handleLogin))
	mux.HandleFunc("/logout", a.withIPAllowlist(a.withPageAuth(a.handleLogout)))
	mux.HandleFunc("/healthz", a.handleHealth)
	mux.HandleFunc("/api/providers", a.withIPAllowlist(a.withAuth(a.handleProviders)))
	mux.HandleFunc("/api/providers/import", a.withIPAllowlist(a.withAuth(a.handleProvidersImport)))
	mux.HandleFunc("/api/providers/import-cc", a.withIPAllowlist(a.withAuth(a.handleProvidersImportCCSwitch)))
	mux.HandleFunc("/api/providers/import-cc/report", a.withIPAllowlist(a.withAuth(a.handleProvidersImportCCSwitchReport)))
	mux.HandleFunc("/api/providers/export", a.withIPAllowlist(a.withAuth(a.handleProvidersExport)))
	mux.HandleFunc("/api/providers/test", a.withIPAllowlist(a.withAuth(a.handleProviderTest)))
	mux.HandleFunc("/api/providers/test-batch", a.withIPAllowlist(a.withAuth(a.handleProviderTestBatch)))
	mux.HandleFunc("/api/providers/", a.withIPAllowlist(a.withAuth(a.handleProviderByID)))
	mux.HandleFunc("/api/activate", a.withIPAllowlist(a.withAuth(a.handleActivate)))
	mux.HandleFunc("/api/backups", a.withIPAllowlist(a.withAuth(a.handleBackups)))
	mux.HandleFunc("/api/rollback", a.withIPAllowlist(a.withAuth(a.handleRollback)))
	mux.HandleFunc("/api/meta", a.withIPAllowlist(a.withAuth(a.handleMeta)))
	mux.HandleFunc("/api/login-audits", a.withIPAllowlist(a.withAuth(a.handleLoginAudits)))
	mux.HandleFunc("/api/login-audits/export", a.withIPAllowlist(a.withAuth(a.handleLoginAuditsExport)))
	mux.HandleFunc("/api/op-audits", a.withIPAllowlist(a.withAuth(a.handleOpAudits)))
	mux.HandleFunc("/api/op-audits/export", a.withIPAllowlist(a.withAuth(a.handleOpAuditsExport)))
	mux.HandleFunc("/api/audits/cleanup", a.withIPAllowlist(a.withAuth(a.handleAuditsCleanup)))
	mux.HandleFunc("/api/audits/settings", a.withIPAllowlist(a.withAuth(a.handleAuditSettings)))
	mux.HandleFunc("/api/metrics/dashboard", a.withIPAllowlist(a.withAuth(a.handleMetricsDashboard)))
	mux.HandleFunc("/api/metrics/export", a.withIPAllowlist(a.withAuth(a.handleMetricsExport)))
	mux.HandleFunc("/api/security/ip-allowlist", a.withIPAllowlist(a.withAuth(a.handleIPAllowlist)))
	mux.HandleFunc("/api/security/ip-allowlist/", a.withIPAllowlist(a.withAuth(a.handleIPAllowlistByID)))
	mux.HandleFunc("/api/security/settings", a.withIPAllowlist(a.withAuth(a.handleSecuritySettings)))

	// RBAC API routes
	mux.HandleFunc("/api/auth/login", a.withIPAllowlist(a.handleUserLogin))
	mux.HandleFunc("/api/auth/logout", a.withIPAllowlist(a.handleUserLogout))
	mux.HandleFunc("/api/users", a.withIPAllowlist(a.withRBACAuth(PermUsersRead, a.handleListUsers)))
	mux.HandleFunc("/api/users/create", a.withIPAllowlist(a.withRBACAuth(PermUsersWrite, a.handleCreateUser)))
	mux.HandleFunc("/api/users/update", a.withIPAllowlist(a.withRBACAuth(PermUsersWrite, a.handleUpdateUser)))
	mux.HandleFunc("/api/users/delete", a.withIPAllowlist(a.withRBACAuth(PermUsersWrite, a.handleDeleteUser)))
	mux.HandleFunc("/api/roles", a.withIPAllowlist(a.withRBACAuth(PermUsersRead, a.handleListRoles)))
	mux.HandleFunc("/api/totp/enable", a.withIPAllowlist(a.handleEnableTOTP))
	mux.HandleFunc("/api/totp/confirm", a.withIPAllowlist(a.handleConfirmTOTP))
	mux.HandleFunc("/api/totp/disable", a.withIPAllowlist(a.handleDisableTOTP))

	return mux
}

func (a *App) initDB() error {
	schema := `
CREATE TABLE IF NOT EXISTS providers (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  target TEXT NOT NULL,
  base_url TEXT,
  api_key TEXT,
  model TEXT,
  notes TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS state (
  k TEXT PRIMARY KEY,
  v TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS login_audits (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ip TEXT,
  user_agent TEXT,
  success INTEGER NOT NULL,
  reason TEXT,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS op_audits (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  action TEXT NOT NULL,
  target TEXT,
  detail TEXT,
  ip TEXT,
  user_agent TEXT,
  created_at TEXT NOT NULL
);
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

func parseCommaList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s != "" {
			out = append(out, s)
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	b, err := os.ReadFile("web/index.html")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "index template missing")
		return
	}
	_, _ = w.Write(b)
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, renderLoginPage(loginPageData{}))
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	ip := clientIP(r)
	ua := strings.TrimSpace(r.UserAgent())
	if wait, blocked := a.isBlocked(ip); blocked {
		_ = a.insertLoginAudit(ip, ua, false, "locked")
		retry := int(wait.Seconds()) + 1
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, renderLoginPage(loginPageData{
			Error:       "登录尝试过多，请稍后再试",
			RetryAfterS: retry,
		}))
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, a.maxLoginBodyBytes)
	if err := r.ParseForm(); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, renderLoginPage(loginPageData{Error: "请求格式不正确"}))
		return
	}
	t := strings.TrimSpace(r.FormValue("token"))
	if t != a.adminToken {
		remain, locked := a.recordFailure(ip)
		_ = a.insertLoginAudit(ip, ua, false, "bad_token")
		status := http.StatusUnauthorized
		errMsg := "token 错误"
		if locked {
			status = http.StatusTooManyRequests
			errMsg = "登录尝试过多，请稍后再试"
		}
		if status == http.StatusTooManyRequests && remain > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(remain))
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, renderLoginPage(loginPageData{Error: errMsg, RetryAfterS: remain}))
		return
	}
	a.clearFailures(ip)
	_ = a.insertLoginAudit(ip, ua, true, "ok")
	secure := a.requestSecure(r)
	http.SetCookie(w, &http.Cookie{
		Name:     "lx_token",
		Value:    t,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 3600,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "lx_token",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   a.requestSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (a *App) isBlocked(ip string) (time.Duration, bool) {
	if strings.TrimSpace(ip) == "" {
		return 0, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	st, ok := a.failed[ip]
	if !ok {
		return 0, false
	}
	if st.LockUntil.After(now) {
		return time.Until(st.LockUntil), true
	}
	if st.LastFailed.Add(a.window).Before(now) {
		delete(a.failed, ip)
	}
	return 0, false
}

func (a *App) recordFailure(ip string) (retryAfterS int, locked bool) {
	if strings.TrimSpace(ip) == "" {
		return 0, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	st, ok := a.failed[ip]
	if !ok || st.LastFailed.Add(a.window).Before(now) {
		st = &attemptState{Count: 1, FirstFailed: now, LastFailed: now}
		a.failed[ip] = st
		return 0, false
	}
	st.Count++
	st.LastFailed = now
	if st.Count >= a.maxAttempts {
		st.LockUntil = now.Add(a.lockout)
		return int(a.lockout.Seconds()) + 1, true
	}
	return 0, false
}

func (a *App) clearFailures(ip string) {
	if strings.TrimSpace(ip) == "" {
		return
	}
	a.mu.Lock()
	delete(a.failed, ip)
	a.mu.Unlock()
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ts": time.Now().Unix()})
}

func (a *App) handleMeta(w http.ResponseWriter, r *http.Request) {
	active, _ := a.getState("active_provider")
	cnt, _ := a.countProviders()
	_ = json.NewEncoder(w).Encode(map[string]any{
		"activeProvider":      active,
		"providerCount":       cnt,
		"firstRun":            cnt == 0,
		"tokenWeak":           a.adminToken == "change-me-please",
		"auditRetentionDays":  a.auditRetentionDays,
		"auditCleanupEnabled": a.auditCleanupEnabled,
	})
}

func (a *App) handleLoginAudits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := parseLoginAuditQuery(r)
	list, err := a.listLoginAudits(q)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	total, err := a.countLoginAudits(q)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items":  list,
		"total":  total,
		"limit":  q.Limit,
		"offset": q.Offset,
		"from":   q.From,
		"to":     q.To,
	})
}

func (a *App) handleLoginAuditsExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := parseLoginAuditQuery(r)
	q.Offset = 0
	q.Limit = parseIntRange(r.URL.Query().Get("limit"), 500, 1, 5000)
	list, err := a.listLoginAudits(q)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=login-audits-%s.csv", time.Now().Format("20060102-150405")))
	_, _ = io.WriteString(w, "id,created_at,ip,success,reason,user_agent\n")
	for _, it := range list {
		success := "0"
		if it.Success {
			success = "1"
		}
		line := fmt.Sprintf("%d,%s,%s,%s,%s,%s\n",
			it.ID,
			csvEsc(it.CreatedAt.Format(time.RFC3339)),
			csvEsc(it.IP),
			success,
			csvEsc(it.Reason),
			csvEsc(it.UserAgent),
		)
		_, _ = io.WriteString(w, line)
	}
}

func (a *App) handleOpAudits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := parseOpAuditQuery(r)
	list, err := a.listOpAudits(q)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	total, err := a.countOpAudits(q)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items":  list,
		"total":  total,
		"limit":  q.Limit,
		"offset": q.Offset,
		"action": q.Action,
		"target": q.Target,
		"from":   q.From,
		"to":     q.To,
	})
}

func (a *App) handleOpAuditsExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := parseOpAuditQuery(r)
	q.Offset = 0
	q.Limit = parseIntRange(r.URL.Query().Get("limit"), 500, 1, 5000)
	list, err := a.listOpAudits(q)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=op-audits-%s.csv", time.Now().Format("20060102-150405")))
	_, _ = io.WriteString(w, "id,created_at,action,target,detail,ip,user_agent\n")
	for _, it := range list {
		line := fmt.Sprintf("%d,%s,%s,%s,%s,%s,%s\n",
			it.ID,
			csvEsc(it.CreatedAt.Format(time.RFC3339)),
			csvEsc(it.Action),
			csvEsc(it.Target),
			csvEsc(it.Detail),
			csvEsc(it.IP),
			csvEsc(it.UserAgent),
		)
		_, _ = io.WriteString(w, line)
	}
}

func (a *App) handleAuditsCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	a.settingsMu.RLock()
	keepDefault := a.auditRetentionDays
	a.settingsMu.RUnlock()
	keepDays := parseIntRange(r.URL.Query().Get("keepDays"), keepDefault, 1, 3650)
	l1, l2, cutoff, err := a.cleanupAudits(keepDays)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = a.insertOpAudit("audit.cleanup", "audits", fmt.Sprintf("keepDays=%d loginDeleted=%d opDeleted=%d", keepDays, l1, l2), clientIP(r), strings.TrimSpace(r.UserAgent()))
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "keepDays": keepDays, "cutoff": cutoff, "loginDeleted": l1, "opDeleted": l2, "totalDeleted": l1 + l2})
}

func (a *App) cleanupAudits(keepDays int) (loginDeleted int64, opDeleted int64, cutoff string, err error) {
	cutoff = time.Now().Add(-time.Duration(keepDays) * 24 * time.Hour).Format(time.RFC3339)
	res1, err := a.db.Exec(`DELETE FROM login_audits WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, 0, cutoff, err
	}
	res2, err := a.db.Exec(`DELETE FROM op_audits WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, 0, cutoff, err
	}
	l1, _ := res1.RowsAffected()
	l2, _ := res2.RowsAffected()
	return l1, l2, cutoff, nil
}

func (a *App) startAuditCleanupLoop() {
	for {
		a.settingsMu.RLock()
		enabled := a.auditCleanupEnabled
		keep := a.auditRetentionDays
		a.settingsMu.RUnlock()
		if enabled && keep > 0 {
			l1, l2, cutoff, err := a.cleanupAudits(keep)
			if err != nil {
				log.Printf("audit cleanup failed: %v", err)
			} else if l1+l2 > 0 {
				_ = a.insertOpAudit("audit.cleanup.auto", "audits", fmt.Sprintf("keepDays=%d cutoff=%s loginDeleted=%d opDeleted=%d", keep, cutoff, l1, l2), "127.0.0.1", "auto-cleanup")
				log.Printf("audit cleanup auto: keep=%d deleted login=%d op=%d", keep, l1, l2)
			}
		}
		time.Sleep(24 * time.Hour)
	}
}

func (a *App) handleAuditSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.settingsMu.RLock()
		keep := a.auditRetentionDays
		enabled := a.auditCleanupEnabled
		a.settingsMu.RUnlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "auditRetentionDays": keep, "auditCleanupEnabled": enabled})
	case http.MethodPost:
		var req struct {
			AuditRetentionDays  int   `json:"auditRetentionDays"`
			AuditCleanupEnabled *bool `json:"auditCleanupEnabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		a.settingsMu.Lock()
		if req.AuditRetentionDays > 0 {
			a.auditRetentionDays = req.AuditRetentionDays
		}
		if req.AuditCleanupEnabled != nil {
			a.auditCleanupEnabled = *req.AuditCleanupEnabled
		}
		keep := a.auditRetentionDays
		enabled := a.auditCleanupEnabled
		a.settingsMu.Unlock()
		_ = a.insertOpAudit("audit.settings.update", "audits", fmt.Sprintf("keepDays=%d enabled=%v", keep, enabled), clientIP(r), strings.TrimSpace(r.UserAgent()))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "auditRetentionDays": keep, "auditCleanupEnabled": enabled})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) handleProviders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		q := ProviderQuery{
			Search: strings.TrimSpace(r.URL.Query().Get("search")),
			Target: strings.TrimSpace(r.URL.Query().Get("target")),
		}
		list, err := a.listProviders(q)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = json.NewEncoder(w).Encode(list)
	case http.MethodPost:
		var req SaveReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := validateSaveReq(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		now := time.Now().Format(time.RFC3339)
		res, err := a.db.Exec(`INSERT INTO providers(name,target,base_url,api_key,model,notes,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
			req.Name, req.Target, req.BaseURL, req.APIKey, req.Model, req.Notes, now, now)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		id, _ := res.LastInsertId()
		_ = a.insertOpAudit("provider.create", req.Target, fmt.Sprintf("id=%d name=%s", id, req.Name), clientIP(r), strings.TrimSpace(r.UserAgent()))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) handleProvidersImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Items        []SaveReq `json:"items"`
		Mode         string    `json:"mode"` // skip|overwrite
		DryRun       bool      `json:"dryRun"`
		PreviewLimit int       `json:"previewLimit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	res, err := a.applyImportItems(req.Items, req.Mode, req.DryRun, req.PreviewLimit)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	action := "provider.import"
	if req.DryRun {
		action = "provider.import.preview"
	}
	_ = a.insertOpAudit(action, "batch", fmt.Sprintf("mode=%s imported=%d overwritten=%d skipped=%d", res.Mode, res.Imported, res.Overwritten, res.Skipped), clientIP(r), strings.TrimSpace(r.UserAgent()))
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "dryRun": req.DryRun, "mode": res.Mode, "imported": res.Imported, "overwritten": res.Overwritten, "skipped": res.Skipped, "details": res.Details, "detailCount": res.DetailCount})
}

func (a *App) handleProvidersImportCCSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var req struct {
		SQL          string `json:"sql"`
		Mode         string `json:"mode"`
		DryRun       bool   `json:"dryRun"`
		PreviewLimit int    `json:"previewLimit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	items, report, err := parseCCSwitchProvidersFromSQL(req.SQL)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	res, err := a.applyImportItems(items, req.Mode, req.DryRun, req.PreviewLimit)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	action := "provider.import.ccswitch"
	if req.DryRun {
		action = "provider.import.ccswitch.preview"
	}
	reportJSON, _ := json.Marshal(report)
	_ = a.insertOpAudit(action, "batch", fmt.Sprintf("mode=%s parsed=%d imported=%d overwritten=%d skipped=%d report=%s", res.Mode, len(items), res.Imported, res.Overwritten, res.Skipped, trimForAudit(string(reportJSON))), clientIP(r), strings.TrimSpace(r.UserAgent()))
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "parsed": len(items), "dryRun": req.DryRun, "mode": res.Mode, "imported": res.Imported, "overwritten": res.Overwritten, "skipped": res.Skipped, "details": res.Details, "detailCount": res.DetailCount, "mappingReport": report})
}

func (a *App) handleProvidersImportCCSwitchReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var req struct {
		SQL string `json:"sql"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	_, report, err := parseCCSwitchProvidersFromSQL(req.SQL)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	stamp := time.Now().Format("20060102-150405")
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=cc-switch-mapping-report-%s.csv", stamp))
	_, _ = io.WriteString(w, "row_index,name,app_type,mapped_target,status,reason,base_url,model\n")
	for _, row := range report.Rows {
		line := fmt.Sprintf("%d,%s,%s,%s,%s,%s,%s,%s\n",
			row.RowIndex,
			csvEsc(row.Name),
			csvEsc(row.AppType),
			csvEsc(row.MappedTarget),
			csvEsc(row.Status),
			csvEsc(row.Reason),
			csvEsc(row.BaseURL),
			csvEsc(row.Model),
		)
		_, _ = io.WriteString(w, line)
	}
	_, _ = io.WriteString(w, "\nsummary_key,summary_value\n")
	_, _ = io.WriteString(w, fmt.Sprintf("total_rows,%d\n", report.TotalRows))
	_, _ = io.WriteString(w, fmt.Sprintf("importable_rows,%d\n", report.ImportableRows))
	_, _ = io.WriteString(w, fmt.Sprintf("skipped_rows,%d\n", report.SkippedRows))
	for k, v := range report.TargetMapped {
		_, _ = io.WriteString(w, fmt.Sprintf("target_%s,%d\n", k, v))
	}
}

func (a *App) handleProvidersExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := ProviderQuery{
		Search: strings.TrimSpace(r.URL.Query().Get("search")),
		Target: strings.TrimSpace(r.URL.Query().Get("target")),
	}
	list, err := a.listProviders(q)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=providers-%s.json", time.Now().Format("20060102-150405")))
	_ = json.NewEncoder(w).Encode(map[string]any{"items": list, "count": len(list)})
}

func (a *App) handleProviderTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req ProviderTestReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	baseURL := strings.TrimSpace(req.BaseURL)
	apiKey := strings.TrimSpace(req.APIKey)
	if req.ProviderID > 0 {
		p, err := a.getProvider(req.ProviderID)
		if err != nil {
			http.Error(w, "provider not found", 404)
			return
		}
		if baseURL == "" {
			baseURL = p.BaseURL
		}
		if apiKey == "" {
			apiKey = p.APIKey
		}
	}
	if baseURL == "" {
		http.Error(w, "baseUrl required", 400)
		return
	}
	code, detail, ok := testProviderConnectivity(baseURL, apiKey)
	if req.ProviderID > 0 {
		_ = a.insertOpAudit("provider.test", "id", fmt.Sprintf("providerId=%d ok=%v code=%d", req.ProviderID, ok, code), clientIP(r), strings.TrimSpace(r.UserAgent()))
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": ok, "statusCode": code, "detail": detail})
}

func (a *App) handleProviderTestBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := ProviderQuery{
		Search: strings.TrimSpace(r.URL.Query().Get("search")),
		Target: strings.TrimSpace(r.URL.Query().Get("target")),
	}
	list, err := a.listProviders(q)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	results := make([]ProviderTestResult, 0, len(list))
	okCount := 0
	for _, p := range list {
		code, detail, ok := testProviderConnectivity(p.BaseURL, p.APIKey)
		if ok {
			okCount++
		}
		results = append(results, ProviderTestResult{
			ProviderID: p.ID,
			Name:       p.Name,
			Target:     p.Target,
			OK:         ok,
			StatusCode: code,
			Detail:     trimForAudit(detail),
		})
	}
	_ = a.insertOpAudit("provider.test.batch", "batch", fmt.Sprintf("total=%d ok=%d fail=%d", len(list), okCount, len(list)-okCount), clientIP(r), strings.TrimSpace(r.UserAgent()))
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "total": len(list), "okCount": okCount, "failCount": len(list) - okCount, "items": results})
}

func (a *App) handleProviderByID(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/providers/")
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
	case http.MethodPut:
		var req SaveReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := validateSaveReq(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		_, err := a.db.Exec(`UPDATE providers SET name=?,target=?,base_url=?,api_key=?,model=?,notes=?,updated_at=? WHERE id=?`,
			req.Name, req.Target, req.BaseURL, req.APIKey, req.Model, req.Notes, time.Now().Format(time.RFC3339), id)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_ = a.insertOpAudit("provider.update", req.Target, fmt.Sprintf("id=%d name=%s", id, req.Name), clientIP(r), strings.TrimSpace(r.UserAgent()))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	case http.MethodDelete:
		p, _ := a.getProvider(id)
		_, err := a.db.Exec(`DELETE FROM providers WHERE id=?`, id)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		tgt := ""
		nm := ""
		if p != nil {
			tgt = p.Target
			nm = p.Name
		}
		_ = a.insertOpAudit("provider.delete", tgt, fmt.Sprintf("id=%d name=%s", id, nm), clientIP(r), strings.TrimSpace(r.UserAgent()))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) handleActivate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req ActivateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	p, err := a.getProvider(req.ProviderID)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	code, detail, ok := testProviderConnectivity(p.BaseURL, p.APIKey)
	if !ok {
		_ = a.insertOpAudit("provider.activate.blocked", p.Target, fmt.Sprintf("id=%d name=%s code=%d detail=%s", p.ID, p.Name, code, trimForAudit(detail)), clientIP(r), strings.TrimSpace(r.UserAgent()))
		http.Error(w, fmt.Sprintf("connectivity test failed: code=%d detail=%s", code, detail), 400)
		return
	}

	stamp := time.Now().Format("20060102-150405")
	bk, err := a.backupTargetConfig(p.Target, stamp)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := a.writeTargetConfig(p); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := a.setState("active_provider", fmt.Sprint(p.ID)); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = a.insertOpAudit("provider.activate", p.Target, fmt.Sprintf("id=%d name=%s backup=%s", p.ID, p.Name, bk), clientIP(r), strings.TrimSpace(r.UserAgent()))
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "backup": bk})
}

func (a *App) handleBackups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	entries, err := os.ReadDir(a.backupDir)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	type item struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	var out []item
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		st, _ := e.Info()
		out = append(out, item{Name: e.Name(), Size: st.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	_ = json.NewEncoder(w).Encode(out)
}

func (a *App) handleRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	name := filepath.Base(req.Name)
	if name == "." || name == "" {
		http.Error(w, "bad backup", 400)
		return
	}
	parts := strings.SplitN(name, "__", 2)
	if len(parts) != 2 {
		http.Error(w, "invalid backup name", 400)
		return
	}
	target := parts[0]
	dst, err := targetPath(target)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	src := filepath.Join(a.backupDir, name)
	b, err := os.ReadFile(src)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = a.insertOpAudit("backup.rollback", target, fmt.Sprintf("backup=%s restored=%s", name, dst), clientIP(r), strings.TrimSpace(r.UserAgent()))
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "restored": dst})
}

func (a *App) countProviders() (int, error) {
	var n int
	err := a.db.QueryRow(`SELECT COUNT(1) FROM providers`).Scan(&n)
	return n, err
}

func (a *App) insertLoginAudit(ip, ua string, success bool, reason string) error {
	s := 0
	if success {
		s = 1
	}
	_, err := a.db.Exec(`INSERT INTO login_audits(ip,user_agent,success,reason,created_at) VALUES(?,?,?,?,?)`,
		ip, ua, s, reason, time.Now().Format(time.RFC3339))
	return err
}

func (a *App) countLoginAudits(q LoginAuditQuery) (int, error) {
	var n int
	query := `SELECT COUNT(1) FROM login_audits WHERE 1=1`
	args := []any{}
	if q.From != "" {
		query += ` AND created_at >= ?`
		args = append(args, q.From)
	}
	if q.To != "" {
		query += ` AND created_at <= ?`
		args = append(args, q.To)
	}
	err := a.db.QueryRow(query, args...).Scan(&n)
	return n, err
}

func (a *App) listLoginAudits(q LoginAuditQuery) ([]LoginAudit, error) {
	query := `SELECT id,ip,user_agent,success,reason,created_at FROM login_audits WHERE 1=1`
	args := []any{}
	if q.From != "" {
		query += ` AND created_at >= ?`
		args = append(args, q.From)
	}
	if q.To != "" {
		query += ` AND created_at <= ?`
		args = append(args, q.To)
	}
	query += ` ORDER BY id DESC LIMIT ? OFFSET ?`
	args = append(args, q.Limit, q.Offset)
	rows, err := a.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LoginAudit
	for rows.Next() {
		var it LoginAudit
		var ok int
		var ts string
		if err := rows.Scan(&it.ID, &it.IP, &it.UserAgent, &ok, &it.Reason, &ts); err != nil {
			return nil, err
		}
		it.Success = ok == 1
		it.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		out = append(out, it)
	}
	if out == nil {
		out = []LoginAudit{}
	}
	return out, nil
}

func (a *App) insertOpAudit(action, target, detail, ip, ua string) error {
	_, err := a.db.Exec(`INSERT INTO op_audits(action,target,detail,ip,user_agent,created_at) VALUES(?,?,?,?,?,?)`,
		action, target, detail, ip, ua, time.Now().Format(time.RFC3339))
	return err
}

func (a *App) countOpAudits(q OpAuditQuery) (int, error) {
	var n int
	query := `SELECT COUNT(1) FROM op_audits WHERE 1=1`
	args := []any{}
	if q.Action != "" {
		query += ` AND action=?`
		args = append(args, q.Action)
	}
	if q.Target != "" {
		query += ` AND target=?`
		args = append(args, q.Target)
	}
	if q.From != "" {
		query += ` AND created_at >= ?`
		args = append(args, q.From)
	}
	if q.To != "" {
		query += ` AND created_at <= ?`
		args = append(args, q.To)
	}
	err := a.db.QueryRow(query, args...).Scan(&n)
	return n, err
}

func (a *App) listOpAudits(q OpAuditQuery) ([]OpAudit, error) {
	query := `SELECT id,action,target,detail,ip,user_agent,created_at FROM op_audits WHERE 1=1`
	args := []any{}
	if q.Action != "" {
		query += ` AND action=?`
		args = append(args, q.Action)
	}
	if q.Target != "" {
		query += ` AND target=?`
		args = append(args, q.Target)
	}
	if q.From != "" {
		query += ` AND created_at >= ?`
		args = append(args, q.From)
	}
	if q.To != "" {
		query += ` AND created_at <= ?`
		args = append(args, q.To)
	}
	query += ` ORDER BY id DESC LIMIT ? OFFSET ?`
	args = append(args, q.Limit, q.Offset)
	rows, err := a.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OpAudit
	for rows.Next() {
		var it OpAudit
		var ts string
		if err := rows.Scan(&it.ID, &it.Action, &it.Target, &it.Detail, &it.IP, &it.UserAgent, &ts); err != nil {
			return nil, err
		}
		it.CreatedAt, _ = time.Parse(time.RFC3339, ts)
		out = append(out, it)
	}
	if out == nil {
		out = []OpAudit{}
	}
	return out, nil
}

func (a *App) getMetricsSummary(duration, window string) (*MetricsSummary, error) {
	// Login metrics
	loginQuery := `
		SELECT
			COUNT(*) as total,
			SUM(CASE WHEN success = 1 THEN 1 ELSE 0 END) as success,
			COUNT(DISTINCT ip) as unique_ips
		FROM login_audits
		WHERE created_at >= datetime('now', ?)
	`
	var loginTotal, loginSuccess, uniqueIPs int
	err := a.db.QueryRow(loginQuery, duration).Scan(&loginTotal, &loginSuccess, &uniqueIPs)
	if err != nil {
		return nil, err
	}

	loginFailed := loginTotal - loginSuccess
	var successRate float64
	if loginTotal > 0 {
		successRate = float64(loginSuccess) / float64(loginTotal) * 100
	}

	// Operation metrics by action
	opQuery := `
		SELECT
			action,
			COUNT(*) as total,
			SUM(CASE WHEN detail LIKE '%失败%' OR detail LIKE '%error%' OR detail LIKE '%failed%' THEN 1 ELSE 0 END) as failed
		FROM op_audits
		WHERE created_at >= datetime('now', ?)
		GROUP BY action
	`
	rows, err := a.db.Query(opQuery, duration)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	operations := make(map[string]OpMetrics)
	for rows.Next() {
		var action string
		var total, failed int
		if err := rows.Scan(&action, &total, &failed); err != nil {
			return nil, err
		}
		var failureRate float64
		if total > 0 {
			failureRate = float64(failed) / float64(total) * 100
		}
		operations[action] = OpMetrics{
			Total:       total,
			Failed:      failed,
			FailureRate: failureRate,
		}
	}

	// By target (activate actions only)
	targetQuery := `
		SELECT
			target,
			COUNT(*) as count
		FROM op_audits
		WHERE created_at >= datetime('now', ?)
			AND action = 'activate'
			AND target IN ('openclaw', 'claude', 'codex', 'gemini')
		GROUP BY target
	`
	targetRows, err := a.db.Query(targetQuery, duration)
	if err != nil {
		return nil, err
	}
	defer targetRows.Close()

	byTarget := make(map[string]int)
	for targetRows.Next() {
		var target string
		var count int
		if err := targetRows.Scan(&target, &count); err != nil {
			return nil, err
		}
		byTarget[target] = count
	}

	return &MetricsSummary{
		Window: window,
		Login: LoginMetrics{
			Total:       loginTotal,
			Success:     loginSuccess,
			Failed:      loginFailed,
			SuccessRate: successRate,
			UniqueIPs:   uniqueIPs,
		},
		Operations: operations,
		ByTarget:   byTarget,
	}, nil
}

func (a *App) listProviders(q ProviderQuery) ([]Provider, error) {
	query := `SELECT id,name,target,base_url,api_key,model,notes,created_at,updated_at FROM providers WHERE 1=1`
	args := []any{}
	if q.Target != "" {
		query += ` AND target=?`
		args = append(args, q.Target)
	}
	if q.Search != "" {
		query += ` AND (name LIKE ? OR model LIKE ? OR base_url LIKE ? OR notes LIKE ?)`
		kw := "%" + q.Search + "%"
		args = append(args, kw, kw, kw, kw)
	}
	query += ` ORDER BY id DESC`
	rows, err := a.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Provider
	for rows.Next() {
		var p Provider
		var c, u string
		if err := rows.Scan(&p.ID, &p.Name, &p.Target, &p.BaseURL, &p.APIKey, &p.Model, &p.Notes, &c, &u); err != nil {
			return nil, err
		}
		p.CreatedAt, _ = time.Parse(time.RFC3339, c)
		p.UpdatedAt, _ = time.Parse(time.RFC3339, u)
		out = append(out, p)
	}
	if out == nil {
		out = []Provider{}
	}
	return out, nil
}

func normalizeMode(mode string) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "skip"
	}
	if mode != "skip" && mode != "overwrite" {
		return "", errors.New("mode must be skip or overwrite")
	}
	return mode, nil
}

func (a *App) applyImportItems(items []SaveReq, mode string, dryRun bool, previewLimit int) (*importApplyResult, error) {
	if len(items) == 0 {
		return nil, errors.New("items required")
	}
	if len(items) > 200 {
		return nil, errors.New("too many items (max 200)")
	}
	m, err := normalizeMode(mode)
	if err != nil {
		return nil, err
	}
	if previewLimit <= 0 {
		previewLimit = 30
	}
	if previewLimit > 200 {
		previewLimit = 200
	}

	tx, err := a.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	now := time.Now().Format(time.RFC3339)
	res := &importApplyResult{Mode: m, DryRun: dryRun, Details: []map[string]any{}}
	for i := range items {
		it := items[i]
		if err := validateSaveReq(&it); err != nil {
			return nil, fmt.Errorf("item[%d]: %v", i, err)
		}
		existingID, exists, err := findProviderIDByNameTargetTx(tx, it.Name, it.Target)
		if err != nil {
			return nil, err
		}
		if exists {
			if m == "skip" {
				res.Skipped++
				if len(res.Details) < previewLimit {
					res.Details = append(res.Details, map[string]any{"index": i, "name": it.Name, "target": it.Target, "action": "skip", "existingId": existingID})
				}
				continue
			}
			res.Overwritten++
			if len(res.Details) < previewLimit {
				res.Details = append(res.Details, map[string]any{"index": i, "name": it.Name, "target": it.Target, "action": "overwrite", "existingId": existingID})
			}
			if dryRun {
				continue
			}
			if _, err := tx.Exec(`UPDATE providers SET base_url=?,api_key=?,model=?,notes=?,updated_at=? WHERE id=?`,
				it.BaseURL, it.APIKey, it.Model, it.Notes, now, existingID); err != nil {
				return nil, err
			}
			continue
		}
		res.Imported++
		if len(res.Details) < previewLimit {
			res.Details = append(res.Details, map[string]any{"index": i, "name": it.Name, "target": it.Target, "action": "insert"})
		}
		if dryRun {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO providers(name,target,base_url,api_key,model,notes,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
			it.Name, it.Target, it.BaseURL, it.APIKey, it.Model, it.Notes, now, now); err != nil {
			return nil, err
		}
	}
	if dryRun {
		_ = tx.Rollback()
		res.DetailCount = len(res.Details)
		return res, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	res.DetailCount = len(res.Details)
	return res, nil
}

func parseCCSwitchProvidersFromSQL(sqlText string) ([]SaveReq, CCImportMappingReport, error) {
	sqlText = strings.TrimSpace(sqlText)
	report := CCImportMappingReport{TargetMapped: map[string]int{}, Rows: []CCImportRowReport{}}
	if sqlText == "" {
		return nil, report, errors.New("sql required")
	}
	if !strings.Contains(strings.ToLower(sqlText), "insert into") || !strings.Contains(strings.ToLower(sqlText), "providers") {
		return nil, report, errors.New("no providers INSERT found in SQL")
	}
	stmts := parseCCProviderInsertStatements(sqlText)
	if len(stmts) == 0 {
		return nil, report, errors.New("no providers rows parsed")
	}
	out := []SaveReq{}
	rowIdx := 0
	for _, st := range stmts {
		cols := parseSQLColumns(st.ColumnsRaw)
		if len(cols) == 0 {
			continue
		}
		tuples := splitSQLTuples(st.ValuesRaw)
		for _, tuple := range tuples {
			rowIdx++
			rec := CCImportRowReport{RowIndex: rowIdx}
			vals := splitSQLValues(tuple)
			if len(vals) == 0 || len(vals) != len(cols) {
				rec.Status = "skipped"
				rec.Reason = "columns_values_mismatch"
				report.Rows = append(report.Rows, rec)
				continue
			}
			row := map[string]string{}
			for i := range cols {
				row[strings.ToLower(strings.TrimSpace(cols[i]))] = unquoteSQLValue(vals[i])
			}
			rec.Name = strings.TrimSpace(row["name"])
			rec.AppType = strings.TrimSpace(row["app_type"])
			if rec.Name == "" {
				rec.Name = strings.TrimSpace(row["id"])
			}
			if rec.AppType == "" {
				rec.Status = "skipped"
				rec.Reason = "missing_app_type"
				report.Rows = append(report.Rows, rec)
				continue
			}
			target := mapCCAppTypeToTarget(rec.AppType)
			rec.MappedTarget = target
			if target == "" {
				rec.Status = "skipped"
				rec.Reason = "unsupported_app_type"
				report.Rows = append(report.Rows, rec)
				continue
			}
			baseURL, apiKey, model := extractCCSettings(strings.TrimSpace(row["settings_config"]))
			rec.BaseURL = baseURL
			rec.Model = model
			if strings.TrimSpace(rec.Name) == "" {
				rec.Status = "skipped"
				rec.Reason = "missing_name"
				report.Rows = append(report.Rows, rec)
				continue
			}
			if strings.TrimSpace(baseURL) == "" {
				rec.Status = "skipped"
				rec.Reason = "missing_base_url"
				report.Rows = append(report.Rows, rec)
				continue
			}
			notes := strings.TrimSpace(row["notes"])
			out = append(out, SaveReq{Name: rec.Name, Target: target, BaseURL: baseURL, APIKey: apiKey, Model: model, Notes: notes})
			rec.Status = "importable"
			rec.Reason = "ok"
			report.TargetMapped[target]++
			report.Rows = append(report.Rows, rec)
		}
	}
	report.TotalRows = len(report.Rows)
	report.ImportableRows = len(out)
	report.SkippedRows = report.TotalRows - report.ImportableRows
	if len(out) == 0 {
		return nil, report, errors.New("parsed zero importable providers from SQL")
	}
	return out, report, nil
}

type ccProviderInsertStmt struct {
	ColumnsRaw string
	ValuesRaw  string
}

func parseCCProviderInsertStatements(sqlText string) []ccProviderInsertStmt {
	re := regexp.MustCompile(`(?is)INSERT\s+INTO\s+["\x60\[]?providers["\x60\]]?\s*\((.*?)\)\s*VALUES\s*(.*?);`)
	ms := re.FindAllStringSubmatch(sqlText, -1)
	out := make([]ccProviderInsertStmt, 0, len(ms))
	for _, m := range ms {
		if len(m) < 3 {
			continue
		}
		out = append(out, ccProviderInsertStmt{ColumnsRaw: m[1], ValuesRaw: m[2]})
	}
	return out
}

func parseSQLColumns(raw string) []string {
	parts := splitSQLValues(raw)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		x := strings.TrimSpace(p)
		x = strings.Trim(x, "\"")
		x = strings.Trim(x, "`")
		x = strings.TrimPrefix(x, "[")
		x = strings.TrimSuffix(x, "]")
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

func splitSQLTuples(raw string) []string {
	out := []string{}
	raw = strings.TrimSpace(raw)
	start := -1
	depth := 0
	inSingle := false
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if ch == '\'' {
			if inSingle && i+1 < len(raw) && raw[i+1] == '\'' {
				i++
				continue
			}
			inSingle = !inSingle
			continue
		}
		if inSingle {
			continue
		}
		if ch == '(' {
			if depth == 0 {
				start = i + 1
			}
			depth++
			continue
		}
		if ch == ')' {
			if depth > 0 {
				depth--
				if depth == 0 && start >= 0 && start <= i {
					out = append(out, strings.TrimSpace(raw[start:i]))
					start = -1
				}
			}
		}
	}
	return out
}

func splitSQLValues(raw string) []string {
	out := []string{}
	var b strings.Builder
	inSingle := false
	inDouble := false
	depth := 0
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if ch == '\'' && !inDouble {
			if inSingle && i+1 < len(raw) && raw[i+1] == '\'' {
				b.WriteByte('\'')
				i++
				continue
			}
			inSingle = !inSingle
			b.WriteByte(ch)
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			b.WriteByte(ch)
			continue
		}
		if !inSingle && !inDouble {
			if ch == '(' {
				depth++
			} else if ch == ')' && depth > 0 {
				depth--
			}
			if ch == ',' && depth == 0 {
				out = append(out, strings.TrimSpace(b.String()))
				b.Reset()
				continue
			}
		}
		b.WriteByte(ch)
	}
	if s := strings.TrimSpace(b.String()); s != "" {
		out = append(out, s)
	}
	return out
}

func unquoteSQLValue(v string) string {
	v = strings.TrimSpace(v)
	if strings.EqualFold(v, "NULL") {
		return ""
	}
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		v = v[1 : len(v)-1]
		v = strings.ReplaceAll(v, `''`, `'`)
		return v
	}
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return strings.TrimSpace(v[1 : len(v)-1])
	}
	return v
}

func mapCCAppTypeToTarget(appType string) string {
	switch strings.ToLower(strings.TrimSpace(appType)) {
	case "claude":
		return "claude"
	case "codex":
		return "codex"
	case "gemini":
		return "gemini"
	case "opencode":
		return "openclaw"
	default:
		return ""
	}
}

func extractCCSettings(raw string) (baseURL, apiKey, model string) {
	if strings.TrimSpace(raw) == "" {
		return "", "", ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return "", "", ""
	}
	baseURL = firstString(m, "baseUrl", "base_url", "url", "endpoint", "apiBase", "api_base")
	apiKey = firstString(m, "apiKey", "api_key", "token", "access_token")
	model = firstString(m, "model", "model_id", "defaultModel")
	return strings.TrimSpace(baseURL), strings.TrimSpace(apiKey), strings.TrimSpace(model)
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			s, _ := v.(string)
			if strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}

func findProviderIDByNameTargetTx(tx *sql.Tx, name, target string) (int64, bool, error) {
	var id int64
	err := tx.QueryRow(`SELECT id FROM providers WHERE name=? AND target=? LIMIT 1`, name, target).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

func testProviderConnectivity(baseURL, apiKey string) (int, string, bool) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return 0, "empty baseUrl", false
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return 0, "invalid baseUrl", false
	}
	candidate := strings.TrimRight(baseURL, "/")
	if !strings.Contains(candidate, "/v1") {
		candidate = candidate + "/v1"
	}
	endpoint := strings.TrimRight(candidate, "/") + "/models"

	client := &http.Client{Timeout: 8 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, endpoint, nil)
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err.Error(), false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.StatusCode, "ok", true
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	detail := strings.TrimSpace(string(b))
	if detail == "" {
		detail = resp.Status
	}
	return resp.StatusCode, detail, false
}

func (a *App) getProvider(id int64) (*Provider, error) {
	var p Provider
	var c, u string
	err := a.db.QueryRow(`SELECT id,name,target,base_url,api_key,model,notes,created_at,updated_at FROM providers WHERE id=?`, id).
		Scan(&p.ID, &p.Name, &p.Target, &p.BaseURL, &p.APIKey, &p.Model, &p.Notes, &c, &u)
	if err != nil {
		return nil, err
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339, c)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, u)
	return &p, nil
}

func (a *App) setState(k, v string) error {
	_, err := a.db.Exec(`INSERT INTO state(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}
func (a *App) getState(k string) (string, error) {
	var v string
	err := a.db.QueryRow(`SELECT v FROM state WHERE k=?`, k).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func targetPath(target string) (string, error) {
	switch target {
	case "openclaw":
		return "/root/.openclaw/config.json", nil
	case "claude":
		return "/root/.claude.json", nil
	case "codex":
		return "/root/.codex/config.toml", nil
	case "gemini":
		return "/root/.gemini/config.json", nil
	default:
		return "", errors.New("unsupported target: " + target)
	}
}

func (a *App) backupTargetConfig(target, stamp string) (string, error) {
	src, err := targetPath(target)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s__%s.bak", target, stamp)
	dst := filepath.Join(a.backupDir, name)
	b, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			_ = os.WriteFile(dst, []byte(""), 0o600)
			return name, nil
		}
		return "", err
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		return "", err
	}
	return name, nil
}

func (a *App) writeTargetConfig(p *Provider) error {
	dst, err := targetPath(p.Target)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	switch p.Target {
	case "codex":
		content := ""
		if p.BaseURL != "" {
			content += fmt.Sprintf("base_url = \"%s\"\n", p.BaseURL)
		}
		if p.APIKey != "" {
			content += fmt.Sprintf("api_key = \"%s\"\n", p.APIKey)
		}
		if p.Model != "" {
			content += fmt.Sprintf("model = \"%s\"\n", p.Model)
		}
		if content == "" {
			content = "# managed by lx-switch\n"
		}
		return os.WriteFile(dst, []byte(content), 0o600)
	default:
		m := map[string]any{
			"managedBy": "lx-switch",
			"name":      p.Name,
			"target":    p.Target,
			"baseUrl":   p.BaseURL,
			"apiKey":    p.APIKey,
			"model":     p.Model,
			"notes":     p.Notes,
			"updatedAt": time.Now().Format(time.RFC3339),
		}
		b, _ := json.MarshalIndent(m, "", "  ")
		return os.WriteFile(dst, b, 0o600)
	}
}

func validateSaveReq(req *SaveReq) error {
	req.Name = strings.TrimSpace(req.Name)
	req.Target = strings.TrimSpace(req.Target)
	req.BaseURL = strings.TrimSpace(req.BaseURL)
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.Model = strings.TrimSpace(req.Model)
	req.Notes = strings.TrimSpace(req.Notes)

	if req.Name == "" || req.Target == "" {
		return errors.New("name/target required")
	}
	if _, err := targetPath(req.Target); err != nil {
		return err
	}
	if req.BaseURL != "" {
		u, err := url.Parse(req.BaseURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return errors.New("invalid baseUrl")
		}
		s := strings.ToLower(u.Scheme)
		if s != "http" && s != "https" {
			return errors.New("baseUrl must start with http:// or https://")
		}
	}
	return nil
}

func csvEsc(s string) string {
	s = strings.ReplaceAll(s, `"`, `""`)
	return `"` + s + `"`
}

func trimForAudit(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 160 {
		return s[:160] + "..."
	}
	return s
}

func parseLoginAuditQuery(r *http.Request) LoginAuditQuery {
	return LoginAuditQuery{
		Limit:  parseIntRange(r.URL.Query().Get("limit"), 50, 1, 200),
		Offset: parseIntRange(r.URL.Query().Get("offset"), 0, 0, 1_000_000),
		From:   parseDatePrefixRFC3339(strings.TrimSpace(r.URL.Query().Get("from")), false),
		To:     parseDatePrefixRFC3339(strings.TrimSpace(r.URL.Query().Get("to")), true),
	}
}

func parseOpAuditQuery(r *http.Request) OpAuditQuery {
	q := OpAuditQuery{
		Limit:  parseIntRange(r.URL.Query().Get("limit"), 50, 1, 200),
		Offset: parseIntRange(r.URL.Query().Get("offset"), 0, 0, 1_000_000),
		Action: strings.TrimSpace(r.URL.Query().Get("action")),
		Target: strings.TrimSpace(r.URL.Query().Get("target")),
		From:   parseDatePrefixRFC3339(strings.TrimSpace(r.URL.Query().Get("from")), false),
		To:     parseDatePrefixRFC3339(strings.TrimSpace(r.URL.Query().Get("to")), true),
	}
	if len(q.Action) > 64 {
		q.Action = q.Action[:64]
	}
	if len(q.Target) > 64 {
		q.Target = q.Target[:64]
	}
	return q
}

type loginPageData struct {
	Error       string
	RetryAfterS int
}

func renderLoginPage(d loginPageData) string {
	errBlock := ""
	if d.Error != "" {
		msg := html.EscapeString(d.Error)
		retry := ""
		if d.RetryAfterS > 0 {
			retry = fmt.Sprintf("（约 %d 秒后可重试）", d.RetryAfterS)
		}
		errBlock = fmt.Sprintf(`<div class="err">%s %s</div>`, msg, retry)
	}
	b, err := os.ReadFile("web/login.html")
	if err != nil {
		// fallback
		return "login template missing"
	}
	return strings.ReplaceAll(string(b), "{{ERROR_BLOCK}}", errBlock)
}



