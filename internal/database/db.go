package database

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

var DB *sql.DB

// InitDB initializes SQLite connection with proper PRAGMAs and auto-created directory
func InitDB(dbPath string) (*sql.DB, error) {
	if dbPath == "" {
		dbPath = GetDefaultDBPath()
	}

	// Ensure parent directory exists before SQLite tries to open the file
	parentDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		log.Printf("[DB WARNING] Failed to create directory %s: %v", parentDir, err)
	}

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		log.Printf("[DB WARNING] Database file %s not found. It will be created.", dbPath)
	}

	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// PRAGMA configuration for concurrency and foreign key enforcement
	pragmas := []string{
		"PRAGMA foreign_keys = ON;",
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA busy_timeout = 5000;",
	}

	for _, p := range pragmas {
		if _, err := conn.Exec(p); err != nil {
			log.Printf("[DB WARNING] Failed to execute %s: %v", p, err)
		}
	}

	conn.SetMaxOpenConns(10)
	conn.SetMaxIdleConns(5)

	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	DB = conn
	log.Printf("[DB] Connected successfully to %s", dbPath)
	ensureSchema(DB)
	return DB, nil
}

func ensureSchema(db *sql.DB) {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users_pins (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'admin',
			pin_hash TEXT NOT NULL,
			can_trigger_manual INTEGER DEFAULT 1,
			can_change_preset INTEGER DEFAULT 1,
			can_send_tts INTEGER DEFAULT 1,
			is_active INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		`CREATE TABLE IF NOT EXISTS app_settings (
			key TEXT PRIMARY KEY,
			value TEXT,
			description TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		`CREATE TABLE IF NOT EXISTS schedule_presets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			code TEXT UNIQUE,
			description TEXT,
			is_default INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		`CREATE TABLE IF NOT EXISTS audio_files (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			category TEXT DEFAULT 'general',
			file_path TEXT NOT NULL,
			duration_seconds INTEGER DEFAULT 0,
			is_builtin INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		`CREATE TABLE IF NOT EXISTS schedules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			preset_id INTEGER NOT NULL,
			day_of_week INTEGER NOT NULL,
			time_trigger TEXT NOT NULL,
			title TEXT NOT NULL,
			audio_file_id INTEGER,
			custom_tts_text TEXT,
			is_active INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(preset_id) REFERENCES schedule_presets(id) ON DELETE CASCADE,
			FOREIGN KEY(audio_file_id) REFERENCES audio_files(id) ON DELETE SET NULL
		);`,

		`CREATE TABLE IF NOT EXISTS calendar_exceptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			exception_date TEXT NOT NULL UNIQUE,
			title TEXT NOT NULL,
			is_holiday INTEGER DEFAULT 1,
			override_preset_id INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		`CREATE TABLE IF NOT EXISTS quick_announcements (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			tts_text TEXT NOT NULL,
			language TEXT DEFAULT 'id-ID',
			chime_audio_id INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,

		`CREATE TABLE IF NOT EXISTS bell_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			triggered_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			trigger_type TEXT NOT NULL,
			user_id INTEGER,
			triggered_by_user TEXT,
			schedule_id INTEGER,
			audio_title TEXT,
			relay_triggered INTEGER DEFAULT 0,
			status TEXT,
			error_message TEXT
		);`,

		`CREATE TABLE IF NOT EXISTS license_data (
			id INTEGER PRIMARY KEY,
			license_key TEXT,
			hardware_id TEXT,
			product_code TEXT DEFAULT 'BELL_PINTAR',
			edition TEXT,
			expires_at TEXT,
			signature TEXT,
			is_valid INTEGER DEFAULT 0,
			last_synced_at DATETIME
		);`,

		`CREATE TABLE IF NOT EXISTS active_sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER,
			device_id TEXT NOT NULL UNIQUE,
			device_name TEXT,
			platform TEXT,
			ip_address TEXT,
			last_active DATETIME DEFAULT CURRENT_TIMESTAMP,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
	}

	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			log.Printf("[DB WARNING] Failed to execute schema query: %v", err)
		}
	}

	// Seed default initial data if tables are empty
	seedInitialData(db)

	// Ensure custom audio and TTS directories exist
	_ = os.MkdirAll(GetCustomAudioDir(), 0755)
	_ = os.MkdirAll(GetTTSCacheDir(), 0755)
}

