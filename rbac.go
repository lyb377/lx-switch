package main

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Permission constants
const (
	PermProvidersRead  = "providers:read"
	PermProvidersWrite = "providers:write"
	PermUsersRead      = "users:read"
	PermUsersWrite     = "users:write"
	PermAuditRead      = "audit:read"
	PermAuditCleanup   = "audit:cleanup"
	PermSecurityRead   = "security:read"
	PermSecurityWrite  = "security:write"
	PermBackupsRead    = "backups:read"
	PermBackupsWrite   = "backups:write"
)

// Role constants
const (
	RoleAdmin  = "admin"
	RoleEditor = "editor"
	RoleViewer = "viewer"
)

// User represents a user in the system
type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email,omitempty"`
	RoleID    int64     `json:"roleId"`
	RoleName  string    `json:"roleName,omitempty"`
	Active    bool      `json:"active"`
	TOTPEnabled bool    `json:"totpEnabled"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	LastLogin *time.Time `json:"lastLogin,omitempty"`
}

// Role represents a role in the system
type Role struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Session represents a user session
type Session struct {
	ID        string
	UserID    int64
	CreatedAt time.Time
	ExpiresAt time.Time
	IP        string
	UserAgent string
}

// Session store
var (
	sessions     = make(map[string]*Session)
	sessionsMu   sync.RWMutex
	sessionTTL   = 24 * time.Hour
)

// initRBACSchema initializes the RBAC database tables
func (a *App) initRBACSchema() error {
	schema := `
	-- Users table
	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL UNIQUE,
		email TEXT,
		password_hash TEXT NOT NULL,
		role_id INTEGER NOT NULL DEFAULT 3,
		active INTEGER NOT NULL DEFAULT 1,
		totp_enabled INTEGER NOT NULL DEFAULT 0,
		totp_secret TEXT DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		last_login TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);

	-- Roles table
	CREATE TABLE IF NOT EXISTS roles (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		description TEXT,
		permissions TEXT NOT NULL DEFAULT '[]',
		created_at TEXT NOT NULL
	);

	-- Sessions table (for persistent sessions)
	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		user_id INTEGER NOT NULL,
		created_at TEXT NOT NULL,
		expires_at TEXT NOT NULL,
		ip TEXT,
		user_agent TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
	CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);
	`

	_, err := a.db.Exec(schema)
	if err != nil {
		return err
	}

	// Initialize default roles if not exist
	if err := a.initDefaultRoles(); err != nil {
		return err
	}

	// Initialize default admin user if not exist
	return a.initDefaultAdmin()
}

// initDefaultRoles initializes default roles
func (a *App) initDefaultRoles() error {
	// Check if roles exist
	var count int
	err := a.db.QueryRow(`SELECT COUNT(1) FROM roles`).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	now := time.Now().Format(time.RFC3339)
	roles := []struct {
		name        string
		description string
		permissions string
	}{
		{
			name:        RoleAdmin,
			description: "Full system access",
			permissions: `["providers:read","providers:write","users:read","users:write","audit:read","audit:cleanup","security:read","security:write","backups:read","backups:write"]`,
		},
		{
			name:        RoleEditor,
			description: "Can manage providers and view audit logs",
			permissions: `["providers:read","providers:write","audit:read","backups:read"]`,
		},
		{
			name:        RoleViewer,
			description: "Read-only access",
			permissions: `["providers:read","audit:read"]`,
		},
	}

	for _, r := range roles {
		_, err := a.db.Exec(
			`INSERT INTO roles(name, description, permissions, created_at) VALUES(?, ?, ?, ?)`,
			r.name, r.description, r.permissions, now,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

// initDefaultAdmin initializes the default admin user
func (a *App) initDefaultAdmin() error {
	// Check if any user exists
	var count int
	err := a.db.QueryRow(`SELECT COUNT(1) FROM users`).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	// Create default admin with the admin token as password
	now := time.Now().Format(time.RFC3339)
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(a.adminToken), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	_, err = a.db.Exec(
		`INSERT INTO users(username, email, password_hash, role_id, active, created_at, updated_at) VALUES(?, ?, ?, 1, 1, ?, ?)`,
		"admin", "admin@localhost", string(passwordHash), now, now,
	)
	return err
}

// withRBACAuth is a middleware that checks if the user has the required permission
func (a *App) withRBACAuth(permission string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get user from session
		user, ok := a.getUserFromRequest(r)
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}

		// Check if user is active
		if !user.Active {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "account disabled"})
			return
		}

		// Check permission
		if !a.hasPermission(user.RoleID, permission) {
			_ = a.insertOpAudit("rbac.denied", "permission", fmt.Sprintf("userId=%d permission=%s", user.ID, permission), clientIP(r), r.UserAgent())
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "permission denied"})
			return
		}

		// Add user to context (simple approach: set in header for downstream)
		r.Header.Set("X-User-ID", fmt.Sprint(user.ID))
		r.Header.Set("X-User-Role", user.RoleName)

		next(w, r)
	}
}

// getUserFromRequest extracts user from session token
func (a *App) getUserFromRequest(r *http.Request) (*User, bool) {
	// Try session token from cookie or header
	var sessionID string
	if c, err := r.Cookie("lx_session"); err == nil {
		sessionID = c.Value
	}
	if sessionID == "" {
		sessionID = r.Header.Get("X-Session-Token")
	}

	// Fallback to legacy admin token
	if sessionID == "" {
		token := r.Header.Get("X-Admin-Token")
		if token == "" {
			token = r.URL.Query().Get("token")
		}
		if token == "" {
			if c, err := r.Cookie("lx_token"); err == nil {
				token = c.Value
			}
		}
		if token == a.adminToken {
			// Return admin user
			return a.getUserByUsername("admin")
		}
	}

	if sessionID == "" {
		return nil, false
	}

	// Get session
	session, ok := getSession(sessionID)
	if !ok {
		// Try database session
		session, ok = a.getDBSession(sessionID)
		if !ok {
			return nil, false
		}
	}

	// Get user
	return a.getUserByID(session.UserID)
}

// getSession retrieves a session from memory
func getSession(id string) (*Session, bool) {
	sessionsMu.RLock()
	defer sessionsMu.RUnlock()
	s, ok := sessions[id]
	if !ok {
		return nil, false
	}
	if time.Now().After(s.ExpiresAt) {
		return nil, false
	}
	return s, true
}

// getDBSession retrieves a session from database
func (a *App) getDBSession(id string) (*Session, bool) {
	var s Session
	var createdAt, expiresAt string
	err := a.db.QueryRow(
		`SELECT id, user_id, created_at, expires_at, ip, user_agent FROM sessions WHERE id = ? AND expires_at > ?`,
		id, time.Now().Format(time.RFC3339),
	).Scan(&s.ID, &s.UserID, &createdAt, &expiresAt, &s.IP, &s.UserAgent)
	if err != nil {
		return nil, false
	}
	s.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	s.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt)
	return &s, true
}

// getUserByID retrieves a user by ID
func (a *App) getUserByID(id int64) (*User, bool) {
	var u User
	var createdAt, updatedAt string
	var lastLogin sql.NullString
	var active, totpEnabled int
	err := a.db.QueryRow(`
		SELECT u.id, u.username, u.email, u.role_id, u.active, u.totp_enabled, u.created_at, u.updated_at, u.last_login, r.name
		FROM users u JOIN roles r ON u.role_id = r.id WHERE u.id = ?`,
		id,
	).Scan(&u.ID, &u.Username, &u.Email, &u.RoleID, &active, &totpEnabled, &createdAt, &updatedAt, &lastLogin, &u.RoleName)
	if err != nil {
		return nil, false
	}
	u.Active = active == 1
	u.TOTPEnabled = totpEnabled == 1
	u.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	u.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if lastLogin.Valid {
		t, _ := time.Parse(time.RFC3339, lastLogin.String)
		u.LastLogin = &t
	}
	return &u, true
}

// getUserByUsername retrieves a user by username
func (a *App) getUserByUsername(username string) (*User, bool) {
	var u User
	var createdAt, updatedAt string
	var lastLogin sql.NullString
	var active, totpEnabled int
	err := a.db.QueryRow(`
		SELECT u.id, u.username, u.email, u.role_id, u.active, u.totp_enabled, u.created_at, u.updated_at, u.last_login, r.name
		FROM users u JOIN roles r ON u.role_id = r.id WHERE u.username = ?`,
		username,
	).Scan(&u.ID, &u.Username, &u.Email, &u.RoleID, &active, &totpEnabled, &createdAt, &updatedAt, &lastLogin, &u.RoleName)
	if err != nil {
		return nil, false
	}
	u.Active = active == 1
	u.TOTPEnabled = totpEnabled == 1
	u.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	u.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if lastLogin.Valid {
		t, _ := time.Parse(time.RFC3339, lastLogin.String)
		u.LastLogin = &t
	}
	return &u, true
}

// hasPermission checks if a role has a specific permission
func (a *App) hasPermission(roleID int64, permission string) bool {
	var permissionsJSON string
	err := a.db.QueryRow(`SELECT permissions FROM roles WHERE id = ?`, roleID).Scan(&permissionsJSON)
	if err != nil {
		return false
	}

	var permissions []string
	if err := json.Unmarshal([]byte(permissionsJSON), &permissions); err != nil {
		return false
	}

	for _, p := range permissions {
		if p == permission {
			return true
		}
	}
	return false
}

// handleUserLogin handles user login
func (a *App) handleUserLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		TOTPCode string `json:"totpCode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	username := strings.TrimSpace(req.Username)
	password := req.Password

	if username == "" || password == "" {
		http.Error(w, "username and password required", http.StatusBadRequest)
		return
	}

	// Get user
	var u User
	var passwordHash string
	var active, totpEnabled int
	var totpSecret string
	err := a.db.QueryRow(`
		SELECT u.id, u.username, u.email, u.role_id, u.active, u.totp_enabled, u.totp_secret, u.password_hash, r.name
		FROM users u JOIN roles r ON u.role_id = r.id WHERE u.username = ?`,
		username,
	).Scan(&u.ID, &u.Username, &u.Email, &u.RoleID, &active, &totpEnabled, &totpSecret, &passwordHash, &u.RoleName)
	if err != nil {
		_ = a.insertLoginAudit(clientIP(r), r.UserAgent(), false, "user_not_found")
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	u.Active = active == 1
	u.TOTPEnabled = totpEnabled == 1

	// Check if user is active
	if !u.Active {
		_ = a.insertLoginAudit(clientIP(r), r.UserAgent(), false, "account_disabled")
		http.Error(w, "account disabled", http.StatusForbidden)
		return
	}

	// Verify password
	if bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)) != nil {
		_ = a.insertLoginAudit(clientIP(r), r.UserAgent(), false, "invalid_password")
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	// Check TOTP if enabled
	if u.TOTPEnabled {
		totpCode := strings.TrimSpace(req.TOTPCode)
		if totpCode == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":          false,
				"requiresTOTP": true,
				"message":     "TOTP code required",
			})
			return
		}
		if !verifyTOTP(totpSecret, totpCode) {
			_ = a.insertLoginAudit(clientIP(r), r.UserAgent(), false, "invalid_totp")
			http.Error(w, "invalid TOTP code", http.StatusUnauthorized)
			return
		}
	}

	// Create session
	sessionID, err := generateSessionID()
	if err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}

	now := time.Now()
	expiresAt := now.Add(sessionTTL)
	session := &Session{
		ID:        sessionID,
		UserID:    u.ID,
		CreatedAt: now,
		ExpiresAt: expiresAt,
		IP:        clientIP(r),
		UserAgent: r.UserAgent(),
	}

	// Store session in memory
	sessionsMu.Lock()
	sessions[sessionID] = session
	sessionsMu.Unlock()

	// Store session in database
	_, err = a.db.Exec(
		`INSERT INTO sessions(id, user_id, created_at, expires_at, ip, user_agent) VALUES(?, ?, ?, ?, ?, ?)`,
		session.ID, session.UserID, session.CreatedAt.Format(time.RFC3339), session.ExpiresAt.Format(time.RFC3339), session.IP, session.UserAgent,
	)
	if err != nil {
		// Continue even if DB insert fails
	}

	// Update last login
	_, _ = a.db.Exec(`UPDATE users SET last_login = ? WHERE id = ?`, now.Format(time.RFC3339), u.ID)

	_ = a.insertLoginAudit(clientIP(r), r.UserAgent(), true, "ok")

	// Set session cookie
	secure := a.requestSecure(r)
	http.SetCookie(w, &http.Cookie{
		Name:     "lx_session",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})

	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":        true,
		"sessionId": sessionID,
		"user": map[string]any{
			"id":       u.ID,
			"username": u.Username,
			"role":     u.RoleName,
		},
	})
}

