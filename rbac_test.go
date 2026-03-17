package main

import (
	"net"
	"net/http"
	"testing"
	"time"
)

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
	app.recordFailedAttempt(ip)
	wait, blocked := app.isBlocked(ip)
	if blocked {
		t.Error("isBlocked() = true after 1 attempt, want false")
	}

	// Second attempt
	app.recordFailedAttempt(ip)
	wait, blocked = app.isBlocked(ip)
	if blocked {
		t.Error("isBlocked() = true after 2 attempts, want false")
	}

	// Third attempt (should trigger lockout)
	app.recordFailedAttempt(ip)
	wait, blocked = app.isBlocked(ip)
	if !blocked {
		t.Error("isBlocked() = false after 3 attempts, want true")
	}

	if wait <= 0 {
		t.Errorf("isBlocked() wait = %v, want > 0", wait)
	}

	// Clear attempts
	app.clearFailedAttempts(ip)
	wait, blocked = app.isBlocked(ip)
	if blocked {
		t.Error("isBlocked() = true after clearFailedAttempts(), want false")
	}
}
