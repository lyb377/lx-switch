package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type App struct {
	db                 *sql.DB
	dataDir            string
	backupDir          string
	adminToken         string
	failed             map[string]*attemptState
	mu                 sync.Mutex
	maxAttempts        int
	window             time.Duration
	lockout            time.Duration
	maxLoginBodyBytes  int64
	auditRetentionDays int
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

type OpAuditQuery struct {
	Limit  int
	Offset int
	Action string
	Target string
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

func main() {
	dataDir := getenv("LX_SWITCH_DATA_DIR", "/var/lib/lx-switch")
	listen := getenv("LX_SWITCH_LISTEN", ":18777")
	token := os.Getenv("LX_SWITCH_TOKEN")
	if strings.TrimSpace(token) == "" {
		token = "change-me-please"
	}

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatal(err)
	}
	backupDir := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		log.Fatal(err)
	}

	dbPath := filepath.Join(dataDir, "lx-switch.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatal(err)
	}

	app := &App{
		db:                 db,
		dataDir:            dataDir,
		backupDir:          backupDir,
		adminToken:         token,
		failed:             map[string]*attemptState{},
		maxAttempts:        getenvInt("LX_SWITCH_MAX_LOGIN_ATTEMPTS", 6),
		window:             time.Duration(getenvInt("LX_SWITCH_LOGIN_WINDOW_SEC", 300)) * time.Second,
		lockout:            time.Duration(getenvInt("LX_SWITCH_LOGIN_LOCK_SEC", 900)) * time.Second,
		maxLoginBodyBytes:  int64(getenvInt("LX_SWITCH_LOGIN_MAX_BODY", 4096)),
		auditRetentionDays: getenvInt("LX_SWITCH_AUDIT_RETENTION_DAYS", 30),
	}
	if err := app.initDB(); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", app.withPageAuth(app.handleIndex))
	mux.HandleFunc("/login", app.handleLogin)
	mux.HandleFunc("/logout", app.withPageAuth(app.handleLogout))
	mux.HandleFunc("/healthz", app.handleHealth)
	mux.HandleFunc("/api/providers", app.withAuth(app.handleProviders))
	mux.HandleFunc("/api/providers/import", app.withAuth(app.handleProvidersImport))
	mux.HandleFunc("/api/providers/export", app.withAuth(app.handleProvidersExport))
	mux.HandleFunc("/api/providers/test", app.withAuth(app.handleProviderTest))
	mux.HandleFunc("/api/providers/test-batch", app.withAuth(app.handleProviderTestBatch))
	mux.HandleFunc("/api/providers/", app.withAuth(app.handleProviderByID))
	mux.HandleFunc("/api/activate", app.withAuth(app.handleActivate))
	mux.HandleFunc("/api/backups", app.withAuth(app.handleBackups))
	mux.HandleFunc("/api/rollback", app.withAuth(app.handleRollback))
	mux.HandleFunc("/api/meta", app.withAuth(app.handleMeta))
	mux.HandleFunc("/api/login-audits", app.withAuth(app.handleLoginAudits))
	mux.HandleFunc("/api/login-audits/export", app.withAuth(app.handleLoginAuditsExport))
	mux.HandleFunc("/api/op-audits", app.withAuth(app.handleOpAudits))
	mux.HandleFunc("/api/op-audits/export", app.withAuth(app.handleOpAuditsExport))
	mux.HandleFunc("/api/audits/cleanup", app.withAuth(app.handleAuditsCleanup))

	srv := &http.Server{Addr: listen, Handler: logging(mux)}
	log.Printf("lx-switch listening on %s, data=%s", listen, dataDir)
	if token == "change-me-please" {
		log.Printf("WARNING: LX_SWITCH_TOKEN is default, please change it in service env")
	}
	log.Fatal(srv.ListenAndServe())
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
`
	_, err := a.db.Exec(schema)
	return err
}

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

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, indexHTML)
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
		"activeProvider":     active,
		"providerCount":      cnt,
		"firstRun":           cnt == 0,
		"tokenWeak":          a.adminToken == "change-me-please",
		"auditRetentionDays": a.auditRetentionDays,
	})
}

func (a *App) handleLoginAudits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	limit := parseIntRange(r.URL.Query().Get("limit"), 50, 1, 200)
	offset := parseIntRange(r.URL.Query().Get("offset"), 0, 0, 1_000_000)
	list, err := a.listLoginAudits(limit, offset)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	total, err := a.countLoginAudits()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items":  list,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (a *App) handleLoginAuditsExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	limit := parseIntRange(r.URL.Query().Get("limit"), 500, 1, 5000)
	list, err := a.listLoginAudits(limit, 0)
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
	keepDays := parseIntRange(r.URL.Query().Get("keepDays"), a.auditRetentionDays, 1, 3650)
	cutoff := time.Now().Add(-time.Duration(keepDays) * 24 * time.Hour).Format(time.RFC3339)

	res1, err := a.db.Exec(`DELETE FROM login_audits WHERE created_at < ?`, cutoff)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	res2, err := a.db.Exec(`DELETE FROM op_audits WHERE created_at < ?`, cutoff)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	l1, _ := res1.RowsAffected()
	l2, _ := res2.RowsAffected()
	_ = a.insertOpAudit("audit.cleanup", "audits", fmt.Sprintf("keepDays=%d loginDeleted=%d opDeleted=%d", keepDays, l1, l2), clientIP(r), strings.TrimSpace(r.UserAgent()))
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "keepDays": keepDays, "cutoff": cutoff, "loginDeleted": l1, "opDeleted": l2, "totalDeleted": l1 + l2})
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
	if len(req.Items) == 0 {
		http.Error(w, "items required", 400)
		return
	}
	if len(req.Items) > 200 {
		http.Error(w, "too many items (max 200)", 400)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "skip"
	}
	if mode != "skip" && mode != "overwrite" {
		http.Error(w, "mode must be skip or overwrite", 400)
		return
	}
	previewLimit := req.PreviewLimit
	if previewLimit <= 0 {
		previewLimit = 30
	}
	if previewLimit > 200 {
		previewLimit = 200
	}

	tx, err := a.db.Begin()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer tx.Rollback()

	now := time.Now().Format(time.RFC3339)
	imported := 0
	overwritten := 0
	skipped := 0
	details := []map[string]any{}
	for i := range req.Items {
		it := req.Items[i]
		if err := validateSaveReq(&it); err != nil {
			http.Error(w, fmt.Sprintf("item[%d]: %v", i, err), 400)
			return
		}
		existingID, exists, err := findProviderIDByNameTargetTx(tx, it.Name, it.Target)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if exists {
			if mode == "skip" {
				skipped++
				if len(details) < previewLimit {
					details = append(details, map[string]any{"index": i, "name": it.Name, "target": it.Target, "action": "skip", "existingId": existingID})
				}
				continue
			}
			overwritten++
			if len(details) < previewLimit {
				details = append(details, map[string]any{"index": i, "name": it.Name, "target": it.Target, "action": "overwrite", "existingId": existingID})
			}
			if req.DryRun {
				continue
			}
			if _, err := tx.Exec(`UPDATE providers SET base_url=?,api_key=?,model=?,notes=?,updated_at=? WHERE id=?`,
				it.BaseURL, it.APIKey, it.Model, it.Notes, now, existingID); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			continue
		}
		imported++
		if len(details) < previewLimit {
			details = append(details, map[string]any{"index": i, "name": it.Name, "target": it.Target, "action": "insert"})
		}
		if req.DryRun {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO providers(name,target,base_url,api_key,model,notes,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
			it.Name, it.Target, it.BaseURL, it.APIKey, it.Model, it.Notes, now, now); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	if req.DryRun {
		_ = tx.Rollback()
		_ = a.insertOpAudit("provider.import.preview", "batch", fmt.Sprintf("mode=%s imported=%d overwritten=%d skipped=%d", mode, imported, overwritten, skipped), clientIP(r), strings.TrimSpace(r.UserAgent()))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "dryRun": true, "mode": mode, "imported": imported, "overwritten": overwritten, "skipped": skipped, "details": details, "detailCount": len(details)})
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = a.insertOpAudit("provider.import", "batch", fmt.Sprintf("mode=%s imported=%d overwritten=%d skipped=%d", mode, imported, overwritten, skipped), clientIP(r), strings.TrimSpace(r.UserAgent()))
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "mode": mode, "imported": imported, "overwritten": overwritten, "skipped": skipped, "details": details, "detailCount": len(details)})
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

func (a *App) countLoginAudits() (int, error) {
	var n int
	err := a.db.QueryRow(`SELECT COUNT(1) FROM login_audits`).Scan(&n)
	return n, err
}

func (a *App) listLoginAudits(limit, offset int) ([]LoginAudit, error) {
	rows, err := a.db.Query(`SELECT id,ip,user_agent,success,reason,created_at FROM login_audits ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
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

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

func getenv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func getenvInt(k string, d int) int {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return d
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return d
	}
	return n
}

