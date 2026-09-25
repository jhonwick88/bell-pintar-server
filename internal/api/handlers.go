package api

import (
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"bell_server/internal/audio"
	"bell_server/internal/auth"
	"bell_server/internal/database"
	"bell_server/internal/license"
	"bell_server/internal/relay"
	"bell_server/internal/scheduler"
	"bell_server/internal/tts"

	"github.com/gin-gonic/gin"
)

// SetupRoutes registers all API endpoints
func SetupRoutes(r *gin.Engine) {
	api := r.Group("/api/v1")

	// Public Routes
	api.GET("/ping", handlePing)
	api.POST("/auth/login-pin", handleLoginPIN)
	api.POST("/auth/logout", handleLogout)
	api.GET("/dashboard/status", handleDashboardStatus)
	api.GET("/license/status", handleGetLicenseStatus)
	api.POST("/license/activate", handleActivateLicense)
	api.GET("/server/network", handleGetServerNetwork)

	// Protected Routes (Requires valid JWT)
	protected := api.Group("/")
	protected.Use(auth.JWTMiddleware())
	{
		// Dashboard & Live Control
		protected.POST("/bell/trigger", handleTriggerManualBell)
		protected.POST("/tts/speak", handleTTSSpeak)
		protected.GET("/presets", handleGetPresets)
		protected.POST("/presets/active", handleSetActivePreset)
		protected.POST("/presets", handleCreatePreset)
		protected.DELETE("/presets/:id", handleDeletePreset)

		// Schedules Management
		protected.GET("/schedules", handleGetSchedules)
		protected.POST("/schedules", handleCreateSchedule)
		protected.PUT("/schedules/:id", handleUpdateSchedule)
		protected.DELETE("/schedules/clear-day", handleClearDaySchedules)
		protected.DELETE("/schedules/:id", handleDeleteSchedule)

		// Audio Library & Announcements (Custom Audio CRUD)
		protected.GET("/audio", handleGetAudioList)
		protected.POST("/audio/test", handleTestAudio)
		protected.POST("/audio/upload", handleUploadAudio)
		protected.PUT("/audio/:id", handleUpdateAudio)
		protected.DELETE("/audio/:id", handleDeleteAudio)
		protected.GET("/announcements", handleGetAnnouncements)
		protected.POST("/announcements", handleCreateAnnouncement)
		protected.PUT("/announcements/:id", handleUpdateAnnouncement)
		protected.DELETE("/announcements/:id", handleDeleteAnnouncement)
		protected.POST("/announcements/trigger/:id", handleTriggerAnnouncement)

		// Device Sessions (feat_max_devices)
		protected.GET("/sessions", handleGetSessions)
		protected.DELETE("/sessions/:id", handleRevokeSession)

		// Logs
		protected.GET("/logs", handleGetLogs)

		// Admin-Only Routes
		adminOnly := protected.Group("/")
		adminOnly.Use(auth.RequireRole("ADMIN"))
		{
			adminOnly.GET("/settings", handleGetSettings)
			adminOnly.POST("/settings", handleSaveSettings)
			adminOnly.POST("/relay/test", handleTestRelay)
		}
	}
}

func handlePing(c *gin.Context) {
	lic := license.GetLicenseStatus()
	c.JSON(http.StatusOK, gin.H{
		"status":         "online",
		"system_time":    time.Now().Format("2006-01-02 15:04:05"),
		"school_name":    database.GetSetting("school_name", "SMA Negeri 1 Pintar"),
		"edition":        lic["edition"],
		"license_status": lic["status"],
		"hardware_id":    lic["hardware_id"],
		"days_remaining": lic["days_remaining"],
		"relay_on":       relay.Instance.IsOn(),
	})
}

func handleGetLicenseStatus(c *gin.Context) {
	c.JSON(http.StatusOK, license.GetLicenseStatus())
}

