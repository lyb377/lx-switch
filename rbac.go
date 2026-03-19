package main

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
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
	PermActivate       = "activate"
	PermRollback       = "rollback"
	PermMetricsRead    = "metrics:read"
)

// Role constants
const (
	RoleAdmin    = "admin"
	RoleEditor   = "editor"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

// User represents a user in the system
type User struct {
	ID           int64      `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"-"`
	Email        string     `json:"email,omitempty"`
	RoleID       int64      `json:"roleId"`
	RoleName     string     `json:"roleName,omitempty"`
	Enabled      bool       `json:"enabled"`
	TotpSecret   string     `json:"-"`
	TOTPEnabled  bool       `json:"totpEnabled"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	LastLogin    *time.Time `json:"lastLogin,omitempty"`
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
	sessions   = make(map[string]*Session)
	sessionsMu sync.RWMutex
	sessionTTL = 24 * time.Hour
)

// Built-in roles permissions
var builtInRolePermissions = map[string][]string{
	RoleAdmin: {
		PermProvidersRead, PermProvidersWrite,
		PermUsersRead, PermUsersWrite,
		PermAuditRead, PermAuditCleanup,
		PermSecurityRead, PermSecurityWrite,
		PermBackupsRead, PermBackupsWrite,
		PermActivate, PermRollback, PermMetricsRead,
	},
	RoleEditor: {
		PermProvidersRead, PermProvidersWrite,
		PermAuditRead, PermBackupsRead,
		PermActivate, PermMetricsRead,
	},
	RoleOperator: {
		PermProvidersRead, PermActivate,
		PermAuditRead, PermMetricsRead,
	},
	RoleViewer: {
		PermProvidersRead, PermAuditRead, PermMetricsRead,
	},
}

func hashPassword(password string) (string, error) {
	password = strings.TrimSpace(password)
	if password == "" {
		return "", errors.New("password required")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func verifyPassword(hash string, password string) bool {
	if strings.TrimSpace(hash) == "" || strings.TrimSpace(password) == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

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
		enabled INTEGER NOT NULL DEFAULT 1,
		totp_enabled INTEGER NOT NULL DEFAULT 0,
		totp_secret TEXT DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		last_login TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
	CREATE INDEX IF NOT EXISTS idx_users_enabled ON users(enabled);

	-- Roles table
	CREATE TABLE IF NOT EXISTS roles (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		description TEXT,
		permissions TEXT NOT NULL DEFAULT '[]',
		created_at TEXT NOT NULL
	);

	-- Role permissions table (alternative storage)
	CREATE TABLE IF NOT EXISTS role_permissions (
		role_id INTEGER NOT NULL,
		permission TEXT NOT NULL,
		PRIMARY KEY (role_id, permission),
		FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE CASCADE
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
		permissions []string
	}{
		{name: RoleAdmin, description: "Full system access", permissions: builtInRolePermissions[RoleAdmin]},
		{name: RoleEditor, description: "Can manage providers and view audit logs", permissions: builtInRolePermissions[RoleEditor]},
		{name: RoleOperator, description: "Can activate providers", permissions: builtInRolePermissions[RoleOperator]},
		{name: RoleViewer, description: "Read-only access", permissions: builtInRolePermissions[RoleViewer]},
	}

	for _, r := range roles {
		permsJSON, _ := json.Marshal(r.permissions)
		_, err := a.db.Exec(
			`INSERT INTO roles(name, description, permissions, created_at) VALUES(?, ?, ?, ?)`,
			r.name, r.description, string(permsJSON), now,
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
	passwordHash, err := hashPassword(a.adminToken)
	if err != nil {
		return err
	}

	_, err = a.db.Exec(
		`INSERT INTO users(username, email, password_hash, role_id, enabled, created_at, updated_at) VALUES(?, ?, ?, 1, 1, ?, ?)`,
		"admin", "admin@localhost", passwordHash, now, now,
	)
	return err
}

// withRBACAuth is a middleware that checks if the user has the required permission
func (a *App) withRBACAuth(permission string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get user from session
		userID, err := a.getUserIDFromRequest(r)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}

		// Check permission
		if permission != "" {
			ok, err := a.hasPermission(userID, permission)
			if err != nil || !ok {
				_ = a.insertOpAudit("rbac.denied", "permission", fmt.Sprintf("userId=%d permission=%s", userID, permission), clientIP(r), r.UserAgent())
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
				return
			}
		}

		// Store user ID in header for downstream handlers
		r.Header.Set("X-User-ID", fmt.Sprint(userID))
		next(w, r)
	}
}

func (a *App) withRBACMethodAuth(methodPermissions map[string]string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := a.getUserIDFromRequest(r)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}

		method := r.Method
		if method == http.MethodHead {
			method = http.MethodGet
		}
		permission := strings.TrimSpace(methodPermissions[method])
		if permission == "" {
			permission = strings.TrimSpace(methodPermissions["*"])
		}

		if permission != "" {
			ok, err := a.hasPermission(userID, permission)
			if err != nil || !ok {
				_ = a.insertOpAudit("rbac.denied", "permission", fmt.Sprintf("userId=%d permission=%s method=%s path=%s", userID, permission, r.Method, r.URL.Path), clientIP(r), r.UserAgent())
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
				return
			}
		}

		r.Header.Set("X-User-ID", fmt.Sprint(userID))
		next(w, r)
	}
}

// getUserIDFromRequest extracts user ID from request (session/cookie/token)
func (a *App) getUserIDFromRequest(r *http.Request) (int64, error) {
	// Check for session cookie
	if c, err := r.Cookie("lx_session"); err == nil && c.Value != "" {
		// Validate session and get user ID
		userID, err := a.validateSession(c.Value)
		if err == nil {
			return userID, nil
		}
	}

	// Fallback: check for legacy token (for backward compatibility)
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
		// Legacy token mode: return admin user ID (ID 1)
		return 1, nil
	}

	return 0, errors.New("no valid authentication")
}

// validateSession validates a session token and returns user ID
func (a *App) validateSession(sessionID string) (int64, error) {
	// Check memory cache first
	sessionsMu.RLock()
	s, ok := sessions[sessionID]
	sessionsMu.RUnlock()
	if ok && time.Now().Before(s.ExpiresAt) {
		// Verify session still exists in database (handles cases where DB session was deleted)
		var dbUserID int64
		err := a.db.QueryRow(`SELECT user_id FROM sessions WHERE id = ?`, sessionID).Scan(&dbUserID)
		if err != nil {
			// Session was deleted from DB, remove from memory cache
			sessionsMu.Lock()
			delete(sessions, sessionID)
			sessionsMu.Unlock()
			return 0, errors.New("session invalidated")
		}
		return s.UserID, nil
	}

	// Check database
	var userID int64
	var expiresAt string
	err := a.db.QueryRow(`
		SELECT user_id, expires_at FROM sessions WHERE id = ?`, sessionID).Scan(&userID, &expiresAt)
	if err != nil {
		return 0, err
	}

	// Check expiration
	expires, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil || time.Now().After(expires) {
		return 0, errors.New("session expired")
	}

	return userID, nil
}

// clearUserSessions removes all sessions for a user from both memory and database
func (a *App) clearUserSessions(userID int64) error {
	// Clear memory sessions
	sessionsMu.Lock()
	for id, s := range sessions {
		if s.UserID == userID {
			delete(sessions, id)
		}
	}
	sessionsMu.Unlock()

	// Clear database sessions
	_, err := a.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// getUserByID retrieves a user by ID
func (a *App) getUserByID(id int64) (*User, error) {
	var u User
	var createdAt, updatedAt string
	var lastLogin sql.NullString
	var enabled, totpEnabled int
	err := a.db.QueryRow(`
		SELECT u.id, u.username, u.email, u.password_hash, u.role_id, u.enabled, u.totp_enabled, 
		       COALESCE(u.totp_secret, ''), u.created_at, u.updated_at, u.last_login, r.name
		FROM users u JOIN roles r ON u.role_id = r.id WHERE u.id = ?`,
		id,
	).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.RoleID, &enabled, &totpEnabled, &u.TotpSecret, &createdAt, &updatedAt, &lastLogin, &u.RoleName)
	if err != nil {
		return nil, err
	}
	u.Enabled = enabled == 1
	u.TOTPEnabled = totpEnabled == 1
	u.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	u.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if lastLogin.Valid {
		t, _ := time.Parse(time.RFC3339, lastLogin.String)
		u.LastLogin = &t
	}
	return &u, nil
}

// getUserByUsername retrieves a user by username
func (a *App) getUserByUsername(username string) (*User, error) {
	var u User
	var createdAt, updatedAt string
	var lastLogin sql.NullString
	var enabled, totpEnabled int
	err := a.db.QueryRow(`
		SELECT u.id, u.username, u.email, u.password_hash, u.role_id, u.enabled, u.totp_enabled,
		       COALESCE(u.totp_secret, ''), u.created_at, u.updated_at, u.last_login, r.name
		FROM users u JOIN roles r ON u.role_id = r.id WHERE u.username = ?`,
		username,
	).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.RoleID, &enabled, &totpEnabled, &u.TotpSecret, &createdAt, &updatedAt, &lastLogin, &u.RoleName)
	if err != nil {
		return nil, err
	}
	u.Enabled = enabled == 1
	u.TOTPEnabled = totpEnabled == 1
	u.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	u.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if lastLogin.Valid {
		t, _ := time.Parse(time.RFC3339, lastLogin.String)
		u.LastLogin = &t
	}
	return &u, nil
}

// hasPermission checks if a user has a specific permission
func (a *App) hasPermission(userID int64, permission string) (bool, error) {
	var permissionsJSON string
	err := a.db.QueryRow(`
		SELECT r.permissions FROM users u 
		JOIN roles r ON u.role_id = r.id 
		WHERE u.id = ? AND u.enabled = 1`, userID).Scan(&permissionsJSON)
	if err != nil {
		return false, err
	}

	var permissions []string
	if err := json.Unmarshal([]byte(permissionsJSON), &permissions); err != nil {
		return false, err
	}

	for _, p := range permissions {
		if p == permission {
			return true, nil
		}
	}
	return false, nil
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
		TotpCode string `json:"totpCode,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}

	ip := clientIP(r)
	ua := strings.TrimSpace(r.UserAgent())

	// Check rate limiting
	if wait, blocked := a.isBlocked(ip); blocked {
		_ = a.insertLoginAudit(ip, ua, false, "locked")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "too many attempts", "retryAfter": fmt.Sprintf("%d", int(wait.Seconds()))})
		return
	}

	// Get user
	user, err := a.getUserByUsername(req.Username)
	if err != nil {
		a.recordFailure(ip)
		_ = a.insertLoginAudit(ip, ua, false, "invalid_credentials")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid credentials"})
		return
	}

	// Check if user is enabled
	if !user.Enabled {
		_ = a.insertLoginAudit(ip, ua, false, "user_disabled")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "user disabled"})
		return
	}

	// Verify password
	if !verifyPassword(user.PasswordHash, req.Password) {
		a.recordFailure(ip)
		_ = a.insertLoginAudit(ip, ua, false, "invalid_credentials")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid credentials"})
		return
	}

	// Check 2FA if enabled
	if user.TOTPEnabled {
		if req.TotpCode == "" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "totp_required", "message": "2FA code required"})
			return
		}

		if !verifyTOTP(user.TotpSecret, req.TotpCode) {
			a.recordFailure(ip)
			_ = a.insertLoginAudit(ip, ua, false, "invalid_totp")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid 2FA code"})
			return
		}
	}

	// Create session
	sessionID, err := generateSessionID()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to create session"})
		return
	}

	now := time.Now()
	expiresAt := now.Add(sessionTTL)
	session := &Session{
		ID:        sessionID,
		UserID:    user.ID,
		CreatedAt: now,
		ExpiresAt: expiresAt,
		IP:        ip,
		UserAgent: ua,
	}

	// Store session in memory
	sessionsMu.Lock()
	sessions[sessionID] = session
	sessionsMu.Unlock()

	// Store session in database
	_, _ = a.db.Exec(
		`INSERT INTO sessions(id, user_id, created_at, expires_at, ip, user_agent) VALUES(?, ?, ?, ?, ?, ?)`,
		session.ID, session.UserID, session.CreatedAt.Format(time.RFC3339), session.ExpiresAt.Format(time.RFC3339), session.IP, session.UserAgent,
	)

	// Update last login
	_, _ = a.db.Exec(`UPDATE users SET last_login = ? WHERE id = ?`, now.Format(time.RFC3339), user.ID)

	a.clearFailures(ip)
	_ = a.insertLoginAudit(ip, ua, true, "ok")

	// Set session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "lx_session",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.requestSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"user": map[string]interface{}{
			"id":       user.ID,
			"username": user.Username,
			"email":    user.Email,
			"role":     user.RoleName,
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
	if c, err := r.Cookie("lx_session"); err == nil && c.Value != "" {
		// Remove from memory
		sessionsMu.Lock()
		delete(sessions, c.Value)
		sessionsMu.Unlock()

		// Remove from database
		_, _ = a.db.Exec(`DELETE FROM sessions WHERE id = ?`, c.Value)
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

	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// handleListUsers handles listing users
func (a *App) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	rows, err := a.db.Query(`
		SELECT u.id, u.username, u.email, u.role_id, u.enabled, u.totp_enabled, u.created_at, u.updated_at, u.last_login, r.name
		FROM users u JOIN roles r ON u.role_id = r.id ORDER BY u.id`,
	)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		var createdAt, updatedAt string
		var lastLogin sql.NullString
		var enabled, totpEnabled int
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.RoleID, &enabled, &totpEnabled, &createdAt, &updatedAt, &lastLogin, &u.RoleName); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		u.Enabled = enabled == 1
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

	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "items": users, "count": len(users)})
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
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}

	username := strings.TrimSpace(req.Username)
	password := strings.TrimSpace(req.Password)

	if username == "" || password == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "username and password required"})
		return
	}

	if len(password) < 6 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "password must be at least 6 characters"})
		return
	}

	if req.RoleID == 0 {
		req.RoleID = 4 // Default to viewer
	}

	// Hash password
	passwordHash, err := hashPassword(password)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to hash password"})
		return
	}

	now := time.Now().Format(time.RFC3339)
	res, err := a.db.Exec(
		`INSERT INTO users(username, email, password_hash, role_id, enabled, created_at, updated_at) VALUES(?, ?, ?, ?, 1, ?, ?)`,
		username, req.Email, passwordHash, req.RoleID, now, now,
	)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	id, _ := res.LastInsertId()
	_ = a.insertOpAudit("user.create", "user", fmt.Sprintf("id=%d username=%s", id, username), clientIP(r), r.UserAgent())

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "id": id})
}

