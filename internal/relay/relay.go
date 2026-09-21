package relay

import (
	"log"
	"strconv"
	"sync"
	"time"

	"bell_server/internal/database"
)

type Controller struct {
	mu           sync.Mutex
	isPoweredOn  bool
	mode         string // "SIMULATED", "USB_HID", "SERIAL_COM"
	lastActiveAt time.Time
}

var Instance = &Controller{
	mode: "SIMULATED",
}

// SetPower controls the Relay state (true = ON, false = OFF)
func (rc *Controller) SetPower(on bool) error {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	enabled := database.GetSetting("relay_enabled", "1") == "1"
	if !enabled {
		log.Printf("[RELAY] Disabled in settings. Skipping power change.")
		rc.isPoweredOn = false
		return nil
	}

	rc.mode = database.GetSetting("relay_type", "USB_HID")
	port := database.GetSetting("relay_port", "COM3")

	if on {
		log.Printf("[RELAY TRIGGER] >>> AMPLIFIER POWER ON <<< (Mode: %s, Port: %s)", rc.mode, port)
		rc.isPoweredOn = true
		rc.lastActiveAt = time.Now()
	} else {
		log.Printf("[RELAY TRIGGER] <<< AMPLIFIER POWER OFF >>> (Mode: %s, Port: %s)", rc.mode, port)
		rc.isPoweredOn = false
	}

	return nil
}

// IsOn checks current relay power state
func (rc *Controller) IsOn() bool {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.isPoweredOn
}

// GetDelays returns configured (delayBeforeSec, delayAfterSec)
func GetDelays() (time.Duration, time.Duration) {
	beforeStr := database.GetSetting("relay_delay_before_sec", "5")
	afterStr := database.GetSetting("relay_delay_after_sec", "5")

	b, _ := strconv.Atoi(beforeStr)
	a, _ := strconv.Atoi(afterStr)

	if b <= 0 {
		b = 3
	}
	if a <= 0 {
		a = 3
	}

	return time.Duration(b) * time.Second, time.Duration(a) * time.Second
}
