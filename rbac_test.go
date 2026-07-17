package main

import (
	"database/sql"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestAppRBAC(t *testing.T) *App {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	app := &App{
		db:         db,
		adminToken: "test-admin-token",
		failed:     map[string]*attemptState{},
		maxAttempts: 3,
		window:      5 * time.Minute,
		lockout:     15 * time.Minute,
	}
	if err := app.initDB(); err != nil {
		t.Fatalf("initDB: %v", err)
	}
	if err := app.initRBACSchema(); err != nil {
		t.Fatalf("initRBACSchema: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return app
}

// Test IP allowlist functions
func TestIPInCIDRList(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		list     []string
		expected bool
	}{
		{
			name:     "IP in single IP list",
			ip:       "192.168.1.100",
			list:     []string{"192.168.1.100"},
			expected: true,
		},
		{
			name:     "IP in CIDR range",
			ip:       "192.168.1.100",
			list:     []string{"192.168.1.0/24"},
			expected: true,
		},
		{
			name:     "IP not in list",
			ip:       "10.0.0.1",
			list:     []string{"192.168.1.0/24"},
			expected: false,
		},
		{
			name:     "Multiple CIDRs",
			ip:       "172.16.0.50",
			list:     []string{"192.168.0.0/16", "172.16.0.0/12", "10.0.0.0/8"},
			expected: true,
		},
		{
			name:     "Empty list",
			ip:       "192.168.1.1",
			list:     []string{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			result := ipInCIDRList(ip, tt.list)
			if result != tt.expected {
				t.Errorf("ipInCIDRList(%s, %v) = %v, want %v", tt.ip, tt.list, result, tt.expected)
			}
		})
	}
}

func TestParseXFF(t *testing.T) {
	tests := []struct {
		name     string
		xff      string
		expected int
	}{
		{
			name:     "Single IP",
			xff:      "203.0.113.1",
			expected: 1,
		},
		{
			name:     "Multiple IPs",
			xff:      "203.0.113.1, 198.51.100.1, 192.0.2.1",
			expected: 3,
		},
		{
			name:     "Empty string",
			xff:      "",
			expected: 0,
		},
		{
			name:     "Invalid IP",
			xff:      "invalid, 192.0.2.1",
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseXFF(tt.xff)
			if len(result) != tt.expected {
				t.Errorf("parseXFF(%s) returned %d IPs, want %d", tt.xff, len(result), tt.expected)
			}
		})
	}
}

func TestGetRealIP(t *testing.T) {
	tests := []struct {
		name           string
		remoteAddr     string
		xff            string
		trustedProxies []string
		expected       string
	}{
		{
			name:           "Direct connection",
			remoteAddr:     "203.0.113.1:12345",
			xff:            "",
			trustedProxies: []string{},
			expected:       "203.0.113.1",
		},
		{
			name:           "Trusted proxy with XFF",
			remoteAddr:     "10.0.0.1:12345",
			xff:            "203.0.113.1",
			trustedProxies: []string{"10.0.0.0/8"},
			expected:       "203.0.113.1",
		},
		{
			name:           "Untrusted proxy",
			remoteAddr:     "203.0.113.1:12345",
			xff:            "192.0.2.1",
			trustedProxies: []string{"10.0.0.0/8"},
			expected:       "203.0.113.1",
		},
		{
			name:           "Chain of proxies",
			remoteAddr:     "10.0.0.1:12345",
			xff:            "203.0.113.1, 172.16.0.1, 10.0.0.2",
			trustedProxies: []string{"10.0.0.0/8", "172.16.0.0/12"},
			expected:       "203.0.113.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Mock request
			r := &http.Request{
				RemoteAddr: tt.remoteAddr,
				Header:     make(http.Header),
			}
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}

			result := getRealIP(r, tt.trustedProxies)
			if result != tt.expected {
				t.Errorf("getRealIP() = %s, want %s", result, tt.expected)
			}
		})
	}
}

// Test RBAC functions
func TestHashPassword(t *testing.T) {
	password := "SecurePassword123!"
	hash, err := hashPassword(password)
	if err != nil {
		t.Fatalf("hashPassword() error = %v", err)
	}

	if hash == "" {
		t.Error("hashPassword() returned empty hash")
	}

	if hash == password {
		t.Error("hashPassword() returned plaintext password")
	}
}