// handleUpdateUser handles updating a user
func (a *App) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID       int64   `json:"id"`
		Email    *string `json:"email"`
		Password *string `json:"password"`
		RoleID   *int64  `json:"roleId"`
		Enabled  *bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}

	if req.ID == 0 {
		// Try to get ID from URL
		idStr := r.URL.Query().Get("id")
		if idStr != "" {
			req.ID, _ = strconv.ParseInt(idStr, 10, 64)
		}
	}

	if req.ID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "id required"})
		return
	}

	updates := []string{}
	args := []any{}

	if req.Email != nil {
		updates = append(updates, "email = ?")
		args = append(args, *req.Email)
	}

	if req.Password != nil && *req.Password != "" {
		passwordHash, err := hashPassword(*req.Password)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to hash password"})
			return
		}
		updates = append(updates, "password_hash = ?")
		args = append(args, passwordHash)
	}

	if req.RoleID != nil {
		updates = append(updates, "role_id = ?")
		args = append(args, *req.RoleID)
	}

	if req.Enabled != nil {
		updates = append(updates, "enabled = ?")
		var enabled int
		if *req.Enabled {
			enabled = 1
		}
		args = append(args, enabled)
	}

	if len(updates) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "no fields to update"})
		return
	}

	updates = append(updates, "updated_at = ?")
	args = append(args, time.Now().Format(time.RFC3339))
	args = append(args, req.ID)

	query := "UPDATE users SET " + strings.Join(updates, ", ") + " WHERE id = ?"
	_, err := a.db.Exec(query, args...)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	_ = a.insertOpAudit("user.update", "user", fmt.Sprintf("id=%d", req.ID), clientIP(r), r.UserAgent())

	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// handleDeleteUser handles deleting a user
