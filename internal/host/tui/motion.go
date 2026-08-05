package tui

import (
	"os"
	"strings"
	"time"
)

// MotionMode controls live-row animation density (DOG motion contract).
type MotionMode string

const (
	MotionFull    MotionMode = "full"
	MotionReduced MotionMode = "reduced"
	MotionStill   MotionMode = "still"
)

func detectMotionMode() MotionMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CODEHELPER_MOTION"))) {
	case "still", "off", "0":
		return MotionStill
	case "reduced", "reduce":
		return MotionReduced
	default:
		if os.Getenv("NO_ANIMATION") != "" || os.Getenv("CODEHELPER_REDUCED_MOTION") != "" {
			return MotionReduced
		}
		return MotionFull
	}
}

func (m MotionMode) spinnerFrame(tick int) string {
	if m == MotionStill {
		return "•"
	}
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	if m == MotionReduced {
		frames = []string{"·", "•", "·"}
	}
	if len(frames) == 0 {
		return "•"
	}
	if tick < 0 {
		tick = 0
	}
	return frames[tick%len(frames)]
}

const doneBreathDuration = 800 * time.Millisecond

func (m Model) inDoneBreath() bool {
	return !m.doneBreathUntil.IsZero() && time.Now().Before(m.doneBreathUntil)
}