func TestVerifyPassword(t *testing.T) {
	password := "SecurePassword123!"
	hash, err := hashPassword(password)
	if err != nil {
		t.Fatalf("hashPassword() error = %v", err)
	}

	tests := []struct {
		name     string
		hash     string
		password string
		expected bool
	}{
		{
			name:     "Correct password",
			hash:     hash,
			password: password,
			expected: true,
		},
		{
			name:     "Wrong password",
			hash:     hash,
			password: "WrongPassword",
			expected: false,
		},
		{
			name:     "Empty password",
			hash:     hash,
			password: "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := verifyPassword(tt.hash, tt.password)
			if result != tt.expected {
				t.Errorf("verifyPassword() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGenerateTOTPSecret(t *testing.T) {
	secret, err := generateTOTPSecret()
	if err != nil {
		t.Fatalf("generateTOTPSecret() error = %v", err)
	}

	if secret == "" {
		t.Error("generateTOTPSecret() returned empty secret")
	}

	if len(secret) < 20 {
		t.Errorf("generateTOTPSecret() returned short secret: %d bytes", len(secret))
	}

	// Generate another and ensure they're different
	secret2, err := generateTOTPSecret()
	if err != nil {
		t.Fatalf("generateTOTPSecret() error = %v", err)
	}

	if secret == secret2 {
		t.Error("generateTOTPSecret() returned same secret twice")
	}
}

func TestGenerateTOTP(t *testing.T) {
	// Use a known secret for testing
	secret := "JBSWY3DPEHPK3PXP"

	// Generate code for current time
	now := time.Now().Unix() / 30
	code := generateTOTP(secret, now)

	if code == "" {
		t.Error("generateTOTP() returned empty code")
	}

	if len(code) != 6 {
		t.Errorf("generateTOTP() returned code of length %d, want 6", len(code))
	}

	// Verify code is numeric
	for _, c := range code {
		if c < '0' || c > '9' {
			t.Errorf("generateTOTP() returned non-numeric code: %s", code)
			break
		}
	}

	// Same time should generate same code
	code2 := generateTOTP(secret, now)
	if code != code2 {
		t.Errorf("generateTOTP() inconsistent: %s != %s", code, code2)
	}

	// Different time should generate different code (usually)
	code3 := generateTOTP(secret, now+1)
	if code == code3 {
		t.Log("Warning: generateTOTP() generated same code for different time (rare but possible)")
	}
}

func TestGenerateTOTPURI(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXP"
	issuer := "lx-switch"
	accountName := "admin"

	uri := generateTOTPURI(secret, issuer, accountName)

	expected := "otpauth://totp/lx-switch:admin?secret=JBSWY3DPEHPK3PXP&issuer=lx-switch"
	if uri != expected {
		t.Errorf("generateTOTPURI() = %s, want %s", uri, expected)
	}
}

func TestVerifyTOTPCode_RoundTrip(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXP"
	nowStep := time.Now().Unix() / 30
	code := generateTOTP(secret, nowStep)
	if code == "" {
		t.Fatalf("generateTOTP() returned empty code")
	}
	if !verifyTOTPCode(secret, code) {
		t.Fatalf("verifyTOTPCode() = false, want true")
	}
}

func TestWithIPAllowlistMiddleware_AllowDeny(t *testing.T) {
	app := newTestApp(t)

	// Ensure clean cache for this test.
	allowlistCache.mu.Lock()
	allowlistCache.entries = nil
	allowlistCache.mu.Unlock()
	t.Cleanup(func() {
		allowlistCache.mu.Lock()
		allowlistCache.entries = nil
		allowlistCache.mu.Unlock()
	})

	now := time.Now().Format(time.RFC3339)
	_, err := app.db.Exec(`INSERT INTO ip_allowlist(ip_cidr, description, enabled, created_at) VALUES(?, '', 1, ?)`, "192.0.2.0/24", now)
	if err != nil {
		t.Fatalf("seed ip_allowlist: %v", err)
	}
	_ = app.setState("ip_allowlist_enabled", "true")
	app.loadSecuritySettings()

	called := false
	okHandler := app.withIPAllowlist(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	// Allowed
	{
		called = false
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/meta", nil)
		req.RemoteAddr = "192.0.2.10:12345"
		okHandler(rr, req)
		if rr.Code != http.StatusOK || !called {
			t.Fatalf("allowed request: code=%d called=%v", rr.Code, called)
		}
	}

	// Denied
	{
		called = false
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/meta", nil)
		req.RemoteAddr = "203.0.113.1:12345"
		okHandler(rr, req)
		if rr.Code != http.StatusForbidden || called {
			t.Fatalf("denied request: code=%d called=%v", rr.Code, called)
		}
	}
}

func TestTOTPRecoveryCodes_ConsumeOnce(t *testing.T) {
	app := newTestAppRBAC(t)
	admin, err := app.getUserByUsername("admin")
	if err != nil {
		t.Fatalf("getUserByUsername(admin): %v", err)
	}
	codes, err := app.replaceRecoveryCodes(admin.ID)
	if err != nil {
		t.Fatalf("replaceRecoveryCodes: %v", err)
	}
	if len(codes) != 8 {
		t.Fatalf("expected 8 recovery codes, got %d", len(codes))
	}
	ok, err := app.consumeRecoveryCode(admin.ID, codes[0])
	if err != nil {
		t.Fatalf("consumeRecoveryCode: %v", err)
	}
	if !ok {
		t.Fatalf("expected first consume ok=true")
	}
	ok, err = app.consumeRecoveryCode(admin.ID, codes[0])
	if err != nil {
		t.Fatalf("consumeRecoveryCode second: %v", err)
	}
	if ok {
		t.Fatalf("expected second consume ok=false")
	}
}

func TestHasPermission_AdminVsViewer(t *testing.T) {
	app := newTestAppRBAC(t)

	var adminID int64
	if err := app.db.QueryRow(`SELECT id FROM users WHERE username = ?`, "admin").Scan(&adminID); err != nil {
		t.Fatalf("lookup admin: %v", err)
	}

	ok, err := app.hasPermission(adminID, PermUsersWrite)
	if err != nil || !ok {
		t.Fatalf("admin should have %s, ok=%v err=%v", PermUsersWrite, ok, err)
	}

	viewerHash, err := hashPassword("viewer-pass")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	now := time.Now().Format(time.RFC3339)
	res, err := app.db.Exec(`INSERT INTO users(username, email, password_hash, role_id, enabled, created_at, updated_at) VALUES(?, ?, ?, ?, 1, ?, ?)`,
		"viewer", "viewer@localhost", viewerHash, 4, now, now,
	)
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	viewerID, _ := res.LastInsertId()

	ok, err = app.hasPermission(viewerID, PermUsersWrite)
	if err == nil && ok {
		t.Fatalf("viewer should not have %s", PermUsersWrite)
	}
}

// Test rate limiting
func TestAttemptState(t *testing.T) {
	app := &App{
		failed:      make(map[string]*attemptState),
		maxAttempts: 3,
		window:      5 * time.Minute,
		lockout:     15 * time.Minute,
	}

	ip := "192.0.2.1"

	// First attempt
	_, _ = app.recordFailure(ip)
	wait, blocked := app.isBlocked(ip)
	if blocked {
		t.Error("isBlocked() = true after 1 attempt, want false")
	}

	// Second attempt
	_, _ = app.recordFailure(ip)
	wait, blocked = app.isBlocked(ip)
	if blocked {
		t.Error("isBlocked() = true after 2 attempts, want false")
	}

	// Third attempt (should trigger lockout)
	retryAfterS, locked := app.recordFailure(ip)
	wait, blocked = app.isBlocked(ip)
	if !blocked {
		t.Error("isBlocked() = false after 3 attempts, want true")
	}
	if !locked || retryAfterS <= 0 {
		t.Errorf("recordFailure() = (retryAfterS=%d locked=%v), want locked with retryAfterS>0", retryAfterS, locked)
	}

	if wait <= 0 {
		t.Errorf("isBlocked() wait = %v, want > 0", wait)
	}

	// Clear attempts
	app.clearFailures(ip)
	wait, blocked = app.isBlocked(ip)
	if blocked {
		t.Error("isBlocked() = true after clearFailures(), want false")
	}
}
