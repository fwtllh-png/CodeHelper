// Package contentdeps probes optional OCR/speech/pandoc/ffmpeg binaries.
package contentdeps

import (
	"os"
	"os/exec"
	"strings"
)

// Probe reports whether optional content binaries are resolvable via LookPath
// (honoring QCODE_*_BINARY overrides). Keys: ocr, speech, pandoc, ffmpeg.
func Probe() map[string]bool {
	dependencies := map[string]string{
		"ocr":    dependencyName("QCODE_TESSERACT_BINARY", "tesseract"),
		"speech": dependencyName("QCODE_SPEECH_BINARY", "whisper"),
		"pandoc": dependencyName("QCODE_PANDOC_BINARY", "pandoc"),
		"ffmpeg": dependencyName("QCODE_FFMPEG_BINARY", "ffmpeg"),
	}
	available := make(map[string]bool, len(dependencies))
	for name, binary := range dependencies {
		_, err := exec.LookPath(binary)
		available[name] = err == nil
	}
	return available
}

func dependencyName(environment, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(environment)); value != "" {
		return value
	}
	return fallback
}