func handleLoginPIN(c *gin.Context) {
	var req struct {
		PIN        string `json:"pin" binding:"required"`
		DeviceID   string `json:"device_id"`
		DeviceName string `json:"device_name"`
		Platform   string `json:"platform"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "PIN is required"})
		return
	}

	platform := strings.ToLower(req.Platform)
	if platform == "" {
		platform = strings.ToLower(c.GetHeader("X-Device-Platform"))
	}
	if platform == "" {
		platform = "windows"
	}

	// 1. Check Mobile Remote License (feat_remote_mobile)
	if platform == "android" || platform == "ios" || platform == "mobile" {
		if !license.IsFeatureEnabled("feat_remote_mobile") {
			c.JSON(http.StatusForbidden, gin.H{
				"error": "Akses Remote HP tidak aktif pada lisensi sekolah ini (feat_remote_mobile). Hubungi penyedia untuk aktivasi lisensi.",
				"error_code": "FEATURE_REMOTE_MOBILE_DISABLED",
			})
			return
		}
	}

	var userID int64
	var name, role string
	var trigger, preset, canTTS, isActive int

	err := database.DB.QueryRow(`
		SELECT id, name, role, can_trigger_manual, can_change_preset, can_send_tts, is_active
		FROM users_pins
		WHERE pin_hash = ? AND is_active = 1
	`, req.PIN).Scan(&userID, &name, &role, &trigger, &preset, &canTTS, &isActive)

	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "PIN tidak valid atau pengguna nonaktif"})
		return
	}

	// 2. Enforce Max Devices (feat_max_devices)
	clientIP := c.ClientIP()
	isLocalhost := clientIP == "127.0.0.1" || clientIP == "::1"

	deviceID := req.DeviceID
	if deviceID == "" || deviceID == "unknown-device" {
		deviceID = c.GetHeader("X-Device-Id")
	}
	if deviceID == "" || deviceID == "unknown-device" {
		deviceID = fmt.Sprintf("dev_%s_%s", platform, clientIP)
	}

	deviceName := req.DeviceName
	if deviceName == "" {
		deviceName = c.GetHeader("X-Device-Name")
	}
	if deviceName == "" {
		deviceName = fmt.Sprintf("Perangkat (%s - %s)", strings.ToUpper(platform), clientIP)
	}

	maxAllowed := license.GetMaxDevices()
	if maxAllowed <= 0 {
		maxAllowed = 1
	}

	// Clean up stale sessions (> 30 days)
	_, _ = database.DB.Exec("DELETE FROM active_sessions WHERE last_active < datetime('now', '-30 days')")

	// If logging in from localhost, clean up any previous localhost sessions so it doesn't accumulate
	if isLocalhost {
		_, _ = database.DB.Exec("DELETE FROM active_sessions WHERE ip_address = '127.0.0.1' OR ip_address = '::1' OR device_id = '127.0.0.1' OR device_id = '::1'")
	}

	var existingSessionID int64
	// Match by device_id OR (ip_address and platform) to prevent duplicate counting on same device
	errCheck := database.DB.QueryRow("SELECT id FROM active_sessions WHERE device_id = ? OR (ip_address = ? AND platform = ?)", deviceID, clientIP, platform).Scan(&existingSessionID)

	if errCheck == sql.ErrNoRows {
		// New device attempting to login
		// Localhost console is never rejected by mobile remote limit
		if !isLocalhost {
			var activeCount int
			_ = database.DB.QueryRow("SELECT COUNT(*) FROM active_sessions WHERE ip_address != '127.0.0.1' AND ip_address != '::1'").Scan(&activeCount)
			if activeCount >= maxAllowed {
				c.JSON(http.StatusForbidden, gin.H{
					"error": fmt.Sprintf("Batas kuota perangkat lisensi tercapai (Maksimal %d perangkat). Harap cabut atau logout dari perangkat lain terlebih dahulu.", maxAllowed),
					"error_code": "MAX_DEVICES_EXCEEDED",
					"max_devices": maxAllowed,
					"active_count": activeCount,
				})
				return
			}
		}

		_, _ = database.DB.Exec(`
			INSERT INTO active_sessions (user_id, device_id, device_name, platform, ip_address, last_active)
			VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		`, userID, deviceID, deviceName, platform, clientIP)
	} else {
		// Update existing session
		_, _ = database.DB.Exec(`
			UPDATE active_sessions 
			SET user_id = ?, device_id = ?, device_name = ?, platform = ?, ip_address = ?, last_active = CURRENT_TIMESTAMP
			WHERE id = ?
		`, userID, deviceID, deviceName, platform, clientIP, existingSessionID)
	}

	token, err := auth.GenerateToken(
		userID, name, role,
		trigger == 1, preset == 1, canTTS == 1,
		30*24*time.Hour, // 30 days valid for mobile convenience
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal membuat sesi token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user": gin.H{
			"id":                 userID,
			"name":               name,
			"role":               role,
			"can_trigger_manual": trigger == 1,
			"can_change_preset":  preset == 1,
			"can_send_tts":       canTTS == 1,
		},
	})
}

func handleGetServerNetwork(c *gin.Context) {
	var ips []string
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, i := range ifaces {
			if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, err := i.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				var ip net.IP
				switch v := addr.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}
				if ip == nil || ip.IsLoopback() {
					continue
				}
				ip = ip.To4()
				if ip == nil {
					continue
				}
				ips = append(ips, ip.String())
			}
		}
	}

	port := database.GetSetting("api_port", "8088")
	primaryIP := "localhost"
	if len(ips) > 0 {
		primaryIP = ips[0]
	}

	c.JSON(http.StatusOK, gin.H{
		"ips":                ips,
		"primary_ip":         primaryIP,
		"port":               port,
		"server_url":         fmt.Sprintf("http://%s:%s/api/v1", primaryIP, port),
		"school_name":        database.GetSetting("school_name", "SMA Negeri 1 Pintar"),
		"feat_remote_mobile": license.IsFeatureEnabled("feat_remote_mobile"),
	})
}

func handleDashboardStatus(c *gin.Context) {
	next, err := scheduler.GetNextSchedule()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	activePresetID := database.GetSetting("active_preset_id", "1")
	var activePresetName string
	_ = database.DB.QueryRow("SELECT name FROM schedule_presets WHERE id = ?", activePresetID).Scan(&activePresetName)

	c.JSON(http.StatusOK, gin.H{
		"current_time":        time.Now().Format("15:04:05"),
		"current_date":        time.Now().Format("2006-01-02"),
		"active_preset_id":    activePresetID,
		"active_preset_name":  activePresetName,
		"relay_status":        relay.Instance.IsOn(),
		"master_volume":       database.GetSetting("master_volume", "85"),
		"edition":             database.GetSetting("edition", "PRO"),
		"next_schedule":       next,
	})
}

func handleTriggerManualBell(c *gin.Context) {
	var req struct {
		AudioID  *int64 `json:"audio_id"`
		FilePath string `json:"file_path"`
		Title    string `json:"title" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	claims := c.MustGet("claims").(*auth.Claims)
	if !claims.CanTriggerManual {
		c.JSON(http.StatusForbidden, gin.H{"error": "Anda tidak memiliki izin membunyikan bel"})
		return
	}

	audioPath := req.FilePath
	audioTitle := req.Title

	if req.AudioID != nil && *req.AudioID > 0 {
		_ = database.DB.QueryRow("SELECT file_path, title FROM audio_files WHERE id = ?", *req.AudioID).Scan(&audioPath, &audioTitle)
	}

	go func() {
		_ = audio.GlobalPlayer.PlayFile(audioPath, audio.PlayOptions{
			Title:       audioTitle,
			TriggerType: "MANUAL_MOBILE",
			UserID:      &claims.UserID,
			UserName:    claims.Name,
		})
	}()

	c.JSON(http.StatusOK, gin.H{
		"message": "Bel berhasil dibunyikan",
		"title":   audioTitle,
	})
}

func handleTTSSpeak(c *gin.Context) {
	var req struct {
		Text           string  `json:"text" binding:"required"`
		ChimeAudioPath string  `json:"chime_audio_path"`
		Speed          float64 `json:"speed"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	claims := c.MustGet("claims").(*auth.Claims)
	if !claims.CanSendTTS {
		c.JSON(http.StatusForbidden, gin.H{"error": "Anda tidak memiliki izin Text-to-Speech"})
		return
	}

	go func() {
		_ = tts.GlobalEngine.SpeakText(req.Text, req.ChimeAudioPath, audio.PlayOptions{
			UserID:   &claims.UserID,
			UserName: claims.Name,
		}, req.Speed)
	}()

	c.JSON(http.StatusOK, gin.H{
		"message": "Pengumuman TTS sedang disiarkan ke pengeras suara",
	})
}

func handleGetPresets(c *gin.Context) {
	rows, err := database.DB.Query("SELECT id, name, code, description, is_default FROM schedule_presets ORDER BY id ASC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var presets []gin.H
	activeID := database.GetSetting("active_preset_id", "1")

	for rows.Next() {
		var id int64
		var name, code, desc string
		var isDefault int
		if err := rows.Scan(&id, &name, &code, &desc, &isDefault); err == nil {
			presets = append(presets, gin.H{
				"id":          id,
				"name":        name,
				"code":        code,
				"description": desc,
				"is_default":  isDefault == 1,
				"is_active":   strconv.FormatInt(id, 10) == activeID,
			})
		}
	}
	c.JSON(http.StatusOK, presets)
}

func handleSetActivePreset(c *gin.Context) {
	var req struct {
		PresetID int64 `json:"preset_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	_ = database.SetSetting("active_preset_id", strconv.FormatInt(req.PresetID, 10))
	c.JSON(http.StatusOK, gin.H{"message": "Preset jadwal aktif berhasil diperbarui"})
}

func handleCreatePreset(c *gin.Context) {
	var req struct {
		Name             string `json:"name" binding:"required"`
		Code             string `json:"code"`
		Description      string `json:"description"`
		CopyFromPresetID int64  `json:"copy_from_preset_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Nama preset wajib diisi"})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		log.Printf("[PRESET CREATE] Ditolak: Nama preset kosong")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Nama preset tidak boleh kosong"})
		return
	}

	// Generate code if empty or sanitize
	code := strings.TrimSpace(req.Code)
	if code == "" {
		code = fmt.Sprintf("CUSTOM_%d", time.Now().Unix())
	} else {
		code = strings.ToUpper(strings.ReplaceAll(code, " ", "_"))
	}

	res, err := database.DB.Exec(
		"INSERT INTO schedule_presets (name, code, description, is_default) VALUES (?, ?, ?, 0)",
		req.Name, code, strings.TrimSpace(req.Description),
	)
	if err != nil {
		log.Printf("[PRESET CREATE] Error DB INSERT schedule_presets: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menyimpan preset: " + err.Error()})
		return
	}

	newPresetID, err := res.LastInsertId()
	if err != nil {
		log.Printf("[PRESET CREATE] Error LastInsertId: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal mendapatkan ID preset baru"})
		return
	}

	// If copy_from_preset_id is provided and valid, duplicate all schedules from that preset
	copiedCount := int64(0)
	if req.CopyFromPresetID > 0 {
		copyRes, copyErr := database.DB.Exec(`
			INSERT INTO schedules (preset_id, day_of_week, time_trigger, title, audio_file_id, custom_tts_text, is_active)
			SELECT ?, day_of_week, time_trigger, title, audio_file_id, custom_tts_text, is_active
			FROM schedules
			WHERE preset_id = ?
		`, newPresetID, req.CopyFromPresetID)
		if copyErr != nil {
			log.Printf("[PRESET CREATE] Gagal menyalin jadwal dari preset %d ke %d: %v", req.CopyFromPresetID, newPresetID, copyErr)
		} else {
			copiedCount, _ = copyRes.RowsAffected()
		}
	}

	log.Printf("[PRESET CREATE] Berhasil! ID: %d | Nama: '%s' | Code: '%s' | Template Preset: %d (%d jadwal disalin)",
		newPresetID, req.Name, code, req.CopyFromPresetID, copiedCount)

	c.JSON(http.StatusCreated, gin.H{
		"message": "Preset jadwal baru berhasil dibuat",
		"preset": gin.H{
			"id":          newPresetID,
			"name":        req.Name,
			"code":        code,
			"description": req.Description,
			"is_default":  false,
			"is_active":   false,
		},
	})
}

func handleDeletePreset(c *gin.Context) {
	presetIDStr := c.Param("id")
	log.Printf("[PRESET DELETE] Request masuk untuk ID: '%s'", presetIDStr)

	presetID, err := strconv.ParseInt(presetIDStr, 10, 64)
	if err != nil {
		log.Printf("[PRESET DELETE] Ditolak: ID preset '%s' bukan integer valid: %v", presetIDStr, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID preset tidak valid"})
		return
	}

	// Check if preset is default
	var isDefault int
	var name string
	err = database.DB.QueryRow("SELECT is_default, name FROM schedule_presets WHERE id = ?", presetID).Scan(&isDefault, &name)
	if err != nil {
		log.Printf("[PRESET DELETE] Ditolak (404): Preset ID %d tidak ditemukan di database schedule_presets", presetID)
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("Preset dengan ID %d tidak ditemukan di sistem", presetID)})
		return
	}

	if isDefault == 1 {
		log.Printf("[PRESET DELETE] Ditolak: Preset ID %d ('%s') adalah preset bawaan sistem (is_default = 1)", presetID, name)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Preset bawaan sistem tidak dapat dihapus"})
		return
	}

	// Check if preset is currently active
	activeID := database.GetSetting("active_preset_id", "1")
	if strconv.FormatInt(presetID, 10) == activeID {
		log.Printf("[PRESET DELETE] Ditolak: Preset ID %d ('%s') sedang aktif di speaker (active_preset_id = %s)", presetID, name, activeID)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Preset ini sedang aktif di speaker bel. Harap aktifkan preset lain terlebih dahulu sebelum menghapus preset ini."})
		return
	}

	// Delete schedules and preset
	resSched, _ := database.DB.Exec("DELETE FROM schedules WHERE preset_id = ?", presetID)
	schedDeleted, _ := resSched.RowsAffected()

	_, err = database.DB.Exec("DELETE FROM schedule_presets WHERE id = ?", presetID)
	if err != nil {
		log.Printf("[PRESET DELETE] Error DB DELETE schedule_presets ID %d: %v", presetID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menghapus preset: " + err.Error()})
		return
	}

	log.Printf("[PRESET DELETE] Berhasil! Preset ID %d ('%s') beserta %d jadwal bel di dalamnya telah dihapus", presetID, name, schedDeleted)
	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("Preset '%s' beserta seluruh jadwal di dalamnya berhasil dihapus", name)})
}

func handleGetSchedules(c *gin.Context) {
	presetID := c.DefaultQuery("preset_id", database.GetSetting("active_preset_id", "1"))
	dayOfWeek := c.Query("day")

	query := `
		SELECT s.id, s.preset_id, s.day_of_week, s.time_trigger, s.title, 
		       s.audio_file_id, COALESCE(a.title, 'None'), COALESCE(s.custom_tts_text, ''), s.is_active
		FROM schedules s
		LEFT JOIN audio_files a ON s.audio_file_id = a.id
		WHERE s.preset_id = ?
	`
	args := []interface{}{presetID}

	if dayOfWeek != "" {
		query += " AND s.day_of_week = ?"
		args = append(args, dayOfWeek)
	}
	query += " ORDER BY s.day_of_week ASC, s.time_trigger ASC"

	rows, err := database.DB.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	list := make([]gin.H, 0)
	for rows.Next() {
		var id, pID, day, isActive int
		var timeTrig, title, audioTitle, ttsText string
		var audioID sql.NullInt64

		if err := rows.Scan(&id, &pID, &day, &timeTrig, &title, &audioID, &audioTitle, &ttsText, &isActive); err == nil {
			list = append(list, gin.H{
				"id":              id,
				"preset_id":       pID,
				"day_of_week":     day,
				"time_trigger":    timeTrig,
				"title":           title,
				"audio_file_id":   audioID.Int64,
				"audio_title":     audioTitle,
				"custom_tts_text": ttsText,
				"is_active":       isActive == 1,
			})
		}
	}
	c.JSON(http.StatusOK, list)
}

func handleCreateSchedule(c *gin.Context) {
	var req struct {
		PresetID      int64  `json:"preset_id" binding:"required"`
		DayOfWeek     int    `json:"day_of_week" binding:"required"`
		TimeTrigger   string `json:"time_trigger" binding:"required"`
		Title         string `json:"title" binding:"required"`
		AudioFileID   *int64 `json:"audio_file_id"`
		CustomTTSText string `json:"custom_tts_text"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	res, err := database.DB.Exec(`
		INSERT INTO schedules (preset_id, day_of_week, time_trigger, title, audio_file_id, custom_tts_text)
		VALUES (?, ?, ?, ?, ?, ?)
	`, req.PresetID, req.DayOfWeek, req.TimeTrigger, req.Title, req.AudioFileID, req.CustomTTSText)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	id, _ := res.LastInsertId()
	c.JSON(http.StatusCreated, gin.H{"id": id, "message": "Jadwal bel berhasil ditambahkan"})
}

func handleUpdateSchedule(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		DayOfWeek     int    `json:"day_of_week"`
		TimeTrigger   string `json:"time_trigger"`
		Title         string `json:"title"`
		AudioFileID   *int64 `json:"audio_file_id"`
		CustomTTSText string `json:"custom_tts_text"`
		IsActive      *bool  `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	isActiveVal := 1
	if req.IsActive != nil && !*req.IsActive {
		isActiveVal = 0
	}

	_, err := database.DB.Exec(`
		UPDATE schedules 
		SET day_of_week = ?, time_trigger = ?, title = ?, audio_file_id = ?, custom_tts_text = ?, is_active = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, req.DayOfWeek, req.TimeTrigger, req.Title, req.AudioFileID, req.CustomTTSText, isActiveVal, id)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Jadwal berhasil diperbarui"})
}

func handleDeleteSchedule(c *gin.Context) {
	id := c.Param("id")
	_, err := database.DB.Exec("DELETE FROM schedules WHERE id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Jadwal berhasil dihapus"})
}

func handleClearDaySchedules(c *gin.Context) {
	presetID := c.Query("preset_id")
	day := c.Query("day")
	if presetID == "" || day == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "preset_id dan day wajib diisi"})
		return
	}

	res, err := database.DB.Exec("DELETE FROM schedules WHERE preset_id = ? AND day_of_week = ?", presetID, day)
	if err != nil {
		log.Printf("[SCHEDULE CLEAR] Error DB: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	deleted, _ := res.RowsAffected()
	log.Printf("[SCHEDULE CLEAR] Sukses mengosongkan jadwal untuk Preset ID %s di Hari %s (%d jadwal dihapus)", presetID, day, deleted)
	c.JSON(http.StatusOK, gin.H{"message": "Jadwal hari berhasil dikosongkan", "deleted_count": deleted})
}

func handleGetAudioList(c *gin.Context) {
	rows, err := database.DB.Query("SELECT id, title, category, file_path, duration_seconds, is_builtin FROM audio_files ORDER BY id ASC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var audios []gin.H
	for rows.Next() {
		var id, dur, isBuiltin int
		var title, cat, path string
		if err := rows.Scan(&id, &title, &cat, &path, &dur, &isBuiltin); err == nil {
			audios = append(audios, gin.H{
				"id":               id,
				"title":            title,
				"category":         cat,
				"file_path":        path,
				"duration_seconds": dur,
				"is_builtin":       isBuiltin == 1,
			})
		}
	}
	c.JSON(http.StatusOK, audios)
}

func handleTestAudio(c *gin.Context) {
	var req struct {
		AudioID  int64  `json:"audio_id"`
		FilePath string `json:"file_path"`
		Title    string `json:"title"`
	}
	_ = c.ShouldBindJSON(&req)

	path := req.FilePath
	title := req.Title
	if req.AudioID > 0 {
		_ = database.DB.QueryRow("SELECT file_path, title FROM audio_files WHERE id = ?", req.AudioID).Scan(&path, &title)
	}

	go func() {
		_ = audio.GlobalPlayer.PlayFile(path, audio.PlayOptions{
			Title:       "Test Audio: " + title,
			TriggerType: "MANUAL_DESKTOP",
			UserName:    "TESTER",
		})
	}()

	c.JSON(http.StatusOK, gin.H{"message": "Audio sedang diuji putar"})
}

func handleUploadAudio(c *gin.Context) {
	if !license.IsFeatureEnabled("feat_custom_audio") {
		c.JSON(http.StatusForbidden, gin.H{
			"error":      "Fitur upload audio custom tidak aktif pada lisensi sekolah ini.",
			"error_code": "FEATURE_CUSTOM_AUDIO_DISABLED",
		})
		return
	}

	file, err := c.FormFile("audio_file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File audio wajib diunggah: " + err.Error()})
		return
	}

	ext := strings.ToLower(filepath.Ext(file.Filename))
	if ext != ".mp3" && ext != ".wav" && ext != ".ogg" && ext != ".m4a" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Format file tidak didukung. Harap gunakan file .mp3, .wav, .ogg, atau .m4a"})
		return
	}

	title := strings.TrimSpace(c.PostForm("title"))
	if title == "" {
		title = strings.TrimSuffix(file.Filename, ext)
	}

	category := strings.TrimSpace(c.PostForm("category"))
	if category == "" {
		category = "Custom Guru"
	}

	uploadDir := database.GetCustomAudioDir()
	_ = os.MkdirAll(uploadDir, 0755)

	cleanFileName := fmt.Sprintf("%d_%s", time.Now().Unix(), filepath.Base(file.Filename))
	destPath := filepath.Join(uploadDir, cleanFileName)

	if err := c.SaveUploadedFile(file, destPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menyimpan file audio: " + err.Error()})
		return
	}

	// Insert into audio_files with is_builtin = 0
	res, err := database.DB.Exec(`
		INSERT INTO audio_files (title, category, file_path, duration_seconds, is_builtin)
		VALUES (?, ?, ?, 0, 0)
	`, title, category, destPath)
	if err != nil {
		_ = os.Remove(destPath)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal mencatat data audio: " + err.Error()})
		return
	}

	newID, _ := res.LastInsertId()
	c.JSON(http.StatusCreated, gin.H{
		"id":               newID,
		"title":            title,
		"category":         category,
		"file_path":        destPath,
		"duration_seconds": 0,
		"is_builtin":       false,
		"message":          "Audio custom berhasil diunggah",
	})
}

