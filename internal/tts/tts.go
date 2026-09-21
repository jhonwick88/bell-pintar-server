package tts

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"bell_server/internal/audio"
	"bell_server/internal/database"
)

type Engine struct {
	cacheDir string
}

var GlobalEngine = &Engine{}

func init() {
	GlobalEngine.cacheDir = database.GetTTSCacheDir()
	_ = os.MkdirAll(GlobalEngine.cacheDir, 0755)
}

// SpeakText synthesizes text to speech using Windows OneCore (Microsoft Andika) or SAPI fallback
func (e *Engine) SpeakText(text string, chimeAudioPath string, opt audio.PlayOptions, speedOverride ...float64) error {
	finalSpeed := 1.0
	if len(speedOverride) > 0 && speedOverride[0] >= 0.5 && speedOverride[0] <= 3.0 {
		finalSpeed = speedOverride[0]
	} else {
		savedSpeedStr := database.GetSetting("tts_speed", "1.0")
		var s float64
		if _, err := fmt.Sscanf(savedSpeedStr, "%f", &s); err == nil && s >= 0.5 && s <= 3.0 {
			finalSpeed = s
		}
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("text cannot be empty")
	}

	outWav := filepath.Join(e.cacheDir, fmt.Sprintf("tts_%d.wav", os.Getpid()))
	defer os.Remove(outWav)

	log.Printf("[TTS] Synthesizing text: \"%s\"", text)

	escapedText := strings.ReplaceAll(text, "'", "''")
	escapedWav := filepath.ToSlash(outWav)

		// PowerShell WinRT OneCore Synthesizer (Native Microsoft Andika support for Windows 10/11)
	sapiRate := int((finalSpeed - 1.0) * 10)
	if sapiRate < -10 { sapiRate = -10 }
	if sapiRate > 10 { sapiRate = 10 }

	script := fmt.Sprintf(`
		$outPath = '%s'
		$text = '%s'
		$speed = %.2f
		$sapiRate = %d
		
		try {
			Add-Type -AssemblyName System.Runtime.WindowsRuntime
			$asTaskGeneric = [System.WindowsRuntimeSystemExtensions].GetMethods() | ? { $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation`+"`"+`1' } | Select-Object -First 1
			
			function Await($WinRtTask, $ResultType) {
				$asTask = $asTaskGeneric.MakeGenericMethod($ResultType)
				$netTask = $asTask.Invoke($null, @($WinRtTask))
				$netTask.Wait(-1) | Out-Null
				$netTask.Result
			}

			[Windows.Media.SpeechSynthesis.SpeechSynthesizer, Windows.Media, ContentType = WindowsRuntime] | Out-Null
			$synth = New-Object Windows.Media.SpeechSynthesis.SpeechSynthesizer
			$synth.Options.SpeakingRate = $speed
			$voice = [Windows.Media.SpeechSynthesis.SpeechSynthesizer]::AllVoices | Where-Object { $_.DisplayName -like "*Andika*" -or $_.Language -like "id*" -or $_.Description -like "*Indonesian*" } | Select-Object -First 1

			if ($voice) {
				$synth.Voice = $voice
			}

			$op = $synth.SynthesizeTextToStreamAsync($text)
			$stream = Await $op ([Windows.Media.SpeechSynthesis.SpeechSynthesisStream])
			
			$reader = New-Object Windows.Storage.Streams.DataReader($stream.GetInputStreamAt(0))
			$loadOp = $reader.LoadAsync($stream.Size)
			$loadTask = $asTaskGeneric.MakeGenericMethod([System.UInt32]).Invoke($null, @($loadOp))
			$loadTask.Wait(-1) | Out-Null
			
			$bytes = New-Object byte[] $stream.Size
			$reader.ReadBytes($bytes)
			[System.IO.File]::WriteAllBytes($outPath, $bytes)
			exit 0
		} catch {
			# Fallback to Legacy SAPI 5 if WinRT fails
			Add-Type -AssemblyName System.Speech;
			$synth = New-Object System.Speech.Synthesis.SpeechSynthesizer;
			$synth.SetOutputToWaveFile($outPath);
			$synth.Rate = $sapiRate;
			$synth.Volume = 100;
			$indo = $synth.GetInstalledVoices() | Where-Object { 
				$_.VoiceInfo.Culture.Name -like "id*" -or 
				$_.VoiceInfo.Description -like "*Indonesian*" -or 
				$_.VoiceInfo.Description -like "*Andika*" 
			} | Select-Object -First 1;
			if ($indo) { $synth.SelectVoice($indo.VoiceInfo.Name); }
			$synth.Speak($text);
			$synth.Dispose();
			exit 0
		}
	`, escapedWav, escapedText, finalSpeed, sapiRate)

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	if err := cmd.Run(); err != nil {
		log.Printf("[TTS ERROR] Synthesis failed: %v. Running direct live speak fallback...", err)
		return e.directSpeak(text)
	}

	opt.Title = "Pengumuman TTS: " + text
	opt.TriggerType = "TTS"

	// If chime exists, play chime then audio
	if chimeAudioPath != "" && fileExists(chimeAudioPath) {
		log.Printf("[TTS] Playing chime intro: %s", chimeAudioPath)
		_ = audio.GlobalPlayer.PlayFile(chimeAudioPath, audio.PlayOptions{
			Title:       "Chime Pengumuman",
			TriggerType: "TTS",
			UserName:    opt.UserName,
		})
	}

	return audio.GlobalPlayer.PlayFile(outWav, opt)
}

func (e *Engine) directSpeak(text string) error {
	script := fmt.Sprintf(`
		Add-Type -AssemblyName System.Speech;
		$synth = New-Object System.Speech.Synthesis.SpeechSynthesizer;
		$indo = $synth.GetInstalledVoices() | Where-Object { $_.VoiceInfo.Description -like "*Andika*" } | Select-Object -First 1;
		if ($indo) { $synth.SelectVoice($indo.VoiceInfo.Name); }
		$synth.Speak('%s');
	`, strings.ReplaceAll(text, "'", "''"))

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	return cmd.Run()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// GetAvailableVoices returns list of installed TTS voices on the machine
func GetAvailableVoices() []string {
	script := `
		[Windows.Media.SpeechSynthesis.SpeechSynthesizer, Windows.Media, ContentType = WindowsRuntime] | Out-Null
		[Windows.Media.SpeechSynthesis.SpeechSynthesizer]::AllVoices | ForEach-Object { $_.DisplayName }
	`
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return []string{"Microsoft Andika (Indonesian)"}
	}
	lines := strings.Split(string(out), "\r\n")
	var res []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t != "" {
			res = append(res, t)
		}
	}
	return res
}

// GetSetting gets setting helper
func getSetting(key, fallback string) string {
	return database.GetSetting(key, fallback)
}
