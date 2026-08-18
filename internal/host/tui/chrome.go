package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func (m Model) postureLabel() string {
	switch m.posture {
	case "suggest":
		return "Ask"
	case "bypass":
		return "Full"
	default:
		return "Auto"
	}
}

func (m Model) modeChip() string {
	switch m.toolMode {
	case policy.ModePlan:
		return "Plan"
	case policy.ModeOperate:
		return "Operate"
	default:
		return "Act"
	}
}

func (m Model) contextPercent() int {
	window := m.contextWindow
	if window == 0 {
		window = 262144
	}
	used := m.contextTokensUsed
	if used == 0 && (m.tokenPrompt > 0 || m.tokenCompletion > 0) {
		used = m.tokenPrompt + m.tokenCompletion
	}
	if used == 0 {
		return 0
	}
	pct := int((used * 100) / window)
	if pct > 100 {
		pct = 100
	}
	return pct
}

func (m Model) renderHeader() string {
	ws := m.workspaceRoot
	if ws == "" {
		ws = "."
	}
	ws = filepath.Base(ws)
	pct := m.contextPercent()
	ctxStyle := styleMuted
	if pct >= 95 {
		ctxStyle = styleErr
	} else if pct >= 85 {
		ctxStyle = styleWarn
	}
	tokens := ""
	if m.tokenPrompt+m.tokenCompletion > 0 {
		tokens = fmt.Sprintf("  %d tok", m.tokenPrompt+m.tokenCompletion)
	}
	ctx := ""
	if pct > 0 {
		ctx = ctxStyle.Render(fmt.Sprintf("  ctx %d%%", pct))
	}
	chip := styleChip.Render(" " + m.modeChip() + " ")
	left := styleBrand.Render("codehelper") + "  " + chip + "  " +
		styleMuted.Render(fmt.Sprintf("%s · %s/%s", ws, m.provider, m.modelID))
	right := styleMuted.Render(shortSession(m.session)) + tokens + ctx
	width := m.width
	if width <= 0 {
		width = 80
	}
	rightW := lipgloss.Width(right)
	maxLeft := width - rightW - 3
	if maxLeft < 12 {
		maxLeft = 12
	}
	if lipgloss.Width(left) > maxLeft {
		left = lipgloss.NewStyle().MaxWidth(maxLeft).Render(left)
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	row := left + strings.Repeat(" ", gap) + right
	// Force a single physical row — wrapped headers collide with transcript.
	row = strings.ReplaceAll(row, "\n", " ")
	return styleHeader.Width(width).MaxHeight(1).Render(row)
}

func shortSession(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 16 {
		return id
	}
	return id[:8] + "…" + id[len(id)-4:]
}

func (m Model) renderPhaseStrip() string {
	phase := m.phase
	if phase == PhaseIdle || phase == PhaseTyping {
		return ""
	}
	if m.inDoneBreath() && phase == PhaseDone {
		// keep strip visible during completion breath
	} else if phase == PhaseDone && !m.inDoneBreath() {
		return ""
	}
	// Thinking already shows progress — suppress redundant Working band.
	if phase == PhaseWorking && m.liveThinkingActive() {
		return ""
	}
	label := string(phase)
	style := stylePhaseWorking
	prefix := "●"
	switch phase {
	case PhaseWorking:
		label = "working"
		if m.motion != MotionStill {
			prefix = m.motion.spinnerFrame(m.spinnerTick)
		}
		label = m.motion.shimmerText(label, m.spinnerTick)
	case PhaseWaiting:
		label = "waiting"
		style = stylePhaseWait
		prefix = "◐"
	case PhaseApproval:
		label = "approval needed"
		style = stylePhaseWait
		prefix = "!"
	case PhaseIncomplete:
		label = "incomplete"
		style = stylePhaseWait
		prefix = "!"
	case PhaseFailed:
		label = "failed"
		style = stylePhaseFail
		prefix = "✗"
	case PhaseDone:
		label = "done"
		style = stylePhaseDone
		prefix = "✓"
	}
	return style.Render(fmt.Sprintf(" %s %s ", prefix, label))
}

func (m Model) liveThinkingActive() bool {
	if m.streamReasonIdx >= 0 && m.streamReasonIdx < len(m.cells) {
		c := m.cells[m.streamReasonIdx]
		if c.Kind == cellThinking && strings.TrimSpace(c.Raw) != "" && !c.Collapsed {
			return true
		}
	}
	for i := len(m.cells) - 1; i >= 0; i-- {
		if m.cells[i].Kind != cellThinking {
			continue
		}
		if strings.TrimSpace(m.cells[i].Raw) != "" && !m.cells[i].Collapsed {
			return true
		}
		return false
	}
	return false
}

func (m Model) renderFooter() string {
	posture := m.postureLabel()
	parts := []string{
		styleMuted.Render("posture " + posture),
		styleMuted.Render(shortSession(m.session)),
	}
	if summary := m.granularSummary(); summary != "inherit" {
		parts = append(parts, styleMuted.Render("granular "+summary))
	}
	if m.costGlance != "" {
		parts = append(parts, styleMuted.Render(m.costGlance))
	}
	if m.motion != MotionFull {
		parts = append(parts, styleMuted.Render("motion:"+string(m.motion)))
	}
	return strings.Join(parts, "  ·  ")
}

// renderStatusLine merges phase strip + footer facts into one chrome row.
func (m Model) renderStatusLine() string {
	phase := m.renderPhaseStrip()
	footer := m.renderFooter()
	if phase == "" && footer == "" {
		return ""
	}
	if phase == "" {
		return footer
	}
	if footer == "" {
		return phase
	}
	return phase + "  " + footer
}

func (m Model) recomputePhase() Model {
	if m.mode == ModeApprove || m.approvalCard != nil && m.approvalCard.Status == "pending" {
		m.phase = PhaseApproval
		return m
	}
	if m.mode == ModeInput {
		m.phase = PhaseWaiting
		return m
	}
	if m.busy {
		m.phase = PhaseWorking
		return m
	}
	if m.inDoneBreath() {
		m.phase = PhaseDone
		return m
	}
	text := strings.TrimSpace(m.composerText())
	if text != "" && !m.busy {
		m.phase = PhaseTyping
		return m
	}
	if m.phase == PhaseFailed || m.phase == PhaseIncomplete {
		return m
	}
	m.phase = PhaseIdle
	return m
}