func (a *App) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID int64 `json:"id"`
	}
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
			return
		}
	} else {
		// DELETE method, get ID from URL
		idStr := r.URL.Query().Get("id")
		if idStr == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "id required"})
			return
		}
		req.ID, _ = strconv.ParseInt(idStr, 10, 64)
	}

	if req.ID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "id required"})
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
		a.db.QueryRow(`SELECT COUNT(1) FROM users WHERE role_id = 1 AND enabled = 1`).Scan(&adminCount)
		if adminCount <= 1 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "cannot delete the last admin user"})
			return
		}
	}

	// Delete user's sessions (both memory and database)
	if err := a.clearUserSessions(req.ID); err != nil {
		log.Printf("warning: failed to clear user sessions: %v", err)
	}

	// Delete user
	result, err := a.db.Exec(`DELETE FROM users WHERE id = ?`, req.ID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "user not found"})
		return
	}

	_ = a.insertOpAudit("user.delete", "user", fmt.Sprintf("id=%d", req.ID), clientIP(r), r.UserAgent())

	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// handleListRoles handles listing roles
func (a *App) handleListRoles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	rows, err := a.db.Query(`SELECT id, name, description, permissions, created_at FROM roles ORDER BY id`)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	var roles []Role
	for rows.Next() {
		var r Role
		var createdAt, permissionsJSON string
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &permissionsJSON, &createdAt); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.Unmarshal([]byte(permissionsJSON), &r.Permissions)
		r.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		roles = append(roles, r)
	}

	if roles == nil {
		roles = []Role{}
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "items": roles, "count": len(roles)})
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
	_, err := a.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now.Format(time.RFC3339))
	if err != nil {
		log.Printf("session cleanup error: %v", err)
	}
}

// secureCompare performs a constant-time comparison
func secureCompareString(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