func seedInitialData(db *sql.DB) {
	// 1. Seed users_pins if empty
	var userCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM users_pins").Scan(&userCount)
	if userCount == 0 {
		_, _ = db.Exec(`
			INSERT INTO users_pins (name, role, pin_hash, can_trigger_manual, can_change_preset, can_send_tts, is_active)
			VALUES 
				('Admin TU', 'admin', '123456', 1, 1, 1, 1),
				('Guru Piket', 'operator', '7890', 1, 0, 1, 1);
		`)
		log.Println("[DB SEED] Default users (Admin TU, Guru Piket) created.")
	}

	// 2. Seed app_settings if empty
	var settingCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM app_settings").Scan(&settingCount)
	if settingCount == 0 {
		_, _ = db.Exec(`
			INSERT INTO app_settings (key, value, description)
			VALUES 
				('school_name', 'SMK Pintar Labs Indonesia', 'Nama Sekolah'),
				('api_port', '8088', 'Port API Server'),
				('tts_engine', 'onecore', 'Engine Suara TTS Windows'),
				('tts_speed', '1.0', 'Kecepatan Bicara Suara Bel'),
				('tts_voice', 'Microsoft Andika', 'Nama Profil Suara TTS'),
				('active_preset_id', '1', 'Preset Jadwal Bel yang Aktif'),
				('volume', '80', 'Volume Master Bel Sekolah'),
				('edition', 'FREE', 'Edisi Lisensi Sistem');
		`)
		log.Println("[DB SEED] Default app settings created.")
	}

	// 3. Seed schedule_presets if empty
	var presetCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM schedule_presets").Scan(&presetCount)
	if presetCount == 0 {
		_, _ = db.Exec(`
			INSERT INTO schedule_presets (id, name, code, description, is_default)
			VALUES 
				(1, 'Jadwal Reguler 5 Hari (Senin-Jumat)', 'REGULAR_5D', 'Jadwal standar sekolah 5 hari kerja (Sabtu-Minggu libur)', 1),
				(2, 'Jadwal Reguler 6 Hari (Senin-Sabtu)', 'REGULAR_6D', 'Jadwal standar sekolah 6 hari kerja', 0),
				(3, 'Jadwal Khusus Bulan Ramadhan', 'RAMADHAN', 'Jadwal khusus dengan durasi jam pelajaran dipersingkat', 0),
				(4, 'Jadwal Penilaian Akhir Semester (PAS / Ujian)', 'EXAM', 'Jadwal khusus masa ujian dengan 2-3 sesi per hari', 0),
				(5, 'Jadwal Porseni / Classmeeting', 'EVENT', 'Jadwal khusus acara dan kegiatan sekolah', 0);
		`)
		log.Println("[DB SEED] Default schedule presets created.")
	}

	// 4. Seed quick_announcements if empty
	var annCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM quick_announcements").Scan(&annCount)
	if annCount == 0 {
		_, _ = db.Exec(`
			INSERT INTO quick_announcements (title, tts_text, language)
			VALUES 
				('Himbauan Masuk Kelas', 'Perhatian kepada seluruh siswa dan siswi, waktu istirahat telah selesai. Harap segera memasuki ruang kelas masing-masing dengan tertib.', 'id-ID'),
				('Upacara Bendera', 'Perhatian seluruh siswa dan dewan guru, upacara bendera hari Senin akan segera dimulai. Mohon segera berkumpul di lapangan upacara.', 'id-ID'),
				('Panggilan Guru Piket', 'Panggilan kepada bapak dan ibu guru piket hari ini, dimohon segera menuju ke ruang piket. Terima kasih.', 'id-ID');
		`)
		log.Println("[DB SEED] Default quick announcements created.")
	}

	// 5. Seed default audio files if empty
	var audioCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM audio_files").Scan(&audioCount)
	if audioCount == 0 {
		builtinAudios := []struct {
			Title    string
			Category string
			Filename string
		}{
			{"Standard Ding Dong", "chime", "01_ding_dong_standard.wav"},
			{"Westminster Chimes", "chime", "02_westminster_chimes.wav"},
			{"Masuk Jam Pertama", "bell", "03_masuk_jam1.wav"},
			{"Pergantian Jam Pelajaran", "bell", "04_pergantian_jam.wav"},
			{"Istirahat Pertama", "bell", "05_istirahat_pertama.wav"},
			{"Masuk Setelah Istirahat", "bell", "06_masuk_setelah_istirahat.wav"},
			{"Sholat Dzuhur Berjamaah", "bell", "07_sholat_dzuhur.wav"},
			{"Pulang Sekolah", "bell", "08_pulang_sekolah.wav"},
			{"Persiapan Upacara Bendera", "bell", "09_persiapan_upacara.wav"},
			{"Indonesia Raya Chime", "national", "10_indonesia_raya_chime.wav"},
			{"Ujian Mulai", "exam", "11_ujian_mulai.wav"},
			{"Ujian Selesai", "exam", "12_ujian_selesai.wav"},
		}

		for _, a := range builtinAudios {
			path := filepath.Join("assets", "audio", a.Filename)
			if exe, err := os.Executable(); err == nil {
				exeRel := filepath.Join(filepath.Dir(exe), "assets", "audio", a.Filename)
				if _, err := os.Stat(exeRel); err == nil {
					path = exeRel
				}
			}
			_, _ = db.Exec(`
				INSERT INTO audio_files (title, category, file_path, duration_seconds, is_builtin)
				VALUES (?, ?, ?, 10, 1)
			`, a.Title, a.Category, path)
		}
		log.Println("[DB SEED] Default audio files registered.")
	}

	// Auto-scan assets/audio for all MP3 and WAV files
	syncAssetsAudioFiles(db)

	// 6. Seed regular schedules if empty (Days 1 to 5 only, Saturday and Sunday empty)
	var schedCount int
	_ = db.QueryRow("SELECT COUNT(*) FROM schedules").Scan(&schedCount)
	if schedCount == 0 {
		for day := 1; day <= 5; day++ {
			if day == 5 {
				_, _ = db.Exec(`
					INSERT INTO schedules (preset_id, day_of_week, time_trigger, title, is_active)
					VALUES 
						(1, 5, '06:55', 'Lagu Indonesia Raya Pembuka', 1),
						(1, 5, '07:00', 'Masuk Jam Pelajaran Ke-1', 1),
						(1, 5, '09:00', 'Waktu Istirahat Jumat', 1),
						(1, 5, '09:20', 'Masuk Kelas Jam Ke-4', 1),
						(1, 5, '11:20', 'Bel Pulang & Persiapan Sholat Jumat', 1);
				`)
			} else {
				_, _ = db.Exec(`
					INSERT INTO schedules (preset_id, day_of_week, time_trigger, title, is_active)
					VALUES 
						(1, ?, '07:00', 'Masuk Jam Pertama', 1),
						(1, ?, '09:45', 'Istirahat Pertama', 1),
						(1, ?, '10:15', 'Masuk Setelah Istirahat', 1),
						(1, ?, '12:00', 'Istirahat & Sholat Dzuhur', 1),
						(1, ?, '15:00', 'Bel Pulang Sekolah', 1);
				`, day, day, day, day, day)
			}
		}
		log.Println("[DB SEED] Default regular schedules created (Senin - Jumat).")
	}
}