func handleUpdateAudio(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Title    string `json:"title" binding:"required"`
		Category string `json:"category"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cat := req.Category
	if cat == "" {
		cat = "Custom Guru"
	}

	_, err := database.DB.Exec("UPDATE audio_files SET title = ?, category = ? WHERE id = ?", req.Title, cat, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Audio berhasil diperbarui"})
}

func handleDeleteAudio(c *gin.Context) {
	id := c.Param("id")

	var isBuiltin int
	var filePath, title string
	err := database.DB.QueryRow("SELECT title, file_path, is_builtin FROM audio_files WHERE id = ?", id).Scan(&title, &filePath, &isBuiltin)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Audio tidak ditemukan"})
		return
	}

	if isBuiltin == 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Audio bawaan sistem tidak boleh dihapus"})
		return
	}

	// Safety check: is this audio used in any schedules?
	rows, err := database.DB.Query("SELECT title FROM schedules WHERE audio_file_id = ?", id)
	if err == nil {
		defer rows.Close()
		var usedIn []string
		for rows.Next() {
			var schTitle string
			if err := rows.Scan(&schTitle); err == nil {
				usedIn = append(usedIn, schTitle)
			}
		}
		if len(usedIn) > 0 {
			c.JSON(http.StatusConflict, gin.H{
				"error":             fmt.Sprintf("Audio '%s' sedang digunakan di jadwal: [%s]. Harap ubah jadwal terlebih dahulu sebelum menghapus.", title, strings.Join(usedIn, ", ")),
				"used_in_schedules": usedIn,
			})
			return
		}
	}

	// Delete from database
	_, err = database.DB.Exec("DELETE FROM audio_files WHERE id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Remove physical file
	_ = os.Remove(filePath)

	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("Audio '%s' berhasil dihapus", title)})
}

func handleGetSessions(c *gin.Context) {
	rows, err := database.DB.Query(`
		SELECT s.id, s.user_id, u.name, s.device_id, s.device_name, s.platform, s.ip_address, s.last_active, s.created_at
		FROM active_sessions s
		LEFT JOIN users_pins u ON s.user_id = u.id
		ORDER BY s.last_active DESC
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var sessions []gin.H
	for rows.Next() {
		var id, uID int64
		var uName sql.NullString
		var dID, dName, plat, ip, lastAct, created string
		if err := rows.Scan(&id, &uID, &uName, &dID, &dName, &plat, &ip, &lastAct, &created); err == nil {
			sessions = append(sessions, gin.H{
				"id":          id,
				"user_id":     uID,
				"user_name":   uName.String,
				"device_id":   dID,
				"device_name": dName,
				"platform":    plat,
				"ip_address":  ip,
				"last_active": lastAct,
				"created_at":  created,
			})
		}
	}
	if sessions == nil {
		sessions = []gin.H{}
	}
	c.JSON(http.StatusOK, gin.H{
		"sessions":    sessions,
		"max_devices": license.GetMaxDevices(),
		"count":       len(sessions),
	})
}

