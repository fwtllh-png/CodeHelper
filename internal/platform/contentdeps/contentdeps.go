// Package contentdeps probes optional OCR/speech/pandoc/ffmpeg binaries.
package contentdeps

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Probe reports whether optional content binaries are resolvable via LookPath
// (honoring CODEHELPER_*_BINARY overrides). Keys: ocr, speech, pandoc, ffmpeg.
func Probe() map[string]bool {
	dependencies := map[string]string{
		"ocr":    dependencyName("CODEHELPER_TESSERACT_BINARY", "tesseract"),
		"speech": dependencyName("CODEHELPER_SPEECH_BINARY", "whisper"),
		"pandoc": dependencyName("CODEHELPER_PANDOC_BINARY", "pandoc"),
		"ffmpeg": dependencyName("CODEHELPER_FFMPEG_BINARY", "ffmpeg"),
	}
	available := make(map[string]bool, len(dependencies))
	for name, binary := range dependencies {
		_, err := exec.LookPath(binary)
		available[name] = err == nil
	}
	return available
}

// CodeExecutionReady reports platforms that declare a strong sandbox path.
// Live seatbelt/bwrap probing stays in the sandbox package (CLI must not import it).
func CodeExecutionReady() bool {
	switch runtime.GOOS {
	case "darwin", "linux":
		return true
	default:
		return false
	}
}

// FeatureStatus maps a readiness bool to doctor feature vocabulary.
func FeatureStatus(ready bool) string {
	if ready {
		return "ready"
	}
	return "unavailable"
}

func dependencyName(environment, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(environment)); value != "" {
		return value
	}
	return fallback
}