// handleUserLogout handles user logout
func (a *App) handleUserLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Get session ID
	var sessionID string
	if c, err := r.Cookie("lx_session"); err == nil {
		sessionID = c.Value
	}

	if sessionID != "" {
		// Remove from memory
		sessionsMu.Lock()
		delete(sessions, sessionID)
		sessionsMu.Unlock()

		// Remove from database
		_, _ = a.db.Exec(`DELETE FROM sessions WHERE id = ?`, sessionID)
	}

	// Clear session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "lx_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   a.requestSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// handleListUsers handles listing users
func (a *App) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	rows, err := a.db.Query(`
		SELECT u.id, u.username, u.email, u.role_id, u.active, u.totp_enabled, u.created_at, u.updated_at, u.last_login, r.name
		FROM users u JOIN roles r ON u.role_id = r.id ORDER BY u.id`,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		var createdAt, updatedAt string
		var lastLogin sql.NullString
		var active, totpEnabled int
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.RoleID, &active, &totpEnabled, &createdAt, &updatedAt, &lastLogin, &u.RoleName); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		u.Active = active == 1
		u.TOTPEnabled = totpEnabled == 1
		u.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		u.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		if lastLogin.Valid {
			t, _ := time.Parse(time.RFC3339, lastLogin.String)
			u.LastLogin = &t
		}
		users = append(users, u)
	}

	if users == nil {
		users = []User{}
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "items": users, "count": len(users)})
}