func handleRevokeSession(c *gin.Context) {
	id := c.Param("id")
	_, err := database.DB.Exec("DELETE FROM active_sessions WHERE id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Sesi perangkat berhasil dicabut"})
}

func handleLogout(c *gin.Context) {
	deviceID := c.GetHeader("X-Device-Id")
	clientIP := c.ClientIP()

	if deviceID != "" && deviceID != "unknown-device" {
		_, _ = database.DB.Exec("DELETE FROM active_sessions WHERE device_id = ?", deviceID)
	}
	if clientIP == "127.0.0.1" || clientIP == "::1" {
		_, _ = database.DB.Exec("DELETE FROM active_sessions WHERE ip_address = '127.0.0.1' OR ip_address = '::1' OR device_id = '127.0.0.1' OR device_id = '::1'")
	}
	c.JSON(http.StatusOK, gin.H{"message": "Sesi perangkat berhasil ditutup"})
}

type AnnouncementPayload struct {
	Title        string `json:"title" binding:"required"`
	TtsText      string `json:"tts_text" binding:"required"`
	Language     string `json:"language"`
	ChimeAudioID *int64 `json:"chime_audio_id"`
}

func handleGetAnnouncements(c *gin.Context) {
	rows, err := database.DB.Query("SELECT id, title, tts_text, language, chime_audio_id FROM quick_announcements ORDER BY id ASC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	list := make([]gin.H, 0)
	for rows.Next() {
		var id int
		var title, text, lang string
		var chimeID sql.NullInt64
		if err := rows.Scan(&id, &title, &text, &lang, &chimeID); err == nil {
			var cID interface{}
			if chimeID.Valid {
				cID = chimeID.Int64
			} else {
				cID = nil
			}
			list = append(list, gin.H{
				"id":             id,
				"title":          title,
				"tts_text":       text,
				"language":       lang,
				"chime_audio_id": cID,
			})
		}
	}
	c.JSON(http.StatusOK, list)
}

func handleCreateAnnouncement(c *gin.Context) {
	var req AnnouncementPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Judul dan teks pengumuman wajib diisi"})
		return
	}

	lang := req.Language
	if lang == "" {
		lang = "id-ID"
	}

	res, err := database.DB.Exec(
		"INSERT INTO quick_announcements (title, tts_text, language, chime_audio_id) VALUES (?, ?, ?, ?)",
		req.Title, req.TtsText, lang, req.ChimeAudioID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menambahkan template pengumuman: " + err.Error()})
		return
	}

	newID, _ := res.LastInsertId()
	c.JSON(http.StatusCreated, gin.H{
		"id":      newID,
		"message": "Template pengumuman berhasil ditambahkan",
	})
}

