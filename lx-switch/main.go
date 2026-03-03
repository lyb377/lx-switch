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
	db                *sql.DB
	dataDir           string
	backupDir         string
	adminToken        string
	failed            map[string]*attemptState
	mu                sync.Mutex
	maxAttempts       int
	window            time.Duration
	lockout           time.Duration
	maxLoginBodyBytes int64
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

type SaveReq struct {
	Name    string `json:"name"`
	Target  string `json:"target"`
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
	Model   string `json:"model"`
	Notes   string `json:"notes"`
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
		db:                db,
		dataDir:           dataDir,
		backupDir:         backupDir,
		adminToken:        token,
		failed:            map[string]*attemptState{},
		maxAttempts:       getenvInt("LX_SWITCH_MAX_LOGIN_ATTEMPTS", 6),
		window:            time.Duration(getenvInt("LX_SWITCH_LOGIN_WINDOW_SEC", 300)) * time.Second,
		lockout:           time.Duration(getenvInt("LX_SWITCH_LOGIN_LOCK_SEC", 900)) * time.Second,
		maxLoginBodyBytes: int64(getenvInt("LX_SWITCH_LOGIN_MAX_BODY", 4096)),
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
	mux.HandleFunc("/api/providers/", app.withAuth(app.handleProviderByID))
	mux.HandleFunc("/api/activate", app.withAuth(app.handleActivate))
	mux.HandleFunc("/api/backups", app.withAuth(app.handleBackups))
	mux.HandleFunc("/api/rollback", app.withAuth(app.handleRollback))
	mux.HandleFunc("/api/meta", app.withAuth(app.handleMeta))
	mux.HandleFunc("/api/login-audits", app.withAuth(app.handleLoginAudits))
	mux.HandleFunc("/api/login-audits/export", app.withAuth(app.handleLoginAuditsExport))

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
		"activeProvider": active,
		"providerCount":  cnt,
		"firstRun":       cnt == 0,
		"tokenWeak":      a.adminToken == "change-me-please",
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

func (a *App) handleProviders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := a.listProviders()
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
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
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
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	case http.MethodDelete:
		_, err := a.db.Exec(`DELETE FROM providers WHERE id=?`, id)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
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

func (a *App) listProviders() ([]Provider, error) {
	rows, err := a.db.Query(`SELECT id,name,target,base_url,api_key,model,notes,created_at,updated_at FROM providers ORDER BY id DESC`)
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
	return out, nil
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
    <table><thead><tr><th>ID</th><th>Name</th><th>Target</th><th>Model</th><th>操作</th></tr></thead><tbody id="rows"></tbody></table>
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

<script>
let editId = null;
let providerMap = {};
let auditOffset = 0;
const auditLimit = 20;
let auditTotal = 0;

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
}

async function loadProviders(){
  const list=await api('/api/providers');
  providerMap = {};
  const tb=document.getElementById('rows'); tb.innerHTML='';
  list.forEach(p=>{
    providerMap[p.id] = p;
    const tr=document.createElement('tr');
    tr.innerHTML='<td>'+p.id+'</td><td>'+p.name+'</td><td>'+p.target+'</td><td>'+(p.model||'')+'</td><td class="actions">'
      + '<button onclick="activate('+p.id+')">激活</button>'
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
}

async function delP(id){
  if(!confirm('确定删除?')) return;
  await api('/api/providers/'+id,{method:'DELETE'});
  if(editId === id) cancelEdit();
  await loadProviders();
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

async function loadAll(){
  cancelEdit();
  await loadMeta();
  await loadProviders();
  await loadBackups();
  await loadAudits();
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
