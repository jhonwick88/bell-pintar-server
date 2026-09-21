package audio

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
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

// executePlayback executes Windows media playback natively
func (p *Player) executePlayback(absPath string) error {
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		baseName := filepath.Base(absPath)
		fallbacks := []string{
			filepath.Join("assets", "audio", baseName),
			filepath.Join(`H:\FlutterProject\bell_pintar\server\assets\audio`, baseName),
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
			log.Printf("[AUDIO WARNING] Audio file not found at %s. Simulating 3s beep chime...", absPath)
			time.Sleep(3 * time.Second)
			return nil
		}
	}

	// Use powershell Media.MediaPlayer for Windows
	script := fmt.Sprintf(`
		Add-Type -AssemblyName presentationCore;
		$mediaPlayer = New-Object system.windows.media.mediaplayer;
		$mediaPlayer.open('%s');
		$mediaPlayer.Play();
		Start-Sleep -Milliseconds 500;
		while ($mediaPlayer.NaturalDuration.HasTimeSpan -eq $false) { Start-Sleep -Milliseconds 200 };
		$duration = $mediaPlayer.NaturalDuration.TimeSpan.TotalSeconds;
		Start-Sleep -Seconds ([Math]::Ceiling($duration) + 1);
		$mediaPlayer.Stop();
	`, filepath.ToSlash(absPath))

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
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