// GetAppDataDir returns the root data directory.
// Priority:
// 1. Environment variable BELL_DATA_DIR
// 2. Dev environment H:\AMAN (if drive H and directory exist)
// 3. Application executable directory / data
// 4. Fallback: ./data
func GetAppDataDir() string {
	if custom := os.Getenv("BELL_DATA_DIR"); custom != "" {
		_ = os.MkdirAll(custom, 0755)
		return custom
	}

	// Dev environment compatibility
	if _, err := os.Stat(`H:\AMAN`); err == nil {
		return `H:\AMAN`
	}

	// Portable / Exe relative directory
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Join(filepath.Dir(exe), "data")
		_ = os.MkdirAll(dir, 0755)
		return dir
	}

	// Fallback current working directory
	dir := "data"
	_ = os.MkdirAll(dir, 0755)
	return dir
}

// GetDefaultDBPath returns default path for SQLite database
func GetDefaultDBPath() string {
	return filepath.Join(GetAppDataDir(), "bell.db")
}

// GetCustomAudioDir returns storage directory for custom uploaded audio files
func GetCustomAudioDir() string {
	dir := filepath.Join(GetAppDataDir(), "audio_custom")
	_ = os.MkdirAll(dir, 0755)
	return dir
}

// GetTTSCacheDir returns directory for temporary TTS wav files
func GetTTSCacheDir() string {
	dir := filepath.Join(GetAppDataDir(), "tts_cache")
	_ = os.MkdirAll(dir, 0755)
	return dir
}

