package audio

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"bell_server/internal/database"
	"bell_server/internal/relay"
)

type Player struct {
	mu        sync.Mutex
	isPlaying bool
	currentCmd *exec.Cmd
}

var GlobalPlayer = &Player{}

type PlayOptions struct {
	Title           string
	TriggerType     string // "SCHEDULED", "MANUAL_DESKTOP", "MANUAL_MOBILE", "TTS"
	UserID          *int64
	UserName        string
	ScheduleID      *int64
	Volume          int
	CustomDelaySec  int
}

// PlayFile coordinates Relay trigger, timing delays, and sound playback
func (p *Player) PlayFile(filePath string, opt PlayOptions) error {
	p.mu.Lock()
	if p.isPlaying && p.currentCmd != nil && p.currentCmd.Process != nil {
		log.Printf("[AUDIO] Stopping currently playing audio before starting new track...")
		_ = p.currentCmd.Process.Kill()
	}
	p.isPlaying = true
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		p.isPlaying = false
		p.currentCmd = nil
		p.mu.Unlock()
	}()

	// 1. Check file existence
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		absPath = filePath
	}

	relayBefore, relayAfter := relay.GetDelays()

	// 2. Trigger Relay ON
	log.Printf("[AUDIO SEQUENCE] 1. Activating Relay ON for track '%s'...", opt.Title)
	_ = relay.Instance.SetPower(true)

	// 3. Wait Delay Before
	log.Printf("[AUDIO SEQUENCE] 2. Waiting delay_before (%v)...", relayBefore)
	time.Sleep(relayBefore)

	// 4. Play Audio file
	log.Printf("[AUDIO SEQUENCE] 3. Playing audio file: %s", absPath)
	playErr := p.executePlayback(absPath)

	// 5. Wait Delay After
	log.Printf("[AUDIO SEQUENCE] 4. Waiting delay_after (%v)...", relayAfter)
	time.Sleep(relayAfter)

	// 6. Trigger Relay OFF
	log.Printf("[AUDIO SEQUENCE] 5. Deactivating Relay OFF...")
	_ = relay.Instance.SetPower(false)

	// 7. Record Bell Log to SQLite
	status := "SUCCESS"
	var errMsg *string
	if playErr != nil {
		status = "FAILED"
		msg := playErr.Error()
		errMsg = &msg
		log.Printf("[AUDIO ERROR] Playback failed: %v", playErr)
	}

	recordLog(opt, status, errMsg)
	return playErr
}

// executePlayback executes Windows media playback natively without infinite loops
func (p *Player) executePlayback(absPath string) error {
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		baseName := filepath.Base(absPath)
		fallbacks := []string{
			filepath.Join("assets", "audio", baseName),
			filepath.Join(database.GetCustomAudioDir(), baseName),
			filepath.Join(database.GetAppDataDir(), "audio_custom", baseName),
			filepath.Join(database.GetAppDataDir(), "assets", "audio", baseName),
		}
		if exe, err := os.Executable(); err == nil {
			exeDir := filepath.Dir(exe)
			fallbacks = append(fallbacks,
				filepath.Join(exeDir, "assets", "audio", baseName),
				filepath.Join(exeDir, "audio_custom", baseName),
				filepath.Join(exeDir, "data", "audio_custom", baseName),
			)
		}
		found := false
		for _, fb := range fallbacks {
			if _, fbErr := os.Stat(fb); fbErr == nil {
				absPath, _ = filepath.Abs(fb)
				found = true
				break
			}
		}
		if !found {
			log.Printf("[AUDIO WARNING] Audio file not found at '%s'. Playing Windows System Chime as fallback...", absPath)
			fallbackScript := `
				[System.Media.SystemSounds]::Asterisk.Play();
				Start-Sleep -Milliseconds 800;
				[System.Media.SystemSounds]::Exclamation.Play();
				Start-Sleep -Milliseconds 800;
				[System.Media.SystemSounds]::Asterisk.Play();
			`
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", fallbackScript)
			return cmd.Run()
		}
	}

	ext := strings.ToLower(filepath.Ext(absPath))
	var script string

	if ext == ".wav" {
		// Native SoundPlayer for ultra reliable and fast WAV playback
		script = fmt.Sprintf(`
			try {
				$player = New-Object System.Media.SoundPlayer('%s');
				$player.PlaySync();
				exit 0;
			} catch {
				exit 1;
			}
		`, filepath.ToSlash(absPath))
	} else {
		// Native MediaPlayer for MP3 with safety timeout loop (max 15 iterations / 3 seconds wait for duration)
		script = fmt.Sprintf(`
			Add-Type -AssemblyName presentationCore;
			$mediaPlayer = New-Object system.windows.media.mediaplayer;
			$mediaPlayer.open('%s');
			$mediaPlayer.Play();
			Start-Sleep -Milliseconds 500;
			$wait = 0;
			while ($mediaPlayer.NaturalDuration.HasTimeSpan -eq $false -and $wait -lt 15) {
				Start-Sleep -Milliseconds 200;
				$wait++;
			};
			if ($mediaPlayer.NaturalDuration.HasTimeSpan) {
				$duration = $mediaPlayer.NaturalDuration.TimeSpan.TotalSeconds;
				Start-Sleep -Seconds ([Math]::Ceiling($duration) + 1);
			} else {
				Start-Sleep -Seconds 12;
			};
			$mediaPlayer.Stop();
			$mediaPlayer.Close();
		`, filepath.ToSlash(absPath))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	p.mu.Lock()
	p.currentCmd = cmd
	p.mu.Unlock()

	return cmd.Run()
}

func recordLog(opt PlayOptions, status string, errMsg *string) {
	if database.DB == nil {
		return
	}

	relayActive := 0
	if database.GetSetting("relay_enabled", "1") == "1" {
		relayActive = 1
	}

	userName := opt.UserName
	if userName == "" {
		userName = "SYSTEM"
	}

	_, _ = database.DB.Exec(`
		INSERT INTO bell_logs (
			triggered_at, trigger_type, user_id, triggered_by_user, 
			schedule_id, audio_title, relay_triggered, status, error_message
		) VALUES (CURRENT_TIMESTAMP, ?, ?, ?, ?, ?, ?, ?, ?)
	`, opt.TriggerType, opt.UserID, userName, opt.ScheduleID, opt.Title, relayActive, status, errMsg)
}
