package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

// verifyTOTP verifies a TOTP code against a secret
func (a *App) verifyTOTP(secret, code string) bool {
	if secret == "" || code == "" {
		return false
	}

	// Try current time window and ±1 window for clock skew
	now := time.Now().Unix() / 30
	for offset := int64(-1); offset <= 1; offset++ {
		if generateTOTP(secret, now+offset) == code {
			return true
		}
	}
	return false
}

// generateTOTP generates a TOTP code for a given secret and time
func generateTOTP(secret string, timeStep int64) string {
	// Decode base32 secret
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return ""
	}

	// Convert time step to bytes
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(timeStep))

	// HMAC-SHA1
	h := hmac.New(sha1.New, key)
	h.Write(buf)
	hash := h.Sum(nil)

	// Dynamic truncation
	offset := hash[len(hash)-1] & 0x0f
	truncated := binary.BigEndian.Uint32(hash[offset:offset+4]) & 0x7fffffff

	// Generate 6-digit code
	code := truncated % uint32(math.Pow10(6))
	return fmt.Sprintf("%06d", code)
}

// generateTOTPURI generates a TOTP URI for QR code
func generateTOTPURI(secret, issuer, accountName string) string {
	return fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s",
		issuer, accountName, secret, issuer)
}

// handleEnableTOTP enables 2FA for a user
func (a *App) handleEnableTOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	userID, err := a.getUserIDFromRequest(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Generate TOTP secret
	secret, err := generateTOTPSecret()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Store secret (not enabled yet)
	now := time.Now().Format(time.RFC3339)
	_, err = a.db.Exec(`
		UPDATE users SET totp_secret = ?, updated_at = ?
		WHERE id = ?`, secret, now, userID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Get user info for URI
	user, err := a.getUserByID(userID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Generate URI for QR code
	uri := generateTOTPURI(secret, "lx-switch", user.Username)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"secret": secret,
		"uri":    uri,
	})
}

// handleConfirmTOTP confirms and enables 2FA
func (a *App) handleConfirmTOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	userID, err := a.getUserIDFromRequest(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Get user's TOTP secret
	user, err := a.getUserByID(userID)
	if err != nil || user.TotpSecret == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "2FA not initialized"})
		return
	}

	// Verify code
	if !a.verifyTOTP(user.TotpSecret, req.Code) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid code"})
		return
	}

	// Enable TOTP
	now := time.Now().Format(time.RFC3339)
	_, err = a.db.Exec(`
		UPDATE users SET totp_enabled = 1, updated_at = ?
		WHERE id = ?`, now, userID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// handleDisableTOTP disables 2FA for a user
func (a *App) handleDisableTOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	userID, err := a.getUserIDFromRequest(r)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Verify password before disabling
	user, err := a.getUserByID(userID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if !verifyPassword(user.PasswordHash, req.Password) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid password"})
		return
	}

	// Disable TOTP
	now := time.Now().Format(time.RFC3339)
	_, err = a.db.Exec(`
		UPDATE users SET totp_enabled = 0, totp_secret = '', updated_at = ?
		WHERE id = ?`, now, userID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