// handleCreateUser handles creating a new user
func (a *App) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
		RoleID   int64  `json:"roleId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	username := strings.TrimSpace(req.Username)
	password := strings.TrimSpace(req.Password)

	if username == "" || password == "" {
		http.Error(w, "username and password required", http.StatusBadRequest)
		return
	}

	if len(password) < 6 {
		http.Error(w, "password must be at least 6 characters", http.StatusBadRequest)
		return
	}

	if req.RoleID == 0 {
		req.RoleID = 3 // Default to viewer
	}

	// Hash password
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "failed to hash password", http.StatusInternalServerError)
		return
	}

	now := time.Now().Format(time.RFC3339)
	res, err := a.db.Exec(
		`INSERT INTO users(username, email, password_hash, role_id, active, created_at, updated_at) VALUES(?, ?, ?, ?, 1, ?, ?)`,
		username, req.Email, string(passwordHash), req.RoleID, now, now,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	id, _ := res.LastInsertId()
	_ = a.insertOpAudit("user.create", "user", fmt.Sprintf("id=%d username=%s", id, username), clientIP(r), r.UserAgent())

	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id})
}

// handleUpdateUser handles updating a user
func (a *App) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID       int64   `json:"id"`
		Email    *string `json:"email"`
		Password *string `json:"password"`
		RoleID   *int64  `json:"roleId"`
		Active   *bool   `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.ID == 0 {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}

	updates := []string{}
	args := []any{}

	if req.Email != nil {
		updates = append(updates, "email = ?")
		args = append(args, *req.Email)
	}

	if req.Password != nil && *req.Password != "" {
		passwordHash, err := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		if err != nil {
			http.Error(w, "failed to hash password", http.StatusInternalServerError)
			return
		}
		updates = append(updates, "password_hash = ?")
		args = append(args, string(passwordHash))
	}

	if req.RoleID != nil {
		updates = append(updates, "role_id = ?")
		args = append(args, *req.RoleID)
	}

	if req.Active != nil {
		updates = append(updates, "active = ?")
		var active int
		if *req.Active {
			active = 1
		}
		args = append(args, active)
	}

	if len(updates) == 0 {
		http.Error(w, "no fields to update", http.StatusBadRequest)
		return
	}

	updates = append(updates, "updated_at = ?")
	args = append(args, time.Now().Format(time.RFC3339))
	args = append(args, req.ID)

	query := "UPDATE users SET " + strings.Join(updates, ", ") + " WHERE id = ?"
	_, err := a.db.Exec(query, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_ = a.insertOpAudit("user.update", "user", fmt.Sprintf("id=%d", req.ID), clientIP(r), r.UserAgent())

	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// handleDeleteUser handles deleting a user
func (a *App) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.ID == 0 {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}

	// Prevent deleting the last admin
	var roleID int64
	err := a.db.QueryRow(`SELECT role_id FROM users WHERE id = ?`, req.ID).Scan(&roleID)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if roleID == 1 { // Admin role
		var adminCount int
		a.db.QueryRow(`SELECT COUNT(1) FROM users WHERE role_id = 1`).Scan(&adminCount)
		if adminCount <= 1 {
			http.Error(w, "cannot delete the last admin user", http.StatusBadRequest)
			return
		}
	}

	// Delete user's sessions
	_, _ = a.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, req.ID)

	// Delete user
	_, err = a.db.Exec(`DELETE FROM users WHERE id = ?`, req.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_ = a.insertOpAudit("user.delete", "user", fmt.Sprintf("id=%d", req.ID), clientIP(r), r.UserAgent())

	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// handleListRoles handles listing roles
func (a *App) handleListRoles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	rows, err := a.db.Query(`SELECT id, name, description, permissions, created_at FROM roles ORDER BY id`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var roles []Role
	for rows.Next() {
		var r Role
		var createdAt, permissionsJSON string
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &permissionsJSON, &createdAt); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.Unmarshal([]byte(permissionsJSON), &r.Permissions)
		r.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		roles = append(roles, r)
	}

	if roles == nil {
		roles = []Role{}
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "items": roles, "count": len(roles)})
}

// generateSessionID generates a random session ID
func generateSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// startSessionCleanupLoop periodically cleans up expired sessions
func (a *App) startSessionCleanupLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		a.cleanupSessions()
	}
}

// cleanupSessions removes expired sessions from memory and database
func (a *App) cleanupSessions() {
	now := time.Now()

	// Clean memory sessions
	sessionsMu.Lock()
	for id, s := range sessions {
		if now.After(s.ExpiresAt) {
			delete(sessions, id)
		}
	}
	sessionsMu.Unlock()

	// Clean database sessions
	_, _ = a.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now.Format(time.RFC3339))
}

// secureCompare performs a constant-time comparison
func secureCompareString(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