func handleUpdateAnnouncement(c *gin.Context) {
	id := c.Param("id")
	var req AnnouncementPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Judul dan teks pengumuman wajib diisi"})
		return
	}

	lang := req.Language
	if lang == "" {
		lang = "id-ID"
	}

	res, err := database.DB.Exec(
		"UPDATE quick_announcements SET title = ?, tts_text = ?, language = ?, chime_audio_id = ? WHERE id = ?",
		req.Title, req.TtsText, lang, req.ChimeAudioID, id,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal memperbarui template pengumuman: " + err.Error()})
		return
	}

	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Template pengumuman tidak ditemukan"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Template pengumuman berhasil diperbarui"})
}

func handleDeleteAnnouncement(c *gin.Context) {
	id := c.Param("id")
	res, err := database.DB.Exec("DELETE FROM quick_announcements WHERE id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Gagal menghapus template pengumuman: " + err.Error()})
		return
	}

	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Template pengumuman tidak ditemukan"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Template pengumuman berhasil dihapus"})
}

func handleTriggerAnnouncement(c *gin.Context) {
	id := c.Param("id")
	var text string
	var chimeID sql.NullInt64

	err := database.DB.QueryRow("SELECT tts_text, chime_audio_id FROM quick_announcements WHERE id = ?", id).Scan(&text, &chimeID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Pengumuman tidak ditemukan"})
		return
	}

	var chimePath string
	if chimeID.Valid {
		_ = database.DB.QueryRow("SELECT file_path FROM audio_files WHERE id = ?", chimeID.Int64).Scan(&chimePath)
	}

	claims := c.MustGet("claims").(*auth.Claims)
	go func() {
		_ = tts.GlobalEngine.SpeakText(text, chimePath, audio.PlayOptions{
			UserID:   &claims.UserID,
			UserName: claims.Name,
		})
	}()

	c.JSON(http.StatusOK, gin.H{"message": "Pengumuman berhasil disiarkan"})
}

