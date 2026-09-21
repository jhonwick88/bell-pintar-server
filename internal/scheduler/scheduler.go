package scheduler

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"bell_server/internal/audio"
	"bell_server/internal/database"
	"bell_server/internal/tts"
)

type Scheduler struct {
	mu          sync.Mutex
	isRunning   bool
	stopChan    chan struct{}
	rungMinutes map[string]bool // "2026-09-19_07:00_scheduleID" to ensure only 1 ring per minute
}

type NextScheduleInfo struct {
	ScheduleID    int64  `json:"schedule_id"`
	PresetName    string `json:"preset_name"`
	DayOfWeek     int    `json:"day_of_week"`
	TimeTrigger   string `json:"time_trigger"`
	Title         string `json:"title"`
	AudioTitle    string `json:"audio_title"`
	SecondsUntil  int64  `json:"seconds_until"`
	IsHoliday     bool   `json:"is_holiday"`
	HolidayReason string `json:"holiday_reason,omitempty"`
}

var GlobalScheduler = &Scheduler{
	stopChan:    make(chan struct{}),
	rungMinutes: make(map[string]bool),
}

func (s *Scheduler) Start() {
	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		return
	}
	s.isRunning = true
	s.mu.Unlock()

	log.Println("[SCHEDULER] Precision Background Scheduler started (1-sec interval).")

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-s.stopChan:
				log.Println("[SCHEDULER] Scheduler stopped.")
				return
			case now := <-ticker.C:
				s.checkAndTrigger(now)
			}
		}
	}()
}

func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.isRunning {
		return
	}
	s.isRunning = false
	close(s.stopChan)
}

func (s *Scheduler) checkAndTrigger(now time.Time) {
	if database.DB == nil {
		return
	}

	dateStr := now.Format("2006-01-02")
	minuteStr := now.Format("15:04")
	timeMinZero := minuteStr + ":00"

	// 1. Check Holiday / Exception Override for today
	var isHoliday int
	var overridePresetID sql.NullInt64
	var holidayTitle string

	err := database.DB.QueryRow(`
		SELECT is_holiday, override_preset_id, title 
		FROM calendar_exceptions 
		WHERE exception_date = ?
	`, dateStr).Scan(&isHoliday, &overridePresetID, &holidayTitle)

	if err == nil && isHoliday == 1 {
		// Today is holiday, do not ring scheduled bells
		return
	}

	// 2. Determine active preset
	var activePresetID int64
	if overridePresetID.Valid {
		activePresetID = overridePresetID.Int64
	} else {
		presetSetting := database.GetSetting("active_preset_id", "1")
		id, _ := strconv.ParseInt(presetSetting, 10, 64)
		if id == 0 {
			id = 1
		}
		activePresetID = id
	}

	// Day of week: Go Sunday=0, Monday=1, ..., Saturday=6
	// In our DB: 1=Senin, 2=Selasa, ..., 5=Jumat, 6=Sabtu, 7=Minggu
	dayOfWeek := int(now.Weekday())
	if dayOfWeek == 0 {
		dayOfWeek = 7 // Minggu
	}

	// 3. Query active schedules matching current minute (both "HH:MM" and "HH:MM:00")
	rows, err := database.DB.Query(`
		SELECT s.id, s.title, s.custom_tts_text, COALESCE(s.volume_override, 0),
		       COALESCE(a.file_path, ''), COALESCE(a.title, '')
		FROM schedules s
		LEFT JOIN audio_files a ON s.audio_file_id = a.id
		WHERE s.preset_id = ? 
		  AND s.day_of_week = ? 
		  AND (s.time_trigger = ? OR s.time_trigger = ? OR s.time_trigger LIKE ?)
		  AND s.is_active = 1
	`, activePresetID, dayOfWeek, minuteStr, timeMinZero, minuteStr+":%")

	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var scheduleID int64
		var title, customTTS, audioPath, audioTitle string
		var volume int

		if err := rows.Scan(&scheduleID, &title, &customTTS, &volume, &audioPath, &audioTitle); err != nil {
			continue
		}

		// Deduplication: ensure this schedule only rings ONCE in this exact minute
		minuteKey := fmt.Sprintf("%s_%s_%d", dateStr, minuteStr, scheduleID)
		s.mu.Lock()
		if s.rungMinutes[minuteKey] {
			s.mu.Unlock()
			continue
		}
		s.rungMinutes[minuteKey] = true
		if len(s.rungMinutes) > 300 {
			s.rungMinutes = map[string]bool{minuteKey: true}
		}
		s.mu.Unlock()

		log.Printf("[SCHEDULER TRIGGER] Ringing Bell ONCE: '%s' at %s (Schedule ID: %d)", title, minuteStr, scheduleID)

		go func(scID int64, t, cTTS, aPath, aTitle string, vol int) {
			opt := audio.PlayOptions{
				Title:       t,
				TriggerType: "SCHEDULED",
				ScheduleID:  &scID,
				UserName:    "SYSTEM_SCHEDULER",
				Volume:      vol,
			}

			if cTTS != "" {
				_ = tts.GlobalEngine.SpeakText(cTTS, aPath, opt)
			} else if aPath != "" {
				_ = audio.GlobalPlayer.PlayFile(aPath, opt)
			} else {
				log.Printf("[SCHEDULER] No audio or TTS assigned to schedule %d", scID)
			}
		}(scheduleID, title, customTTS, audioPath, audioTitle, volume)
	}
}

