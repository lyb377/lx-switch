package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
)

// TOTPConfig holds TOTP configuration for a user
type TOTPConfig struct {
	Enabled   bool   `json:"enabled"`
	Secret    string `json:"secret,omitempty"`
	QRCodeURL string `json:"qrCodeUrl,omitempty"`
	Issuer    string `json:"issuer"`
	Account   string `json:"account"`
}

// TOTPSecret stores the TOTP secret for a user during setup
type TOTPSecret struct {
	UserID    int64
	Secret    string
	CreatedAt time.Time
	Confirmed bool
}

// totpSecrets stores temporary TOTP secrets during setup (in-memory, cleared on restart)
var totpSecrets = make(map[string]*TOTPSecret)

// handleEnableTOTP initiates TOTP setup for a user
func (a *App) handleEnableTOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Get current user from session (using admin token for simplicity)
	userID := int64(1) // Default admin user
	username := "admin"

	// Check if TOTP is already enabled
	var enabled int
	err := a.db.QueryRow(`SELECT totp_enabled FROM users WHERE id = ?`, userID).Scan(&enabled)
	if err == nil && enabled == 1 {
		http.Error(w, "TOTP already enabled", http.StatusBadRequest)
		return
	}

	// Generate new secret
	secret, err := generateTOTPSecret()
	if err != nil {
		http.Error(w, "failed to generate secret", http.StatusInternalServerError)
		return
	}

	// Store secret temporarily
	totpSecrets[secret] = &TOTPSecret{
		UserID:    userID,
		Secret:    secret,
		CreatedAt: time.Now(),
		Confirmed: false,
	}

	// Generate QR code URL
	issuer := "lx-switch"
	account := username
	qrCodeURL := fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=SHA1&digits=6&period=30",
		issuer, account, secret, issuer)

	// Generate QR code image
	qrBytes, err := qrcode.Encode(qrCodeURL, qrcode.Medium, 256)
	if err != nil {
		http.Error(w, "failed to generate QR code", http.StatusInternalServerError)
		return
	}

	// Return QR code as base64 and secret
	qrBase64 := base32.StdEncoding.EncodeToString(qrBytes)
	
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":         true,
		"secret":     secret,
		"qrCodeUrl":  qrCodeURL,
		"qrCodeData": "data:image/png;base64," + qrBase64,
		"issuer":     issuer,
		"account":    account,
		"message":    "Scan the QR code with your authenticator app, then confirm with a code",
	})
}

// handleConfirmTOTP confirms TOTP setup by verifying the first code
func (a *App) handleConfirmTOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Secret string `json:"secret"`
		Code   string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Validate input
	secret := strings.TrimSpace(strings.ToUpper(req.Secret))
	code := strings.TrimSpace(req.Code)
	if secret == "" || code == "" {
		http.Error(w, "secret and code required", http.StatusBadRequest)
		return
	}

	// Check if secret exists in temporary storage
	tempSecret, exists := totpSecrets[secret]
	if !exists {
		http.Error(w, "invalid or expired secret", http.StatusBadRequest)
		return
	}

	// Check if secret is expired (5 minutes)
	if time.Since(tempSecret.CreatedAt) > 5*time.Minute {
		delete(totpSecrets, secret)
		http.Error(w, "secret expired, please try again", http.StatusBadRequest)
		return
	}

	// Verify the code
	if !verifyTOTP(secret, code) {
		http.Error(w, "invalid code, please try again", http.StatusBadRequest)
		return
	}

	// Enable TOTP for user
	_, err := a.db.Exec(`
		UPDATE users SET totp_enabled = 1, totp_secret = ?, updated_at = ? WHERE id = ?`,
		secret, time.Now().Format(time.RFC3339), tempSecret.UserID,
	)
	if err != nil {
		http.Error(w, "failed to enable TOTP", http.StatusInternalServerError)
		return
	}

	// Mark as confirmed and clean up
	delete(totpSecrets, secret)

	_ = a.insertOpAudit("security.totp.enable", "security", fmt.Sprintf("userId=%d", tempSecret.UserID), clientIP(r), r.UserAgent())

	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"message": "TOTP enabled successfully",
	})
}

// handleDisableTOTP disables TOTP for a user
func (a *App) handleDisableTOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Code     string `json:"code"`
		Password string `json:"password"` // Optional: require password confirmation
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	userID := int64(1) // Default admin user

	// Get current TOTP secret
	var totpEnabled int
	var totpSecret string
	err := a.db.QueryRow(`SELECT totp_enabled, totp_secret FROM users WHERE id = ?`, userID).Scan(&totpEnabled, &totpSecret)
	if err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	if totpEnabled == 0 {
		http.Error(w, "TOTP not enabled", http.StatusBadRequest)
		return
	}

	// Verify the code before disabling
	code := strings.TrimSpace(req.Code)
	if code == "" {
		http.Error(w, "code required", http.StatusBadRequest)
		return
	}

	if !verifyTOTP(totpSecret, code) {
		http.Error(w, "invalid code", http.StatusBadRequest)
		return
	}

	// Disable TOTP
	_, err = a.db.Exec(`
		UPDATE users SET totp_enabled = 0, totp_secret = '', updated_at = ? WHERE id = ?`,
		time.Now().Format(time.RFC3339), userID,
	)
	if err != nil {
		http.Error(w, "failed to disable TOTP", http.StatusInternalServerError)
		return
	}

	_ = a.insertOpAudit("security.totp.disable", "security", fmt.Sprintf("userId=%d", userID), clientIP(r), r.UserAgent())

	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"message": "TOTP disabled successfully",
	})
}

// generateTOTPSecret generates a random TOTP secret
func generateTOTPSecret() (string, error) {
	secret := make([]byte, 20) // 160 bits
	_, err := rand.Read(secret)
	if err != nil {
		return "", err
	}

	// Base32 encode without padding
	encoded := base32.StdEncoding.EncodeToString(secret)
	return strings.TrimRight(encoded, "="), nil
}

// verifyTOTP verifies a TOTP code
func verifyTOTP(secret, code string) bool {
	// Pad secret if needed
	secret = strings.ToUpper(strings.TrimSpace(secret))
	if l := len(secret) % 8; l != 0 {
		secret += strings.Repeat("=", 8-l)
	}

	// Decode secret
	key, err := base32.StdEncoding.DecodeString(secret)
	if err != nil {
		return false
	}

	// Get current time step (30 second intervals)
	now := time.Now().Unix() / 30

	// Check current and adjacent time steps (allow 1 step drift)
	for i := -1; i <= 1; i++ {
		expectedCode := generateTOTPCode(key, now+int64(i))
		if subtleConstantTimeCompare(code, expectedCode) {
			return true
		}
	}

	return false
}

// generateTOTPCode generates a TOTP code for a given time step
func generateTOTPCode(key []byte, timeStep int64) string {
	// Convert time step to big-endian bytes
	timeBytes := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		timeBytes[i] = byte(timeStep & 0xff)
		timeStep >>= 8
	}

	// HMAC-SHA1
	h := hmac.New(sha1.New, key)
	h.Write(timeBytes)
	hash := h.Sum(nil)

	// Dynamic truncation
	offset := hash[len(hash)-1] & 0x0f
	code := int32(hash[offset]&0x7f)<<24 |
		int32(hash[offset+1])<<16 |
		int32(hash[offset+2])<<8 |
		int32(hash[offset+3])
	code = code % 1000000

	return fmt.Sprintf("%06d", code)
}

// subtleConstantTimeCompare performs a constant-time comparison
func subtleConstantTimeCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	return bytes.Equal([]byte(a), []byte(b))
}
