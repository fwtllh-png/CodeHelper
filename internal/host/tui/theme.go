package tui

import (
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Theme holds semantic ANSI-256 color tokens for chrome and transcript lipgloss.
type Theme struct {
	Brand     lipgloss.Color
	Dim       lipgloss.Color
	User      lipgloss.Color
	Think     lipgloss.Color
	Asst      lipgloss.Color
	Success   lipgloss.Color
	Warn      lipgloss.Color
	Danger    lipgloss.Color
	Info      lipgloss.Color
	Live      lipgloss.Color
	ToolDone  lipgloss.Color
	ToolWait  lipgloss.Color
	HeaderFg  lipgloss.Color
	HeaderBg  lipgloss.Color
	ChipFg    lipgloss.Color
	ChipBg    lipgloss.Color
	PhaseWork lipgloss.Color
	PhaseWait lipgloss.Color
	PhaseFail lipgloss.Color
	PhaseDone lipgloss.Color
	Overlay   lipgloss.Color
	System    lipgloss.Color
	Diff      lipgloss.Color
	Sidebar   lipgloss.Color
	SidebarBd lipgloss.Color
	Shimmer   []lipgloss.Color // brightness steps for MotionFull status
}

func defaultTheme() Theme {
	return Theme{
		Brand:     "214",
		Dim:       "245",
		User:      "81",
		Think:     "243",
		Asst:      "252",
		Success:   "114",
		Warn:      "214",
		Danger:    "203",
		Info:      "117",
		Live:      "80",
		ToolDone:  "108",
		ToolWait:  "209",
		HeaderFg:  "255",
		HeaderBg:  "236",
		ChipFg:    "236",
		ChipBg:    "114",
		PhaseWork: "80",
		PhaseWait: "209",
		PhaseFail: "203",
		PhaseDone: "114",
		Overlay:   "209",
		System:    "244",
		Diff:      "178",
		Sidebar:   "246",
		SidebarBd: "238",
		Shimmer:   []lipgloss.Color{"245", "250", "255", "250", "245", "240"},
	}
}

var (
	themeMu      sync.Mutex
	activeTheme  Theme
	themeApplied bool
)

func applyTheme(t Theme) {
	themeMu.Lock()
	defer themeMu.Unlock()
	activeTheme = t
	themeApplied = true

	styleBrand = lipgloss.NewStyle().Bold(true).Foreground(t.Brand)
	styleMuted = lipgloss.NewStyle().Foreground(t.Dim)
	styleUser = lipgloss.NewStyle().Foreground(t.User).Bold(true)
	styleThink = lipgloss.NewStyle().Foreground(t.Think).Italic(true)
	styleAsst = lipgloss.NewStyle().Foreground(t.Asst)
	styleOK = lipgloss.NewStyle().Foreground(t.Success)
	styleWarn = lipgloss.NewStyle().Foreground(t.Warn)
	styleErr = lipgloss.NewStyle().Foreground(t.Danger)
	styleTool = lipgloss.NewStyle().Foreground(t.Info)
	styleToolLive = lipgloss.NewStyle().Foreground(t.Live)
	styleToolDone = lipgloss.NewStyle().Foreground(t.ToolDone)
	styleToolWait = lipgloss.NewStyle().Foreground(t.ToolWait)
	styleHeader = lipgloss.NewStyle().Bold(true).Foreground(t.HeaderFg).Background(t.HeaderBg).Padding(0, 1)
	styleChip = lipgloss.NewStyle().Foreground(t.ChipFg).Background(t.ChipBg).Bold(true)
	stylePhaseWorking = lipgloss.NewStyle().Foreground(t.HeaderBg).Background(t.PhaseWork)
	stylePhaseWait = lipgloss.NewStyle().Foreground(t.HeaderBg).Background(t.PhaseWait)
	stylePhaseFail = lipgloss.NewStyle().Foreground(t.HeaderFg).Background(t.PhaseFail)
	stylePhaseDone = lipgloss.NewStyle().Foreground(t.HeaderBg).Background(t.PhaseDone)
	styleOverlay = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.Overlay).Padding(0, 1)
	styleSystem = lipgloss.NewStyle().Foreground(t.System)
	styleDiff = lipgloss.NewStyle().Foreground(t.Diff)
	styleSidebar = lipgloss.NewStyle().Foreground(t.Sidebar).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(t.SidebarBd).PaddingLeft(1)
}

func ensureTheme() {
	themeMu.Lock()
	ok := themeApplied
	themeMu.Unlock()
	if !ok {
		applyTheme(defaultTheme())
	}
}

// shimmerText cycles brightness on MotionFull; Reduced/Still return plain text.
func (motion MotionMode) shimmerText(s string, tick int) string {
	if motion != MotionFull || s == "" {
		return s
	}
	ensureTheme()
	themeMu.Lock()
	steps := activeTheme.Shimmer
	themeMu.Unlock()
	if len(steps) == 0 {
		return s
	}
	if tick < 0 {
		tick = 0
	}
	c := string(steps[tick%len(steps)])
	// Use a dedicated ANSI256 profile so shimmer works even when the global
	// lipgloss profile is Ascii (tests / redirected stdout).
	out := termenv.NewOutput(nil, termenv.WithProfile(termenv.ANSI256))
	return out.String(s).Foreground(out.Color(c)).String()
}

func init() {
	applyTheme(defaultTheme())
}