// GetNextSchedule calculates the next upcoming schedule and remaining seconds
func GetNextSchedule() (*NextScheduleInfo, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("db not initialized")
	}

	now := time.Now()
	dateStr := now.Format("2006-01-02")
	timeStr := now.Format("15:04:05")

	// Check holiday
	var isHoliday int
	var overridePresetID sql.NullInt64
	var holidayTitle string

	err := database.DB.QueryRow(`
		SELECT is_holiday, override_preset_id, title 
		FROM calendar_exceptions 
		WHERE exception_date = ?
	`, dateStr).Scan(&isHoliday, &overridePresetID, &holidayTitle)

	if err == nil && isHoliday == 1 {
		return &NextScheduleInfo{
			IsHoliday:     true,
			HolidayReason: holidayTitle,
			Title:         "Hari Libur Sekolah (" + holidayTitle + ")",
		}, nil
	}

	var activePresetID int64
	if overridePresetID.Valid {
		activePresetID = overridePresetID.Int64
	} else {
		id, _ := strconv.ParseInt(database.GetSetting("active_preset_id", "1"), 10, 64)
		if id == 0 {
			id = 1
		}
		activePresetID = id
	}

	dayOfWeek := int(now.Weekday())
	if dayOfWeek == 0 {
		dayOfWeek = 7
	}

	var res NextScheduleInfo
	var presetName, audioTitle string

	// Query next schedule today after current time
	err = database.DB.QueryRow(`
		SELECT s.id, p.name, s.day_of_week, s.time_trigger, s.title, COALESCE(a.title, 'Default Audio')
		FROM schedules s
		JOIN schedule_presets p ON s.preset_id = p.id
		LEFT JOIN audio_files a ON s.audio_file_id = a.id
		WHERE s.preset_id = ? 
		  AND s.day_of_week = ? 
		  AND s.time_trigger > ?
		  AND s.is_active = 1
		ORDER BY s.time_trigger ASC
		LIMIT 1
	`, activePresetID, dayOfWeek, timeStr).Scan(&res.ScheduleID, &presetName, &res.DayOfWeek, &res.TimeTrigger, &res.Title, &audioTitle)

	if err == nil {
		res.PresetName = presetName
		res.AudioTitle = audioTitle

		// Calculate seconds until
		targetTime, _ := time.Parse("15:04:05", res.TimeTrigger)
		currentTime, _ := time.Parse("15:04:05", timeStr)
		res.SecondsUntil = int64(targetTime.Sub(currentTime).Seconds())
		return &res, nil
	}

	// If no more schedules today, find first schedule for tomorrow
	nextDay := (dayOfWeek % 7) + 1
	err = database.DB.QueryRow(`
		SELECT s.id, p.name, s.day_of_week, s.time_trigger, s.title, COALESCE(a.title, 'Default Audio')
		FROM schedules s
		JOIN schedule_presets p ON s.preset_id = p.id
		LEFT JOIN audio_files a ON s.audio_file_id = a.id
		WHERE s.preset_id = ? 
		  AND s.day_of_week = ? 
		  AND s.is_active = 1
		ORDER BY s.time_trigger ASC
		LIMIT 1
	`, activePresetID, nextDay).Scan(&res.ScheduleID, &presetName, &res.DayOfWeek, &res.TimeTrigger, &res.Title, &audioTitle)

	if err == nil {
		res.PresetName = presetName
		res.AudioTitle = audioTitle
		res.SecondsUntil = 86400 // Tomorrow
		return &res, nil
	}

	return &NextScheduleInfo{
		Title: "Tidak ada jadwal bel aktif berikutnya",
	}, nil
}