// GetSetting returns setting value by key
func GetSetting(key string, fallback string) string {
	if DB == nil {
		return fallback
	}
	var val string
	err := DB.QueryRow("SELECT value FROM app_settings WHERE key = ?", key).Scan(&val)
	if err != nil {
		return fallback
	}
	return val
}

// SetSetting updates or inserts a setting
func SetSetting(key, val string) error {
	if DB == nil {
		return fmt.Errorf("db not initialized")
	}
	_, err := DB.Exec("INSERT OR REPLACE INTO app_settings (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)", key, val)
	return err
}

// syncAssetsAudioFiles scans assets/audio for all MP3 and WAV files and registers any missing ones
func syncAssetsAudioFiles(db *sql.DB) {
	candidates := []string{
		filepath.Join("assets", "audio"),
		`H:\FlutterProject\bell_pintar\server\assets\audio`,
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "assets", "audio"))
	}

	var audioDir string
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			audioDir = c
			break
		}
	}
	if audioDir == "" {
		return
	}

	entries, err := os.ReadDir(audioDir)
	if err != nil {
		return
	}

	addedCount := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".mp3" && ext != ".wav" {
			continue
		}

		filename := e.Name()
		fullPath, _ := filepath.Abs(filepath.Join(audioDir, filename))

		// Check if already registered by filename
		var exists int
		_ = db.QueryRow("SELECT COUNT(*) FROM audio_files WHERE file_path LIKE ?", "%"+filename).Scan(&exists)
		if exists > 0 {
			continue
		}

		nameNoExt := strings.TrimSuffix(filename, filepath.Ext(filename))
		title := strings.Join(strings.Fields(nameNoExt), " ")

		nameLower := strings.ToLower(title)
		category := "bell"
		if strings.Contains(nameLower, "ujian") {
			category = "exam"
		} else if strings.Contains(nameLower, "sholat") || strings.Contains(nameLower, "kerohanian") || strings.Contains(nameLower, "al-qur") || strings.Contains(nameLower, "dzuhur") {
			category = "prayer"
		} else if strings.Contains(nameLower, "upacara") || strings.Contains(nameLower, "indonesia raya") {
			category = "ceremony"
		} else if strings.Contains(nameLower, "kebersihan") || strings.Contains(nameLower, "kepramukaan") || strings.Contains(nameLower, "porseni") || strings.Contains(nameLower, "classmeeting") {
			category = "event"
		} else if strings.Contains(nameLower, "rington") || strings.Contains(nameLower, "chime") || strings.Contains(nameLower, "nada") || strings.Contains(nameLower, "ding dong") {
			category = "chime"
		}

		_, err := db.Exec(`
			INSERT INTO audio_files (title, category, file_path, duration_seconds, is_builtin)
			VALUES (?, ?, ?, 15, 1)
		`, title, category, fullPath)
		if err == nil {
			addedCount++
		}
	}

	if addedCount > 0 {
		log.Printf("[AUDIO SYNC] Berhasil mendaftarkan %d file audio baru dari direktori assets/audio.", addedCount)
	}
}
