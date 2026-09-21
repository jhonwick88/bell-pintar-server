package license

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"bell_server/internal/database"

	"github.com/denisbrodbeck/machineid"
)

const LicenseFileName = "license.token"

var DefaultActivateURL = "http://203.88.125.140:8080/api/v1/license/activate"

type LicenseClaims struct {
	LicenseID          string                 `json:"license_id"`
	ProductID          string                 `json:"product_id"`
	CustomerID         string                 `json:"customer_id"`
	PlanID             string                 `json:"plan_id"`
	InstallationID     string                 `json:"installation_id"`
	MachineFingerprint string                 `json:"machine_fingerprint"`
	Features           map[string]interface{} `json:"features"`
	ExpiresAt          int64                  `json:"expires_at,omitempty"`
	IssuedAt           int64                  `json:"issued_at,omitempty"`
	Iat                int64                  `json:"iat,omitempty"`
}

// GetMachineFingerprint generates hardware-bound HWID identical to PintarLabs ecosystem
func GetMachineFingerprint() (string, error) {
	id, err := machineid.ProtectedID("BellPintar")
	if err != nil {
		hostname, hErr := os.Hostname()
		if hErr != nil {
			return "BELLPINTAR-DEFAULT-HWID", nil
		}
		return "HWID-" + hostname, nil
	}
	return id, nil
}

// GetLicenseClaims decodes license.token payload (matching rest_go_toko implementation)
func GetLicenseClaims() (*LicenseClaims, error) {
	data, err := os.ReadFile(LicenseFileName)
	if err != nil {
		return nil, errors.New("file license.token tidak ditemukan")
	}

	tokenStr := strings.TrimSpace(string(data))
	if tokenStr == "" {
		return nil, errors.New("file license.token kosong")
	}

	parts := strings.Split(tokenStr, ".")
	if len(parts) != 2 && len(parts) != 3 {
		return nil, errors.New("format token lisensi tidak valid")
	}

	payloadIdx := 0
	if len(parts) == 3 {
		payloadIdx = 1 // standard JWT header.payload.sig
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[payloadIdx])
	if err != nil {
		return nil, fmt.Errorf("gagal decode payload lisensi: %v", err)
	}

	var claims LicenseClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("gagal parsing claims lisensi: %v", err)
	}

	return &claims, nil
}

// GetExpirationTime calculates expiration timestamp, formatted string, days remaining, and validity
func GetExpirationTime(claims *LicenseClaims) (int64, string, int64, bool) {
	now := time.Now()
	nowUnix := now.Unix()

	// 1. Direct standard expires_at claim
	if claims.ExpiresAt > 0 {
		expTime := time.Unix(claims.ExpiresAt, 0)
		diffSec := claims.ExpiresAt - nowUnix
		if diffSec > 0 {
			days := diffSec / 86400
			if days == 0 && diffSec > 0 {
				days = 1
			}
			return claims.ExpiresAt, expTime.Format("2006-01-02 15:04:05"), days, true
		}
		return claims.ExpiresAt, expTime.Format("2006-01-02 15:04:05"), 0, false
	}

	// 2. Derive from duration_days in claims.Features
	durationDays := 0
	if val, ok := claims.Features["duration_days"]; ok {
		if n, ok := val.(float64); ok && n > 0 {
			durationDays = int(n)
		}
	}

	if durationDays > 0 {
		startTime := now
		if claims.IssuedAt > 0 {
			startTime = time.Unix(claims.IssuedAt, 0)
		} else if claims.Iat > 0 {
			startTime = time.Unix(claims.Iat, 0)
		} else if database.DB != nil {
			var lastSyncedStr string
			err := database.DB.QueryRow("SELECT last_synced_at FROM license_data WHERE id = 1").Scan(&lastSyncedStr)
			if err == nil && lastSyncedStr != "" {
				if t, parseErr := time.Parse("2006-01-02 15:04:05", lastSyncedStr); parseErr == nil {
					startTime = t
				}
			}
		}

		expTime := startTime.Add(time.Duration(durationDays) * 24 * time.Hour)
		expUnix := expTime.Unix()
		diffSec := expUnix - nowUnix

		if diffSec > 0 {
			days := diffSec / 86400
			if days == 0 && diffSec > 0 {
				days = 1
			}
			return expUnix, expTime.Format("2006-01-02 15:04:05"), days, true
		}
		return expUnix, expTime.Format("2006-01-02 15:04:05"), 0, false
	}

	// 3. Fallback: Lifetime
	return 0, "Lifetime (Tidak Terbatas)", 0, true
}

// HasValidLicense checks if license exists, matches HWID, and has NOT expired
func HasValidLicense() bool {
	claims, err := GetLicenseClaims()
	if err != nil || claims == nil {
		return false
	}

	hwid, err := GetMachineFingerprint()
	if err != nil || claims.MachineFingerprint != hwid {
		log.Printf("[LICENSE WARNING] Hardware ID mismatch! Machine: %s, License: %s", hwid, claims.MachineFingerprint)
		return false
	}

	_, expStr, _, isNotExpired := GetExpirationTime(claims)
	if !isNotExpired {
		log.Printf("[LICENSE WARNING] Lisensi telah kadaluarsa pada %s", expStr)
		return false
	}

	return true
}

// GetMaxDevices returns allowed max devices (reading feat_max_devices or fallback max_devices)
func GetMaxDevices() int {
	claims, err := GetLicenseClaims()
	if err != nil || claims == nil {
		return 1
	}

	if val, ok := claims.Features["feat_max_devices"]; ok {
		if n, ok := val.(float64); ok && n > 0 {
			return int(n)
		}
	}
	if val, ok := claims.Features["max_devices"]; ok {
		if n, ok := val.(float64); ok && n > 0 {
			return int(n)
		}
	}
	return 1
}