func handleGetLogs(c *gin.Context) {
	rows, err := database.DB.Query(`
		SELECT id, triggered_at, trigger_type, triggered_by_user, audio_title, relay_triggered, status 
		FROM bell_logs 
		ORDER BY id DESC 
		LIMIT 50
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var logs []gin.H
	for rows.Next() {
		var id, relayTrig int
		var timeStr, tType, user, title, status string
		if err := rows.Scan(&id, &timeStr, &tType, &user, &title, &relayTrig, &status); err == nil {
			logs = append(logs, gin.H{
				"id":                id,
				"triggered_at":      timeStr,
				"trigger_type":      tType,
				"triggered_by":      user,
				"audio_title":       title,
				"relay_triggered":   relayTrig == 1,
				"status":            status,
			})
		}
	}
	c.JSON(http.StatusOK, logs)
}

func handleGetSettings(c *gin.Context) {
	rows, err := database.DB.Query("SELECT key, COALESCE(value, ''), COALESCE(description, '') FROM app_settings")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var k, v, desc string
		if err := rows.Scan(&k, &v, &desc); err == nil {
			settings[k] = v
		} else {
			log.Printf("[SETTINGS SCAN ERROR] %v", err)
		}
	}
	c.JSON(http.StatusOK, settings)
}

func handleSaveSettings(c *gin.Context) {
	var req map[string]string
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	for k, v := range req {
		_ = database.SetSetting(k, v)
	}

	c.JSON(http.StatusOK, gin.H{"message": "Pengaturan berhasil disimpan"})
}

func handleTestRelay(c *gin.Context) {
	var req struct {
		PowerOn bool `json:"power_on"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	_ = relay.Instance.SetPower(req.PowerOn)
	c.JSON(http.StatusOK, gin.H{
		"message":  "Relay trigger sent",
		"power_on": relay.Instance.IsOn(),
	})
}

func handleActivateLicense(c *gin.Context) {
	var req struct {
		LicenseKey string `json:"license_key" binding:"required"`
		ServerURL  string `json:"server_url"`
		SchoolName string `json:"school_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	claims, err := license.ActivateLicense(req.LicenseKey, req.ServerURL)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.SchoolName != "" {
		_ = database.SetSetting("school_name", req.SchoolName)
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Lisensi berhasil diaktivasi!",
		"status":  "ACTIVE",
		"edition": license.GetLicenseStatus()["edition"],
		"claims":  claims,
	})
}
