package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// User represents a system user
type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Email        string    `json:"email"`
	RoleID       int64     `json:"roleId"`
	RoleName     string    `json:"roleName,omitempty"`
	Enabled      bool      `json:"enabled"`
	TotpSecret   string    `json:"-"`
	TotpEnabled  bool      `json:"totpEnabled"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// Role represents a user role
type Role struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Permission points
const (
	PermProvidersRead   = "providers:read"
	PermProvidersWrite  = "providers:write"
	PermActivate        = "activate"
	PermRollback        = "rollback"
	PermAuditsRead      = "audits:read"
	PermAuditsExport    = "audits:export"
	PermSettingsWrite   = "settings:write"
	PermUsersRead       = "users:read"
	PermUsersWrite      = "users:write"
	PermMetricsRead     = "metrics:read"
)

// Built-in roles
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

var builtInRoles = map[string][]string{
	RoleAdmin: {
		PermProvidersRead, PermProvidersWrite,
		PermActivate, PermRollback,
		PermAuditsRead, PermAuditsExport,
		PermSettingsWrite,
		PermUsersRead, PermUsersWrite,
		PermMetricsRead,
	},
	RoleOperator: {
		PermProvidersRead, PermProvidersWrite,
		PermActivate,
		PermAuditsRead,
		PermMetricsRead,
	},
	RoleViewer: {
		PermProvidersRead,
		PermAuditsRead,
		PermMetricsRead,
	},
}

// initRBACSchema creates RBAC tables
func (a *App) initRBACSchema() error {
	schema := `
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  email TEXT,
  role_id INTEGER NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  totp_secret TEXT,
  totp_enabled INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (role_id) REFERENCES roles(id)
);

CREATE TABLE IF NOT EXISTS roles (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  description TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS role_permissions (
  role_id INTEGER NOT NULL,
  permission TEXT NOT NULL,
  PRIMARY KEY (role_id, permission),
  FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS sessions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  token TEXT NOT NULL UNIQUE,
  user_id INTEGER NOT NULL,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_users_enabled ON users(enabled);
CREATE INDEX IF NOT EXISTS idx_role_permissions_role ON role_permissions(role_id);
CREATE INDEX IF NOT EXISTS idx_sessions_token ON sessions(token);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);
`
	if _, err := a.db.Exec(schema); err != nil {
		return err
	}

	// Insert built-in roles if not exist
	for roleName, perms := range builtInRoles {
		var roleID int64
		err := a.db.QueryRow("SELECT id FROM roles WHERE name = ?", roleName).Scan(&roleID)
		if err == sql.ErrNoRows {
			desc := fmt.Sprintf("Built-in %s role", roleName)
			res, err := a.db.Exec("INSERT INTO roles (name, description, created_at) VALUES (?, ?, ?)",
				roleName, desc, time.Now().Format(time.RFC3339))
			if err != nil {
				return err
			}
			roleID, _ = res.LastInsertId()

			// Insert permissions
			for _, perm := range perms {
				_, err = a.db.Exec("INSERT INTO role_permissions (role_id, permission) VALUES (?, ?)",
					roleID, perm)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// hashPassword hashes a password using bcrypt
func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// verifyPassword verifies a password against a hash
func verifyPassword(hash, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// generateTOTPSecret generates a random TOTP secret
func generateTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// getUserByUsername retrieves a user by username
func (a *App) getUserByUsername(username string) (*User, error) {
	var u User
	var roleID int64
	err := a.db.QueryRow(`
		SELECT id, username, password_hash, email, role_id, enabled,
		       COALESCE(totp_secret, ''), totp_enabled, created_at, updated_at
		FROM users WHERE username = ?`, username).Scan(
		&u.ID, &u.Username, &u.PasswordHash, &u.Email, &roleID,
		&u.Enabled, &u.TotpSecret, &u.TotpEnabled, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	u.RoleID = roleID

	// Get role name
	err = a.db.QueryRow("SELECT name FROM roles WHERE id = ?", roleID).Scan(&u.RoleName)
	if err != nil {
		return nil, err
	}

	return &u, nil
}

// getUserPermissions retrieves all permissions for a user
func (a *App) getUserPermissions(userID int64) ([]string, error) {
	rows, err := a.db.Query(`
		SELECT rp.permission
		FROM users u
		JOIN role_permissions rp ON u.role_id = rp.role_id
		WHERE u.id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var perms []string
	for rows.Next() {
		var perm string
		if err := rows.Scan(&perm); err != nil {
			return nil, err
		}
		perms = append(perms, perm)
	}
	return perms, rows.Err()
}