func parseIntRange(raw string, d, min, max int) int {
	v := strings.TrimSpace(raw)
	if v == "" {
		return d
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return d
	}
	if n < min {
		n = min
	}
	if n > max {
		n = max
	}
	return n
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

func parseOpAuditQuery(r *http.Request) OpAuditQuery {
	q := OpAuditQuery{
		Limit:  parseIntRange(r.URL.Query().Get("limit"), 50, 1, 200),
		Offset: parseIntRange(r.URL.Query().Get("offset"), 0, 0, 1_000_000),
		Action: strings.TrimSpace(r.URL.Query().Get("action")),
		Target: strings.TrimSpace(r.URL.Query().Get("target")),
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
	return strings.ReplaceAll(loginHTML, "{{ERROR_BLOCK}}", errBlock)
}

const indexHTML = `<!doctype html>
<html>
<head>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1"/>
  <title>LX Switch</title>
  <style>
    body{font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;margin:20px;max-width:980px}
    input,select,textarea,button{padding:8px;margin:4px 0;width:100%}
    .row{display:grid;grid-template-columns:1fr 1fr;gap:10px}
    .card{border:1px solid #ddd;border-radius:10px;padding:12px;margin:10px 0}
    .muted{color:#666;font-size:12px}
    table{width:100%;border-collapse:collapse} td,th{border-bottom:1px solid #eee;padding:8px;text-align:left}
    .actions button{width:auto;margin-right:6px}
    .actions .ghost{background:#f3f4f6}
    .warn{background:#fff7ed;border:1px solid #fdba74;color:#9a3412;padding:10px;border-radius:8px;margin:10px 0}
    .ok{background:#ecfeff;border:1px solid #67e8f9;color:#155e75;padding:10px;border-radius:8px;margin:10px 0}
    .hide{display:none}
  </style>
</head>
<body>
  <h2>LX Switch v0.2</h2>
  <p class="muted">Server-native switch panel for Claude/Codex/OpenClaw/Gemini</p>

  <div id="firstRun" class="ok hide"></div>
  <div id="weakToken" class="warn hide"></div>
  <div id="activeProvider" class="ok hide"></div>
  <div id="auditRetention" class="muted"></div>

  <div class="card">
    <div style="display:flex;justify-content:space-between;align-items:center;gap:10px;">
      <div class="muted">已登录，可直接管理 Provider</div>
      <button style="width:auto" onclick="location.href='/logout'">退出登录</button>
    </div>
  </div>

  <div class="card">
    <h3 id="editorTitle">新增 Provider</h3>
    <div class="row">
      <div><label>Name</label><input id="name"/></div>
      <div><label>Target</label>
        <select id="target">
          <option>openclaw</option><option>claude</option><option>codex</option><option>gemini</option>
        </select>
      </div>
    </div>
    <div class="row">
      <div><label>Base URL</label><input id="baseUrl"/></div>
      <div><label>Model</label><input id="model"/></div>
    </div>
    <label>API Key</label><input id="apiKey"/>
    <label>Notes</label><textarea id="notes"></textarea>
    <div class="actions">
      <button onclick="saveProvider()">保存</button>
      <button class="ghost" id="cancelEditBtn" onclick="cancelEdit()" style="display:none">取消编辑</button>
    </div>
  </div>

  <div class="card">
    <h3>Providers</h3>
    <div class="row">
      <div>
        <label>搜索</label>
        <input id="providerSearch" placeholder="name/model/baseUrl/notes"/>
      </div>
      <div>
        <label>Target 过滤</label>
        <select id="providerTargetFilter">
          <option value="">全部</option>
          <option>openclaw</option><option>claude</option><option>codex</option><option>gemini</option>
        </select>
      </div>
    </div>
    <div class="actions">
      <button onclick="applyProviderFilter()">应用过滤</button>
      <button class="ghost" onclick="clearProviderFilter()">清空过滤</button>
      <button class="ghost" onclick="testProvidersBatch()">批量测试当前筛选</button>
      <button class="ghost" onclick="exportLastBatchTestCsv()">导出最近批测(CSV)</button>
      <button class="ghost" onclick="exportLastBatchTestJson()">导出最近批测(JSON)</button>
    </div>
    <table><thead><tr><th>ID</th><th>Name</th><th>Target</th><th>Model</th><th>操作</th></tr></thead><tbody id="rows"></tbody></table>
  </div>

  <div class="card">
    <h3>批量导入 Providers(JSON)</h3>
    <p class="muted">格式：{"items":[{"name":"...","target":"openclaw","baseUrl":"https://...","apiKey":"...","model":"...","notes":"..."}]}</p>
    <div class="row">
      <div>
        <label>冲突策略</label>
        <select id="importMode"><option value="skip">skip（冲突跳过）</option><option value="overwrite">overwrite（冲突覆盖）</option></select>
      </div>
      <div>
        <label>预检明细上限</label>
        <input id="previewLimit" type="number" min="1" max="200" value="30"/>
      </div>
    </div>
    <textarea id="importJson" rows="6" placeholder='{"items":[{"name":"demo","target":"openclaw","baseUrl":"https://cli.lyb123.top/v1","model":"gpt-5.3-codex"}]}'></textarea>
    <div class="actions">
      <button onclick="previewImportProviders()">预检（dry-run）</button>
      <button onclick="importProviders()">导入</button>
      <button class="ghost" onclick="exportProviders()">导出当前筛选 JSON</button>
      <button class="ghost" onclick="exportLastImportJson()">导出最近导入结果(JSON)</button>
      <button class="ghost" onclick="exportLastImportCsv()">导出最近导入结果(CSV)</button>
    </div>
  </div>

  <div class="card">
    <h3>Backups</h3>
    <button onclick="loadBackups()">刷新备份</button>
    <table><thead><tr><th>Name</th><th>Size</th><th>操作</th></tr></thead><tbody id="backs"></tbody></table>
  </div>

  <div class="card">
    <h3>登录审计</h3>
    <div class="actions">
      <button onclick="prevAuditPage()">上一页</button>
      <button onclick="nextAuditPage()">下一页</button>
      <button class="ghost" onclick="loadAudits()">刷新审计日志</button>
      <button class="ghost" onclick="exportAudits()">导出 CSV</button>
    </div>
    <div id="auditPager" class="muted"></div>
    <table><thead><tr><th>时间</th><th>IP</th><th>结果</th><th>原因</th><th>UA</th></tr></thead><tbody id="audits"></tbody></table>
  </div>

  <div class="card">
    <h3>操作审计</h3>
    <div class="actions">
      <button onclick="prevOpPage()">上一页</button>
      <button onclick="nextOpPage()">下一页</button>
      <button class="ghost" onclick="loadOpAudits()">刷新操作日志</button>
      <button class="ghost" onclick="exportOpAudits()">导出 CSV</button>
      <button class="ghost" onclick="cleanupAudits()">清理旧审计</button>
    </div>
    <div class="row">
      <div>
        <label>Action 过滤</label>
        <input id="opActionFilter" placeholder="例如 provider.activate"/>
      </div>
      <div>
        <label>Target 过滤</label>
        <input id="opTargetFilter" placeholder="例如 openclaw"/>
      </div>
    </div>
    <div class="actions">
      <button onclick="applyOpFilter()">应用过滤</button>
      <button class="ghost" onclick="clearOpFilter()">清空过滤</button>
    </div>
    <div id="opPager" class="muted"></div>
    <table><thead><tr><th>时间</th><th>Action</th><th>Target</th><th>Detail</th><th>IP</th><th>UA</th></tr></thead><tbody id="ops"></tbody></table>
  </div>

<script>
let editId = null;
let providerMap = {};
let providerSearch = '';
let providerTargetFilter = '';
let lastImportResult = null;
let lastBatchTestResult = null;
let auditOffset = 0;
const auditLimit = 20;
let auditTotal = 0;
let opOffset = 0;
const opLimit = 20;
let opTotal = 0;
let opActionFilter = '';
let opTargetFilter = '';

function H(){ return {'Content-Type':'application/json'}; }
async function api(url,opt={}){ const r=await fetch(url,{...opt,credentials:'same-origin',headers:{...(opt.headers||{}),...H()}}); if(!r.ok) throw new Error(await r.text()); return r.json(); }
function setBox(id,msg){ const el=document.getElementById(id); if(!el) return; if(!msg){el.classList.add('hide');el.textContent='';return;} el.textContent=msg; el.classList.remove('hide'); }
function v(id){return document.getElementById(id).value}
function setV(id,val){ document.getElementById(id).value = val || ''; }
function resetForm(){ ['name','target','baseUrl','apiKey','model','notes'].forEach(k=>setV(k,'')); document.getElementById('target').value='openclaw'; }

function startEdit(id){
  const p = providerMap[id];
  if(!p) return;
  editId = id;
  document.getElementById('editorTitle').textContent = '编辑 Provider #' + id;
  document.getElementById('cancelEditBtn').style.display = '';
  setV('name', p.name);
  setV('target', p.target);
  setV('baseUrl', p.baseUrl);
  setV('apiKey', p.apiKey);
  setV('model', p.model);
  setV('notes', p.notes);
  window.scrollTo({top:0,behavior:'smooth'});
}

function cancelEdit(){
  editId = null;
  document.getElementById('editorTitle').textContent = '新增 Provider';
  document.getElementById('cancelEditBtn').style.display = 'none';
  resetForm();
}

async function saveProvider(){
  const body={
    name:v('name'),target:v('target'),baseUrl:v('baseUrl'),apiKey:v('apiKey'),model:v('model'),notes:v('notes')
  };
  if(editId){
    await api('/api/providers/'+editId,{method:'PUT',body:JSON.stringify(body)});
    alert('已更新');
  }else{
    await api('/api/providers',{method:'POST',body:JSON.stringify(body)});
    alert('已保存');
  }
  cancelEdit();
  await loadProviders();
}

async function loadMeta(){
  const m=await api('/api/meta');
  if(m.firstRun){
    setBox('firstRun','首次使用引导：1) 先新增一个 Provider；2) 点击“激活”写入目标配置；3) 如需回退可在 Backups 里回滚。');
  }else{
    setBox('firstRun','');
  }
  if(m.tokenWeak){
    setBox('weakToken','检测到默认 Token（change-me-please），强烈建议尽快在 systemd 环境变量里修改 LX_SWITCH_TOKEN。');
  }else{
    setBox('weakToken','');
  }
  if(m.activeProvider){
    setBox('activeProvider','当前生效 Provider ID: '+m.activeProvider);
  }else{
    setBox('activeProvider','当前尚未激活 Provider');
  }
  const ard = Number(m.auditRetentionDays||0);
  const ar = document.getElementById('auditRetention');
  if(ar){ ar.textContent = ard>0 ? ('审计默认保留天数：'+ard+' 天') : ''; }
}

async function loadProviders(){
  const q='search='+encodeURIComponent(providerSearch)+'&target='+encodeURIComponent(providerTargetFilter);
  const list=await api('/api/providers?'+q);
  providerMap = {};
  const tb=document.getElementById('rows'); tb.innerHTML='';
  list.forEach(p=>{
    providerMap[p.id] = p;
    const tr=document.createElement('tr');
    tr.innerHTML='<td>'+p.id+'</td><td>'+p.name+'</td><td>'+p.target+'</td><td>'+(p.model||'')+'</td><td class="actions">'
      + '<button onclick="activate('+p.id+')">激活</button>'
      + '<button class="ghost" onclick="testProvider('+p.id+')">测试</button>'
      + '<button class="ghost" onclick="startEdit('+p.id+')">编辑</button>'
      + '<button onclick="delP('+p.id+')">删除</button>'
      + '</td>';
    tb.appendChild(tr);
  });
}

async function activate(id){
  const r=await api('/api/activate',{method:'POST',body:JSON.stringify({providerId:id})});
  alert('已激活，备份: '+r.backup);
  await loadBackups();
  await loadMeta();
  await loadOpAudits();
}

async function delP(id){
  if(!confirm('确定删除?')) return;
  await api('/api/providers/'+id,{method:'DELETE'});
  if(editId === id) cancelEdit();
  await loadProviders();
}

function applyProviderFilter(){
  providerSearch = (document.getElementById('providerSearch').value || '').trim();
  providerTargetFilter = (document.getElementById('providerTargetFilter').value || '').trim();
  loadProviders();
}

function clearProviderFilter(){
  providerSearch = '';
  providerTargetFilter = '';
  document.getElementById('providerSearch').value = '';
  document.getElementById('providerTargetFilter').value = '';
  loadProviders();
}

async function importProviders(){
  const raw = (document.getElementById('importJson').value || '').trim();
  if(!raw){ alert('请先粘贴 JSON'); return; }
  let obj;
  try{ obj = JSON.parse(raw); }catch(e){ alert('JSON 格式错误: '+e.message); return; }
  obj.mode = (document.getElementById('importMode').value || 'skip');
  obj.previewLimit = Number(document.getElementById('previewLimit').value || 30);
  obj.dryRun = false;
  const r = await api('/api/providers/import',{method:'POST',body:JSON.stringify(obj)});
  lastImportResult = r;
  alert('导入完成: 新增 '+(r.imported||0)+'，覆盖 '+(r.overwritten||0)+'，跳过 '+(r.skipped||0)+'，模式 '+(r.mode||''));
  await loadProviders();
  await loadOpAudits();
}

async function previewImportProviders(){
  const raw = (document.getElementById('importJson').value || '').trim();
  if(!raw){ alert('请先粘贴 JSON'); return; }
  let obj;
  try{ obj = JSON.parse(raw); }catch(e){ alert('JSON 格式错误: '+e.message); return; }
  obj.mode = (document.getElementById('importMode').value || 'skip');
  obj.previewLimit = Number(document.getElementById('previewLimit').value || 30);
  obj.dryRun = true;
  const r = await api('/api/providers/import',{method:'POST',body:JSON.stringify(obj)});
  lastImportResult = r;
  const details = (r.details||[]).slice(0,10).map(x=>('['+x.action+'] '+x.target+'/'+x.name+(x.existingId?(' -> #'+x.existingId):''))).join('\n');
  alert('预检完成（不落库）\n新增 '+(r.imported||0)+'，覆盖 '+(r.overwritten||0)+'，跳过 '+(r.skipped||0)+'，模式 '+(r.mode||'') + (details?('\n\n样例:\n'+details):''));
  await loadOpAudits();
}

function exportProviders(){
  const q='search='+encodeURIComponent(providerSearch)+'&target='+encodeURIComponent(providerTargetFilter);
  window.open('/api/providers/export?'+q,'_blank');
}

function downloadTextFile(name, text, type='text/plain;charset=utf-8'){
  const blob = new Blob([text], {type});
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

function exportLastImportJson(){
  if(!lastImportResult){ alert('暂无导入结果，请先执行预检或导入'); return; }
  downloadTextFile('import-result-'+Date.now()+'.json', JSON.stringify(lastImportResult, null, 2), 'application/json;charset=utf-8');
}

function csvEscJs(s){
  s = String(s ?? '');
  return '"'+s.replace(/"/g,'""')+'"';
}

function exportLastImportCsv(){
  if(!lastImportResult){ alert('暂无导入结果，请先执行预检或导入'); return; }
  const rows = [];
  rows.push('index,action,target,name,existing_id');
  (lastImportResult.details||[]).forEach(d=>{
    rows.push([d.index, csvEscJs(d.action), csvEscJs(d.target), csvEscJs(d.name), d.existingId||''].join(','));
  });
  rows.push('');
  rows.push('summary_key,summary_value');
  rows.push('mode,'+csvEscJs(lastImportResult.mode||''));
  rows.push('dryRun,'+csvEscJs(lastImportResult.dryRun===true));
  rows.push('imported,'+(lastImportResult.imported||0));
  rows.push('overwritten,'+(lastImportResult.overwritten||0));
  rows.push('skipped,'+(lastImportResult.skipped||0));
  rows.push('detailCount,'+(lastImportResult.detailCount||0));
  downloadTextFile('import-result-'+Date.now()+'.csv', rows.join('\n'), 'text/csv;charset=utf-8');
}

async function testProvider(id){
  const r = await api('/api/providers/test',{method:'POST',body:JSON.stringify({providerId:id})});
  if(r.ok){
    alert('连通性测试通过，HTTP '+(r.statusCode||0));
  }else{
    alert('连通性测试失败，HTTP '+(r.statusCode||0)+'\n'+(r.detail||''));
  }
  await loadOpAudits();
}

async function testProvidersBatch(){
  const q='search='+encodeURIComponent(providerSearch)+'&target='+encodeURIComponent(providerTargetFilter);
  const r = await api('/api/providers/test-batch?'+q,{method:'POST'});
  lastBatchTestResult = r;
  let msg = '批量测试完成：总计 '+(r.total||0)+'，通过 '+(r.okCount||0)+'，失败 '+(r.failCount||0);
  const fail = (r.items||[]).filter(x=>!x.ok).slice(0,5);
  if(fail.length){
    msg += '\n失败样例：\n' + fail.map(x=>('#'+x.providerId+' '+x.name+' ['+x.target+'] code='+x.statusCode)).join('\n');
  }
  alert(msg);
  await loadOpAudits();
}

function exportLastBatchTestJson(){
  if(!lastBatchTestResult){ alert('暂无批量测试结果，请先执行批量测试'); return; }
  downloadTextFile('provider-batch-test-'+Date.now()+'.json', JSON.stringify(lastBatchTestResult, null, 2), 'application/json;charset=utf-8');
}

function exportLastBatchTestCsv(){
  if(!lastBatchTestResult){ alert('暂无批量测试结果，请先执行批量测试'); return; }
  const rows = [];
  rows.push('provider_id,name,target,ok,status_code,detail');
  (lastBatchTestResult.items||[]).forEach(it=>{
    rows.push([it.providerId, csvEscJs(it.name), csvEscJs(it.target), it.ok ? 1 : 0, it.statusCode||0, csvEscJs(it.detail||'')].join(','));
  });
  rows.push('');
  rows.push('summary_key,summary_value');
  rows.push('total,'+(lastBatchTestResult.total||0));
  rows.push('okCount,'+(lastBatchTestResult.okCount||0));
  rows.push('failCount,'+(lastBatchTestResult.failCount||0));
  downloadTextFile('provider-batch-test-'+Date.now()+'.csv', rows.join('\n'), 'text/csv;charset=utf-8');
}

async function loadBackups(){
  const list=await api('/api/backups');
  const tb=document.getElementById('backs'); tb.innerHTML='';
  list.forEach(b=>{
    const tr=document.createElement('tr');
    tr.innerHTML='<td>'+b.name+'</td><td>'+b.size+'</td><td><button onclick="rollback(\''+b.name+'\')">回滚</button></td>';
    tb.appendChild(tr);
  });
}

async function rollback(name){
  if(!confirm('回滚到 '+name+' ?')) return;
  await api('/api/rollback',{method:'POST',body:JSON.stringify({name})});
  alert('回滚完成');
}

async function loadAudits(){
  const res=await api('/api/login-audits?limit='+auditLimit+'&offset='+auditOffset);
  const list=res.items||[];
  auditTotal = res.total||0;
  const tb=document.getElementById('audits'); tb.innerHTML='';
  const pager=document.getElementById('auditPager');
  const from = auditTotal===0 ? 0 : (auditOffset+1);
  const to = Math.min(auditOffset+auditLimit, auditTotal);
  pager.textContent='审计日志：共 '+auditTotal+' 条，当前 '+from+' - '+to;
  list.forEach(a=>{
    const tr=document.createElement('tr');
    const ua=(a.userAgent||'').replace(/</g,'&lt;').replace(/>/g,'&gt;');
    tr.innerHTML='<td>'+(a.createdAt||'')+'</td><td>'+(a.ip||'')+'</td><td>'+(a.success?'成功':'失败')+'</td><td>'+(a.reason||'')+'</td><td>'+ua+'</td>';
    tb.appendChild(tr);
  });
}

function prevAuditPage(){
  auditOffset = Math.max(0, auditOffset - auditLimit);
  loadAudits();
}

function nextAuditPage(){
  if(auditOffset + auditLimit >= auditTotal) return;
  auditOffset += auditLimit;
  loadAudits();
}

function exportAudits(){
  window.open('/api/login-audits/export?limit=2000','_blank');
}

async function loadOpAudits(){
  const q='limit='+opLimit+'&offset='+opOffset+'&action='+encodeURIComponent(opActionFilter)+'&target='+encodeURIComponent(opTargetFilter);
  const res=await api('/api/op-audits?'+q);
  const list=res.items||[];
  opTotal = res.total||0;
  const tb=document.getElementById('ops'); tb.innerHTML='';
  const pager=document.getElementById('opPager');
  const from = opTotal===0 ? 0 : (opOffset+1);
  const to = Math.min(opOffset+opLimit, opTotal);
  const f = [];
  if(opActionFilter) f.push('action='+opActionFilter);
  if(opTargetFilter) f.push('target='+opTargetFilter);
  pager.textContent='操作日志：共 '+opTotal+' 条，当前 '+from+' - '+to + (f.length?('，过滤：'+f.join(', ')):'');
  list.forEach(a=>{
    const tr=document.createElement('tr');
    const ua=(a.userAgent||'').replace(/</g,'&lt;').replace(/>/g,'&gt;');
    tr.innerHTML='<td>'+(a.createdAt||'')+'</td><td>'+(a.action||'')+'</td><td>'+(a.target||'')+'</td><td>'+(a.detail||'')+'</td><td>'+(a.ip||'')+'</td><td>'+ua+'</td>';
    tb.appendChild(tr);
  });
}

function prevOpPage(){
  opOffset = Math.max(0, opOffset - opLimit);
  loadOpAudits();
}

function nextOpPage(){
  if(opOffset + opLimit >= opTotal) return;
  opOffset += opLimit;
  loadOpAudits();
}

function applyOpFilter(){
  opActionFilter = (document.getElementById('opActionFilter').value || '').trim();
  opTargetFilter = (document.getElementById('opTargetFilter').value || '').trim();
  opOffset = 0;
  loadOpAudits();
}

function clearOpFilter(){
  opActionFilter = '';
  opTargetFilter = '';
  document.getElementById('opActionFilter').value = '';
  document.getElementById('opTargetFilter').value = '';
  opOffset = 0;
  loadOpAudits();
}

function exportOpAudits(){
  const q='limit=2000&action='+encodeURIComponent(opActionFilter)+'&target='+encodeURIComponent(opTargetFilter);
  window.open('/api/op-audits/export?'+q,'_blank');
}

async function cleanupAudits(){
  const raw = prompt('保留最近多少天审计记录？（默认 30）','30');
  if(raw===null) return;
  const keep = Number(raw||30);
  if(!Number.isFinite(keep) || keep<1){ alert('请输入 >=1 的天数'); return; }
  if(!confirm('将删除早于 '+keep+' 天的登录/操作审计，确定继续？')) return;
  const r = await api('/api/audits/cleanup?keepDays='+encodeURIComponent(String(Math.floor(keep))),{method:'POST'});
  alert('清理完成：login '+(r.loginDeleted||0)+' 条，op '+(r.opDeleted||0)+' 条，总计 '+(r.totalDeleted||0));
  auditOffset = 0;
  opOffset = 0;
  await loadAudits();
  await loadOpAudits();
}

async function loadAll(){
  cancelEdit();
  await loadMeta();
  await loadProviders();
  await loadBackups();
  await loadAudits();
  await loadOpAudits();
}

window.addEventListener('DOMContentLoaded', loadAll);
</script>
</body></html>`

const loginHTML = `<!doctype html>
<html>
<head>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1"/>
  <title>LX Switch Login</title>
  <style>
    body{font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;background:#f7f7f8;margin:0}
    .wrap{max-width:420px;margin:80px auto;background:#fff;border:1px solid #e5e7eb;border-radius:12px;padding:20px}
    input,button{width:100%;padding:10px;margin-top:10px}
    .muted{font-size:12px;color:#666}
    .err{margin-top:10px;padding:10px;border-radius:8px;background:#fef2f2;border:1px solid #fecaca;color:#991b1b;font-size:14px}
  </style>
</head>
<body>
  <div class="wrap">
    <h3>LX Switch 登录</h3>
    <p class="muted">输入管理员 Token 进入面板</p>
    {{ERROR_BLOCK}}
    <form method="post" action="/login">
      <input type="password" name="token" placeholder="Admin Token" required />
      <button type="submit">登录</button>
    </form>
  </div>
</body>
</html>`