// IsFeatureEnabled checks if a specific feature code is active
func IsFeatureEnabled(featureName string) bool {
	claims, err := GetLicenseClaims()
	if err != nil || claims == nil {
		return false
	}

	key := strings.ToLower(featureName)
	val, exists := claims.Features[key]
	if !exists {
		// Aliasing feat_max_devices <-> max_devices
		if key == "max_devices" {
			val, exists = claims.Features["feat_max_devices"]
		} else if key == "feat_max_devices" {
			val, exists = claims.Features["max_devices"]
		}
		if !exists {
			return false
		}
	}

	if b, ok := val.(bool); ok {
		return b
	}
	if s, ok := val.(string); ok {
		return s == "1" || strings.ToLower(s) == "true"
	}
	if n, ok := val.(float64); ok {
		return n > 0
	}

	return false
}

// ActivateLicense requests activation to PintarLabs License Server
func ActivateLicense(licenseKey string, serverURL string) (*LicenseClaims, error) {
	fingerprint, err := GetMachineFingerprint()
	if err != nil {
		return nil, err
	}

	if serverURL == "" {
		dbURL := database.GetSetting("license_server_url", "")
		if dbURL != "" {
			serverURL = dbURL
		} else {
			serverURL = DefaultActivateURL
		}
	}

	hostname, _ := os.Hostname()
	payload := map[string]string{
		"license_key":         licenseKey,
		"machine_fingerprint": fingerprint,
		"app_version":         "1.0.0",
		"hostname":            hostname,
		"platform":            "windows-bell-server",
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	log.Printf("[LICENSE] Mengirim aktivasi ke server: %s (Key: %s, HWID: %s)", serverURL, licenseKey, fingerprint)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(serverURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("gagal terhubung ke server lisensi PintarLabs: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca respon server: %v", err)
	}

	var result struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Data    struct {
			Token          string `json:"token"`
			InstallationID string `json:"installation_id"`
			Status         string `json:"status"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("format respon lisensi tidak valid: %s", string(body))
	}

	if resp.StatusCode != http.StatusOK || result.Data.Token == "" {
		msg := result.Message
		if msg == "" {
			msg = "Aktivasi lisensi ditolak oleh server"
		}
		return nil, errors.New(msg)
	}

	// Save token to license.token
	if err := os.WriteFile(LicenseFileName, []byte(result.Data.Token), 0644); err != nil {
		return nil, fmt.Errorf("gagal menyimpan file lisensi: %v", err)
	}

	claims, err := GetLicenseClaims()
	if err != nil {
		return nil, err
	}

	// Synchronize to SQLite bell.db
	if database.DB != nil {
		_, expStr, _, _ := GetExpirationTime(claims)

		edition := "BASIC"
		if IsFeatureEnabled("feat_usb_relay") {
			edition = "PRO"
		}

		_, _ = database.DB.Exec(`
			INSERT OR REPLACE INTO license_data 
			(id, license_key, hardware_id, product_code, edition, expires_at, signature, is_valid, last_synced_at)
			VALUES (1, ?, ?, 'BELL_PINTAR', ?, ?, ?, 1, CURRENT_TIMESTAMP)
		`, licenseKey, fingerprint, edition, expStr, result.Data.Token)

		_ = database.SetSetting("edition", edition)
	}

	log.Printf("[LICENSE SUCCESS] Lisensi berhasil diaktivasi! Hardware: %s", fingerprint)
	return claims, nil
}

// GetLicenseStatus returns current license state for UI & API
func GetLicenseStatus() map[string]interface{} {
	hwid, _ := GetMachineFingerprint()
	claims, err := GetLicenseClaims()

	if err != nil || claims == nil {
		return map[string]interface{}{
			"is_valid":         false,
			"status":           "UNLICENSED",
			"message":          "Aplikasi belum teraktivasi lisensi",
			"hardware_id":      hwid,
			"days_remaining":   0,
			"feat_max_devices": 1,
			"features":         map[string]interface{}{},
		}
	}

	isValid := HasValidLicense()
	_, expiresAtStr, daysRemaining, isNotExpired := GetExpirationTime(claims)
	if !isNotExpired {
		isValid = false
	}

	edition := "BASIC"
	if IsFeatureEnabled("feat_usb_relay") {
		edition = "PRO"
	}

	maxDevs := GetMaxDevices()

	return map[string]interface{}{
		"is_valid":           isValid,
		"status":             map[bool]string{true: "ACTIVE", false: "EXPIRED"}[isValid],
		"edition":            edition,
		"license_id":         claims.LicenseID,
		"hardware_id":        hwid,
		"expires_at":         expiresAtStr,
		"days_remaining":     daysRemaining,
		"feat_max_devices":   maxDevs,
		"max_devices":        maxDevs, // alias untuk backward compatibility
		"feat_scheduler":     IsFeatureEnabled("feat_scheduler"),
		"feat_presets":       IsFeatureEnabled("feat_presets"),
		"feat_usb_relay":     IsFeatureEnabled("feat_usb_relay"),
		"feat_tts":           IsFeatureEnabled("feat_tts"),
		"feat_remote_mobile": IsFeatureEnabled("feat_remote_mobile"),
		"feat_custom_audio":  IsFeatureEnabled("feat_custom_audio"),
		"features":           claims.Features,
	}
}