// hasPermission checks if a user has a specific permission
func (a *App) hasPermission(userID int64, permission string) (bool, error) {
	var count int
	err := a.db.QueryRow(`
		SELECT COUNT(*)
		FROM users u
		JOIN role_permissions rp ON u.role_id = rp.role_id
		WHERE u.id = ? AND rp.permission = ? AND u.enabled = 1`, userID, permission).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// createUser creates a new user
func (a *App) createUser(username, password, email string, roleID int64) (*User, error) {
	if username == "" || password == "" {
		return nil, errors.New("username and password required")
	}

	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}

	now := time.Now().Format(time.RFC3339)
	res, err := a.db.Exec(`
		INSERT INTO users (username, password_hash, email, role_id, enabled, totp_enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, 0, ?, ?)`,
		username, hash, email, roleID, now, now)
	if err != nil {
		return nil, err
	}

	id, _ := res.LastInsertId()
	return a.getUserByID(id)
}

// getUserByID retrieves a user by ID
func (a *App) getUserByID(id int64) (*User, error) {
	var u User
	err := a.db.QueryRow(`
		SELECT u.id, u.username, u.password_hash, u.email, u.role_id, u.enabled,
		       COALESCE(u.totp_secret, ''), u.totp_enabled, u.created_at, u.updated_at, r.name
		FROM users u
		JOIN roles r ON u.role_id = r.id
		WHERE u.id = ?`, id).Scan(
		&u.ID, &u.Username, &u.PasswordHash, &u.Email, &u.RoleID,
		&u.Enabled, &u.TotpSecret, &u.TotpEnabled, &u.CreatedAt, &u.UpdatedAt, &u.RoleName,
	)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// listUsers lists all users
func (a *App) listUsers() ([]User, error) {
	rows, err := a.db.Query(`
		SELECT u.id, u.username, u.email, u.role_id, u.enabled, u.totp_enabled, u.created_at, u.updated_at, r.name
		FROM users u
		JOIN roles r ON u.role_id = r.id
		ORDER BY u.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.RoleID, &u.Enabled, &u.TotpEnabled, &u.CreatedAt, &u.UpdatedAt, &u.RoleName); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// updateUser updates user information
func (a *App) updateUser(id int64, email string, roleID int64, enabled bool) error {
	now := time.Now().Format(time.RFC3339)
	_, err := a.db.Exec(`
		UPDATE users SET email = ?, role_id = ?, enabled = ?, updated_at = ?
		WHERE id = ?`, email, roleID, enabled, now, id)
	return err
}

// updateUserPassword updates user password
func (a *App) updateUserPassword(id int64, newPassword string) error {
	hash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}

	now := time.Now().Format(time.RFC3339)
	_, err = a.db.Exec(`
		UPDATE users SET password_hash = ?, updated_at = ?
		WHERE id = ?`, hash, now, id)
	return err
}

// deleteUser deletes a user
func (a *App) deleteUser(id int64) error {
	_, err := a.db.Exec("DELETE FROM users WHERE id = ?", id)
	return err
}

// listRoles lists all roles
func (a *App) listRoles() ([]Role, error) {
	rows, err := a.db.Query(`
		SELECT id, name, description, created_at
		FROM roles
		ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roles []Role
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &r.CreatedAt); err != nil {
			return nil, err
		}

		// Get permissions
		permRows, err := a.db.Query("SELECT permission FROM role_permissions WHERE role_id = ?", r.ID)
		if err != nil {
			return nil, err
		}
		for permRows.Next() {
			var perm string
			if err := permRows.Scan(&perm); err != nil {
				permRows.Close()
				return nil, err
			}
			r.Permissions = append(r.Permissions, perm)
		}
		permRows.Close()

		roles = append(roles, r)
	}
	return roles, rows.Err()
}

// Middleware: withRBACAuth - authenticates user and checks permission
func (a *App) withRBACAuth(permission string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Try to get user from session/cookie
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
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
				return
			}
		}

		// Store user ID in context for handlers
		// For now, we'll use a simple approach with headers
		// In production, use context.Context
		next(w, r)
	}
}

