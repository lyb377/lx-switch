package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/png"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/qr"
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
var (
	totpSecrets   = make(map[string]*TOTPSecret)
	totpSecretsMu sync.Mutex
)

func hashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(strings.ToLower(code))))
	return hex.EncodeToString(sum[:])
}

func generateRecoveryCodes(n int) ([]string, error) {
	if n <= 0 {
		return []string{}, nil
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		b := make([]byte, 10) // 80-bit
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		code := strings.ToLower(strings.TrimRight(base32.StdEncoding.EncodeToString(b), "="))
		out = append(out, code)
	}
	return out, nil
}

func (a *App) replaceRecoveryCodes(userID int64) ([]string, error) {
	codes, err := generateRecoveryCodes(8)
	if err != nil {
		return nil, err
	}
	now := time.Now().Format(time.RFC3339)

	tx, err := a.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM totp_recovery_codes WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}
	for _, c := range codes {
		if _, err := tx.Exec(`INSERT INTO totp_recovery_codes(user_id, code_hash, created_at) VALUES(?, ?, ?)`, userID, hashRecoveryCode(c), now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return codes, nil
}

func (a *App) consumeRecoveryCode(userID int64, code string) (bool, error) {
	code = strings.TrimSpace(strings.ToLower(code))
	if code == "" {
		return false, nil
	}
	now := time.Now().Format(time.RFC3339)
	res, err := a.db.Exec(`UPDATE totp_recovery_codes SET used_at = ? WHERE user_id = ? AND code_hash = ? AND used_at IS NULL`, now, userID, hashRecoveryCode(code))
	if err != nil {
		return false, err
	}
	aff, _ := res.RowsAffected()
	return aff > 0, nil
}

func qrCodeDataURL(text string, size int) (string, error) {
	c, err := qr.Encode(text, qr.M, qr.Auto)
	if err != nil {
		return "", err
	}
	c, err = barcode.Scale(c, size, size)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, c); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// handleEnableTOTP initiates TOTP setup for a user
func (a *App) handleEnableTOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Get current user from session
	userID, err := a.getUserIDFromRequest(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}

	// Get user info
	user, err := a.getUserByID(userID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to get user"})
		return
	}

	// Check if TOTP is already enabled
	if user.TOTPEnabled {
		http.Error(w, "TOTP already enabled", http.StatusBadRequest)
		return
	}

	// Generate new secret
	secret, err := generateTOTPSecret()
	if err != nil {
		http.Error(w, "failed to generate secret", http.StatusInternalServerError)
		return
	}
	secret = strings.ToUpper(strings.TrimSpace(secret))

	// Store secret temporarily
	totpSecretsMu.Lock()
	totpSecrets[secret] = &TOTPSecret{
		UserID:    userID,
		Secret:    secret,
		CreatedAt: time.Now(),
		Confirmed: false,
	}
	totpSecretsMu.Unlock()

	// Generate QR code URL
	issuer := "lx-switch"
	account := user.Username
	qrCodeURL := generateTOTPURI(secret, issuer, account)
	qrDataURL, err := qrCodeDataURL(qrCodeURL, 256)
	if err != nil {
		http.Error(w, "failed to generate QR code", http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"success":    true,
		"secret":     secret,
		"qrCodeUrl":  qrCodeURL,
		"qrCodeData": qrDataURL,
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

	// Require authentication; confirm must match current user
	userID, err := a.getUserIDFromRequest(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
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
	totpSecretsMu.Lock()
	tempSecret, exists := totpSecrets[secret]
	totpSecretsMu.Unlock()
	if !exists {
		http.Error(w, "invalid or expired secret", http.StatusBadRequest)
		return
	}
	if tempSecret.UserID != userID {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
		return
	}

	// Check if secret is expired (5 minutes)
	if time.Since(tempSecret.CreatedAt) > 5*time.Minute {
		totpSecretsMu.Lock()
		delete(totpSecrets, secret)
		totpSecretsMu.Unlock()
		http.Error(w, "secret expired, please try again", http.StatusBadRequest)
		return
	}

	// Verify the code
	if !verifyTOTPCode(secret, code) {
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

	recoveryCodes, err := a.replaceRecoveryCodes(tempSecret.UserID)
	if err != nil {
		http.Error(w, "failed to generate recovery codes", http.StatusInternalServerError)
		return
	}

	// Mark as confirmed and clean up
	totpSecretsMu.Lock()
	delete(totpSecrets, secret)
	totpSecretsMu.Unlock()

	_ = a.insertOpAudit("security.totp.enable", "security", fmt.Sprintf("userId=%d", tempSecret.UserID), clientIP(r), r.UserAgent())

	_ = json.NewEncoder(w).Encode(map[string]any{
		"success":       true,
		"message":       "TOTP enabled successfully",
		"recoveryCodes": recoveryCodes,
	})
}

// handleDisableTOTP disables TOTP for a user
func (a *App) handleDisableTOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Code         string `json:"code"`         // TOTP code
		RecoveryCode string `json:"recoveryCode"` // Recovery code
		Password     string `json:"password"`     // Optional: password confirmation
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Get current user from session
	userID, err := a.getUserIDFromRequest(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}

	// Get current TOTP secret
	user, err := a.getUserByID(userID)
	if err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	if !user.TOTPEnabled {
		http.Error(w, "TOTP not enabled", http.StatusBadRequest)
		return
	}

	// Optional password confirmation.
	if strings.TrimSpace(req.Password) != "" {
		if !verifyPassword(user.PasswordHash, req.Password) {
			http.Error(w, "invalid password", http.StatusUnauthorized)
			return
		}
	}

	// Verify the code (TOTP or recovery code) before disabling.
	ok := false
	if strings.TrimSpace(req.Code) != "" {
		ok = verifyTOTPCode(user.TotpSecret, strings.TrimSpace(req.Code))
	}
	if !ok && strings.TrimSpace(req.RecoveryCode) != "" {
		used, err := a.consumeRecoveryCode(userID, req.RecoveryCode)
		if err != nil {
			http.Error(w, "failed to verify recovery code", http.StatusInternalServerError)
			return
		}
		ok = used
	}
	if !ok {
		http.Error(w, "invalid code", http.StatusBadRequest)
		return
	}

	// Disable TOTP
	result, err := a.db.Exec(`
		UPDATE users SET totp_enabled = 0, totp_secret = '', updated_at = ? WHERE id = ?`,
		time.Now().Format(time.RFC3339), userID,
	)
	if err != nil {
		http.Error(w, "failed to disable TOTP", http.StatusInternalServerError)
		return
	}
	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	// Delete recovery codes
	if _, err := a.db.Exec(`DELETE FROM totp_recovery_codes WHERE user_id = ?`, userID); err != nil {
		log.Printf("warning: failed to delete recovery codes: %v", err)
	}

	// Clear all user sessions (force re-login after disabling 2FA for security)
	if err := a.clearUserSessions(userID); err != nil {
		log.Printf("warning: failed to clear user sessions after TOTP disable: %v", err)
	}

	_ = a.insertOpAudit("security.totp.disable", "security", fmt.Sprintf("userId=%d", userID), clientIP(r), r.UserAgent())

	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
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

// verifyTOTPCode verifies a TOTP code against a secret
func verifyTOTPCode(secret, code string) bool {
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

	// Check current and adjacent time steps (allow 1 step drift for clock skew)
	for i := -1; i <= 1; i++ {
		expectedCode := generateTOTPCode(key, now+int64(i))
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(code)), []byte(expectedCode)) == 1 {
			return true
		}
	}

	return false
}

// verifyTOTP is an alias for verifyTOTPCode (for backward compatibility)
// This is a package-level function, not a method
func verifyTOTP(secret, code string) bool {
	return verifyTOTPCode(secret, code)
}

// VerifyTOTP is the method version for App
func (a *App) VerifyTOTP(secret, code string) bool {
	return verifyTOTPCode(secret, code)
}

// generateTOTPCode generates a TOTP code for a given time step
func generateTOTPCode(key []byte, timeStep int64) string {
	// Convert time step to big-endian bytes
	timeBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(timeBytes, uint64(timeStep))

	// HMAC-SHA1
	h := hmac.New(sha1.New, key)
	h.Write(timeBytes)
	hash := h.Sum(nil)

	// Dynamic truncation
	offset := hash[len(hash)-1] & 0x0f
	truncated := binary.BigEndian.Uint32(hash[offset : offset+4])
	truncated &= 0x7fffffff

	// Generate 6-digit code
	code := truncated % 1000000

	return fmt.Sprintf("%06d", code)
}

// generateTOTP generates a TOTP code for tests/diagnostics using a specific time-step.
func generateTOTP(secret string, timeStep int64) string {
	secret = strings.ToUpper(strings.TrimSpace(secret))
	if l := len(secret) % 8; l != 0 {
		secret += strings.Repeat("=", 8-l)
	}
	key, err := base32.StdEncoding.DecodeString(secret)
	if err != nil {
		return ""
	}
	return generateTOTPCode(key, timeStep)
}

// generateTOTPURI generates a TOTP URI for QR code
func generateTOTPURI(secret, issuer, accountName string) string {
	return fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s",
		issuer, accountName, secret, issuer)
}
