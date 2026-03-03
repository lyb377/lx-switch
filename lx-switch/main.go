package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type App struct {
	db         *sql.DB
	dataDir    string
	backupDir  string
	adminToken string
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

	app := &App{db: db, dataDir: dataDir, backupDir: backupDir, adminToken: token}
	if err := app.initDB(); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", app.handleIndex)
	mux.HandleFunc("/healthz", app.handleHealth)
	mux.HandleFunc("/api/providers", app.withAuth(app.handleProviders))
	mux.HandleFunc("/api/providers/", app.withAuth(app.handleProviderByID))
	mux.HandleFunc("/api/activate", app.withAuth(app.handleActivate))
	mux.HandleFunc("/api/backups", app.withAuth(app.handleBackups))
	mux.HandleFunc("/api/rollback", app.withAuth(app.handleRollback))
	mux.HandleFunc("/api/meta", app.withAuth(app.handleMeta))

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
		if t != a.adminToken {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, indexHTML)
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ts": time.Now().Unix()})
}

func (a *App) handleMeta(w http.ResponseWriter, r *http.Request) {
	active, _ := a.getState("active_provider")
	_ = json.NewEncoder(w).Encode(map[string]any{"activeProvider": active})
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
		if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Target) == "" {
			http.Error(w, "name/target required", 400)
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
		if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Target) == "" {
			http.Error(w, "name/target required", 400)
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
	var req struct { Name string `json:"name"` }
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

func (a *App) listProviders() ([]Provider, error) {
	rows, err := a.db.Query(`SELECT id,name,target,base_url,api_key,model,notes,created_at,updated_at FROM providers ORDER BY id DESC`)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []Provider
	for rows.Next() {
		var p Provider
		var c,u string
		if err := rows.Scan(&p.ID,&p.Name,&p.Target,&p.BaseURL,&p.APIKey,&p.Model,&p.Notes,&c,&u); err != nil { return nil, err }
		p.CreatedAt,_ = time.Parse(time.RFC3339,c)
		p.UpdatedAt,_ = time.Parse(time.RFC3339,u)
		out = append(out,p)
	}
	return out,nil
}

func (a *App) getProvider(id int64) (*Provider, error) {
	var p Provider
	var c,u string
	err := a.db.QueryRow(`SELECT id,name,target,base_url,api_key,model,notes,created_at,updated_at FROM providers WHERE id=?`, id).
		Scan(&p.ID,&p.Name,&p.Target,&p.BaseURL,&p.APIKey,&p.Model,&p.Notes,&c,&u)
	if err != nil { return nil, err }
	p.CreatedAt,_ = time.Parse(time.RFC3339,c)
	p.UpdatedAt,_ = time.Parse(time.RFC3339,u)
	return &p,nil
}

func (a *App) setState(k,v string) error {
	_, err := a.db.Exec(`INSERT INTO state(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k,v)
	return err
}
func (a *App) getState(k string) (string,error) {
	var v string
	err := a.db.QueryRow(`SELECT v FROM state WHERE k=?`, k).Scan(&v)
	if err == sql.ErrNoRows { return "", nil }
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
	if err != nil { return "", err }
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
	if err := os.WriteFile(dst, b, 0o600); err != nil { return "", err }
	return name, nil
}

func (a *App) writeTargetConfig(p *Provider) error {
	dst, err := targetPath(p.Target)
	if err != nil { return err }
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { return err }

	switch p.Target {
	case "codex":
		content := ""
		if p.BaseURL != "" { content += fmt.Sprintf("base_url = \"%s\"\n", p.BaseURL) }
		if p.APIKey != "" { content += fmt.Sprintf("api_key = \"%s\"\n", p.APIKey) }
		if p.Model != "" { content += fmt.Sprintf("model = \"%s\"\n", p.Model) }
		if content == "" { content = "# managed by lx-switch\n" }
		return os.WriteFile(dst, []byte(content), 0o600)
	default:
		m := map[string]any{
			"managedBy": "lx-switch",
			"name": p.Name,
			"target": p.Target,
			"baseUrl": p.BaseURL,
			"apiKey": p.APIKey,
			"model": p.Model,
			"notes": p.Notes,
			"updatedAt": time.Now().Format(time.RFC3339),
		}
		b, _ := json.MarshalIndent(m, "", "  ")
		return os.WriteFile(dst, b, 0o600)
	}
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w,r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

func getenv(k, d string) string { if v := os.Getenv(k); v != "" { return v }; return d }

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
  </style>
</head>
<body>
  <h2>LX Switch v0.1</h2>
  <p class="muted">Server-native switch panel for Claude/Codex/OpenClaw/Gemini</p>

  <div class="card">
    <label>Admin Token</label>
    <input id="token" placeholder="X-Admin-Token"/>
    <button onclick="loadAll()">连接</button>
  </div>

  <div class="card">
    <h3>新增 Provider</h3>
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
    <button onclick="createProvider()">保存</button>
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

<script>
let T='';
function H(){ return {'Content-Type':'application/json','X-Admin-Token':T}; }
async function api(url,opt={}){ const r=await fetch(url,{...opt,headers:{...(opt.headers||{}),...H()}}); if(!r.ok) throw new Error(await r.text()); return r.json(); }
async function loadAll(){ T=document.getElementById('token').value.trim(); await loadProviders(); await loadBackups(); }
async function createProvider(){
  const body={
    name:v('name'),target:v('target'),baseUrl:v('baseUrl'),apiKey:v('apiKey'),model:v('model'),notes:v('notes')
  };
  await api('/api/providers',{method:'POST',body:JSON.stringify(body)}); alert('已保存'); await loadProviders();
}
function v(id){return document.getElementById(id).value}
async function loadProviders(){
  const list=await api('/api/providers');
  const tb=document.getElementById('rows'); tb.innerHTML='';
  list.forEach(p=>{
    const tr=document.createElement('tr');
    tr.innerHTML='<td>'+p.id+'</td><td>'+p.name+'</td><td>'+p.target+'</td><td>'+(p.model||'')+'</td><td class="actions">'
      + '<button onclick="activate('+p.id+')">激活</button>'
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
async function delP(id){ if(!confirm('确定删除?')) return; await api('/api/providers/'+id,{method:'DELETE'}); await loadProviders(); }
async function loadBackups(){
  const list=await api('/api/backups');
  const tb=document.getElementById('backs'); tb.innerHTML='';
  list.forEach(b=>{
    const tr=document.createElement('tr');
    tr.innerHTML='<td>'+b.name+'</td><td>'+b.size+'</td><td><button onclick="rollback(\''+b.name+'\')">回滚</button></td>';
    tb.appendChild(tr);
  });
}
async function rollback(name){ if(!confirm('回滚到 '+name+' ?')) return; await api('/api/rollback',{method:'POST',body:JSON.stringify({name})}); alert('回滚完成'); }
</script>
</body></html>`