// getUserIDFromRequest extracts user ID from request (session/cookie/token)
func (a *App) getUserIDFromRequest(r *http.Request) (int64, error) {
	// Check for session cookie
	cookie, err := r.Cookie("lx_session")
	if err == nil && cookie.Value != "" {
		// Validate session and get user ID
		userID, err := a.validateSession(cookie.Value)
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
func (a *App) validateSession(sessionToken string) (int64, error) {
	var userID int64
	var expiresAt string
	err := a.db.QueryRow(`
		SELECT user_id, expires_at FROM sessions WHERE token = ?`, sessionToken).Scan(&userID, &expiresAt)
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

// createSession creates a new session for a user
func (a *App) createSession(userID int64, duration time.Duration) (string, error) {
	// Generate random session token
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.URLEncoding.EncodeToString(b)

	expiresAt := time.Now().Add(duration).Format(time.RFC3339)
	createdAt := time.Now().Format(time.RFC3339)

	_, err := a.db.Exec(`
		INSERT INTO sessions (token, user_id, expires_at, created_at)
		VALUES (?, ?, ?, ?)`, token, userID, expiresAt, createdAt)
	if err != nil {
		return "", err
	}

	return token, nil
}

// deleteSession deletes a session
func (a *App) deleteSession(token string) error {
	_, err := a.db.Exec("DELETE FROM sessions WHERE token = ?", token)
	return err
}

// cleanupExpiredSessions removes expired sessions
func (a *App) cleanupExpiredSessions() error {
	now := time.Now().Format(time.RFC3339)
	_, err := a.db.Exec("DELETE FROM sessions WHERE expires_at < ?", now)
	return err
}

// startSessionCleanupLoop periodically cleans up expired sessions
func (a *App) startSessionCleanupLoop() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		if err := a.cleanupExpiredSessions(); err != nil {
			log.Printf("session cleanup error: %v", err)
		}
	}
}


// API Handlers

// handleUserLogin handles user login with RBAC
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
		a.recordFailedAttempt(ip)
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
		a.recordFailedAttempt(ip)
		_ = a.insertLoginAudit(ip, ua, false, "invalid_credentials")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid credentials"})
		return
	}

	// Check 2FA if enabled
	if user.TotpEnabled {
		if req.TotpCode == "" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "totp_required", "message": "2FA code required"})
			return
		}

		// Verify TOTP code (implementation needed in totp.go)
		if !a.verifyTOTP(user.TotpSecret, req.TotpCode) {
			a.recordFailedAttempt(ip)
			_ = a.insertLoginAudit(ip, ua, false, "invalid_totp")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid 2FA code"})
			return
		}
	}

	// Create session
	sessionToken, err := a.createSession(user.ID, 24*time.Hour)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to create session"})
		return
	}

	// Set session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "lx_session",
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.requestSecure(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   86400, // 24 hours
	})

	a.clearFailedAttempts(ip)
	_ = a.insertLoginAudit(ip, ua, true, "")

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

	cookie, err := r.Cookie("lx_session")
	if err == nil && cookie.Value != "" {
		_ = a.deleteSession(cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "lx_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})

	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// handleListUsers lists all users (admin only)
func (a *App) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.listUsers()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(users)
}

// handleCreateUser creates a new user (admin only)
func (a *App) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Email    string `json:"email"`
		RoleID   int64  `json:"roleId"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}

	user, err := a.createUser(req.Username, req.Password, req.Email, req.RoleID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(user)
}

// handleUpdateUser updates a user (admin only)
func (a *App) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Extract user ID from URL
	// For simplicity, assume it's in query param
	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "user id required"})
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid user id"})
		return
	}

	var req struct {
		Email   string `json:"email"`
		RoleID  int64  `json:"roleId"`
		Enabled bool   `json:"enabled"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid request"})
		return
	}

	if err := a.updateUser(id, req.Email, req.RoleID, req.Enabled); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	user, err := a.getUserByID(id)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	_ = json.NewEncoder(w).Encode(user)
}

// handleDeleteUser deletes a user (admin only)
func (a *App) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "user id required"})
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid user id"})
		return
	}

	if err := a.deleteUser(id); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// handleListRoles lists all roles
func (a *App) handleListRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := a.listRoles()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(roles)
}
