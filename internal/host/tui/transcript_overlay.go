package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

const overlayHeaderReserve = 1

func (m Model) openTranscriptOverlay() Model {
	if m.mode != ModeChat && m.mode != ModePlan {
		return m
	}
	m.ensureChrome()
	m.mode = ModeTranscript
	m = m.layoutTranscriptOverlay()
	m = m.syncOverlayContent()
	if m.overlayYOffset > 0 {
		m.transcriptVP.SetYOffset(m.overlayYOffset)
	} else {
		m.transcriptVP.GotoBottom()
	}
	return m
}

func (m Model) closeTranscriptOverlay() Model {
	if m.mode != ModeTranscript {
		return m
	}
	m.overlayYOffset = m.transcriptVP.YOffset
	m.mode = ModeChat
	return m
}

func (m Model) layoutTranscriptOverlay() Model {
	width := m.width
	if width <= 0 {
		width = 80
	}
	height := m.height
	if height <= 0 {
		height = 24
	}
	bodyH := height - overlayHeaderReserve
	if bodyH < 3 {
		bodyH = 3
	}
	if m.transcriptVP.Width == 0 {
		m.transcriptVP = newTranscriptViewport(width, bodyH)
	} else {
		m.transcriptVP.Width = width
		m.transcriptVP.Height = bodyH
	}
	return m
}

func (m Model) syncOverlayContent() Model {
	m = m.layoutTranscriptOverlay()
	content := m.buildOverlayTranscript()
	m.transcriptVP.SetContent(content)
	m.transcriptVP.SetYOffset(m.transcriptVP.YOffset)
	return m
}

func (m Model) scrollOverlay(mutate func(*viewport.Model)) Model {
	m = m.syncOverlayContent()
	mutate(&m.transcriptVP)
	m.overlayYOffset = m.transcriptVP.YOffset
	return m
}

func (m Model) buildOverlayTranscript() string {
	var b strings.Builder
	if m.showWelcome && len(m.cells) == 0 {
		for _, line := range welcomeLines(m.provider, m.modelID, m.workspaceRoot) {
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	prevKind := transcriptCellKind("")
	for _, cell := range m.cells {
		if b.Len() > 0 && needsTranscriptGap(prevKind, cell.Kind) {
			b.WriteByte('\n')
		}
		b.WriteString(m.renderCellOverlay(cell))
		b.WriteByte('\n')
		prevKind = cell.Kind
	}
	if m.planCard != nil && strings.TrimSpace(m.planCard.Body) != "" {
		b.WriteString(styleBrand.Render(m.planCard.Render()))
		b.WriteByte('\n')
	}
	base := strings.TrimRight(b.String(), "\n")
	return composeViewportContent(base, m.liveSuffix())
}

// renderCellOverlay is like renderCell but expands thinking fully and shows
// uncapped tool Detail for the transcript pager.
func (m Model) renderCellOverlay(cell transcriptCell) string {
	if cell.Kind == cellThinking && strings.TrimSpace(cell.Raw) != "" {
		return styleThink.Render("thinking: " + cell.Raw)
	}
	if cell.Kind == cellTool || cell.Kind == cellToolRun || cell.Kind == cellError {
		style := styleToolDone
		if cell.Kind == cellError {
			style = styleErr
		} else if cell.Live {
			style = styleToolLive
		}
		line := style.Render(cell.Raw)
		if cell.Expanded && cell.Detail != "" {
			line += "\n" + styleMuted.Render(cell.Detail)
		}
		return line
	}
	return m.renderCell(cell)
}

func (m Model) renderTranscriptOverlayView() string {
	m.ensureChrome()
	m = m.layoutTranscriptOverlay()
	header := styleBrand.Render("transcript") + styleMuted.Render("  Esc/Ctrl+T close · ↑↓/PgUp · v expand")
	body := padBlockHeight(m.transcriptVP.View(), m.transcriptVP.Height)
	return ensureTrailingNewline(header + "\n" + body)
}

func (m Model) handleTranscriptOverlayKey(msg tea.KeyMsg) (Model, bool, tea.Cmd) {
	if m.mode != ModeTranscript {
		return m, false, nil
	}
	switch msg.Type {
	case tea.KeyCtrlT, tea.KeyEsc, tea.KeyCtrlC:
		return m.closeTranscriptOverlay(), true, nil
	case tea.KeyPgUp:
		return m.scrollOverlay(func(vp *viewport.Model) { vp.PageUp() }), true, nil
	case tea.KeyPgDown:
		return m.scrollOverlay(func(vp *viewport.Model) { vp.PageDown() }), true, nil
	case tea.KeyCtrlU:
		return m.scrollOverlay(func(vp *viewport.Model) { vp.HalfPageUp() }), true, nil
	case tea.KeyCtrlD:
		return m.scrollOverlay(func(vp *viewport.Model) { vp.HalfPageDown() }), true, nil
	case tea.KeyHome:
		return m.scrollOverlay(func(vp *viewport.Model) { vp.GotoTop() }), true, nil
	case tea.KeyEnd:
		return m.scrollOverlay(func(vp *viewport.Model) { vp.GotoBottom() }), true, nil
	case tea.KeyUp:
		return m.scrollOverlay(func(vp *viewport.Model) { vp.ScrollUp(3) }), true, nil
	case tea.KeyDown:
		return m.scrollOverlay(func(vp *viewport.Model) { vp.ScrollDown(3) }), true, nil
	case tea.KeyRunes:
		if len(msg.Runes) == 1 && strings.ToLower(string(msg.Runes)) == "v" {
			m = m.toggleToolExpand()
			m = m.syncOverlayContent()
			return m, true, nil
		}
		return m, true, nil // swallow other typing while overlay open
	}
	return m, true, nil
}
