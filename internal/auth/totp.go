package auth

// TOTP (RFC 6238) authenticator-app 2FA, dependency-free — consistent with the
// hand-rolled PBKDF2 in store.go. Secrets are stored AES-GCM-encrypted at rest;
// backup codes are stored SHA-256 hashed and consumed single-use.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	totpDigits = 6
	totpPeriod = 30 // seconds
	totpSkew   = 1  // accept ±1 step (±30s) for clock drift
	totpIssuer = "Sixter"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

/* ─────────── secret generation ─────────── */

// newTOTPSecret returns a fresh base32 secret (160-bit, the RFC-recommended size).
func newTOTPSecret() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b32.EncodeToString(b)
}

// otpauthURI builds the otpauth:// URI an authenticator app scans.
func otpauthURI(account, secretB32 string) string {
	label := url.PathEscape(totpIssuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secretB32)
	q.Set("issuer", totpIssuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprintf("%d", totpDigits))
	q.Set("period", fmt.Sprintf("%d", totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

/* ─────────── code computation + validation ─────────── */

// hotp computes the HOTP value for a counter (RFC 4226).
func hotp(key []byte, counter uint64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	val := (uint32(sum[off]&0x7f) << 24) |
		(uint32(sum[off+1]) << 16) |
		(uint32(sum[off+2]) << 8) |
		uint32(sum[off+3])
	mod := uint32(1)
	for i := 0; i < totpDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, val%mod)
}

// validateTOTP reports whether code is valid for the secret at the current time,
// tolerating ±totpSkew steps. Comparison is constant-time.
func validateTOTP(secretB32, code string, now time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return false
	}
	key, err := b32.DecodeString(strings.ToUpper(strings.ReplaceAll(secretB32, " ", "")))
	if err != nil {
		return false
	}
	step := uint64(now.Unix() / totpPeriod)
	for d := -totpSkew; d <= totpSkew; d++ {
		want := hotp(key, step+uint64(d))
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

/* ─────────── backup codes ─────────── */

// newBackupCodes returns n human-friendly one-time codes (plaintext, shown once)
// and their SHA-256 hashes (what we persist).
func newBackupCodes(n int) (plain []string, hashes []string) {
	for i := 0; i < n; i++ {
		raw := make([]byte, 10) // 10 bytes -> 16 base32 chars (>= the 10 we use)
		if _, err := rand.Read(raw); err != nil {
			panic(err)
		}
		// 10-char base32, formatted XXXXX-XXXXX for readability
		s := b32.EncodeToString(raw)
		code := strings.ToLower(s[:5] + "-" + s[5:10])
		plain = append(plain, code)
		hashes = append(hashes, sha256Hex(normalizeBackup(code)))
	}
	return
}

func normalizeBackup(code string) string {
	return strings.ToLower(strings.TrimSpace(strings.ReplaceAll(code, "-", "")))
}

/* ─────────── AES-GCM encryption of the secret at rest ─────────── */

// loadOrCreateKey returns a 32-byte AES key from path, creating it (0600) if missing.
func loadOrCreateKey(path string) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil {
		key, derr := hex.DecodeString(strings.TrimSpace(string(b)))
		if derr == nil && len(key) == 32 {
			return key, nil
		}
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)), 0600); err != nil {
		return nil, err
	}
	return key, nil
}

func aesEncrypt(key []byte, plaintext string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ct), nil
}

func aesDecrypt(key []byte, hexCT string) (string, error) {
	ct, err := hex.DecodeString(hexCT)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(ct) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, body := ct[:gcm.NonceSize()], ct[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}
