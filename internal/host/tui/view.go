package tui

import (
	"strings"
)

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	m.ensureChrome()
	m = m.recomputePhase()
	if m.mode == ModeTranscript {
		return m.renderTranscriptOverlayView()
	}
	body := ""
	bodyHeight := 0
	if m.ready {

		m.layoutChrome()
		body = m.viewport.View()
		bodyHeight = m.viewport.Height
		body = padBlockHeight(body, bodyHeight)
	} else {
		body = m.buildTranscriptView()
	}
	header := m.renderHeader()
	scrollHint := ""
	if m.ready && !m.followTail {
		scrollHint = styleMuted.Render("  ↑↓/PgUp · End↓ · Ctrl+T")
	}
	sidebar := ""
	if len(m.sidebarTasks) > 0 {
		sidebar = "\n" + styleSidebar.Render(strings.Join(m.sidebarTasks, "\n"))
	}
	extra := ""
	pane := m.bottomPane()
	if m.height > 0 && m.height < 12 {
		compact := BottomPane{Width: pane.Width, Status: pane.Status, Composer: pane.Composer}
		return ensureTrailingNewline(header + scrollHint + "\n" + compact.View())
	}
	var b strings.Builder
	b.WriteString(header)
	if scrollHint != "" {
		b.WriteString(scrollHint)
	}
	b.WriteByte('\n')
	b.WriteString(body)
	if sidebar != "" {
		b.WriteString(sidebar)
	}
	if extra != "" {
		b.WriteString(extra)
	}
	b.WriteByte('\n')
	b.WriteString(pane.View())
	out := ensureTrailingNewline(b.String())

	if m.height > 0 {
		lines := strings.Count(out, "\n")
		if lines < m.height {
			out += strings.Repeat("\n", m.height-lines)
		}
	}
	return out
}

func padBlockHeight(block string, height int) string {
	if height <= 0 {
		return block
	}
	block = strings.TrimRight(block, "\n")
	n := 0
	if block != "" {
		n = strings.Count(block, "\n") + 1
	}
	if n >= height {
		return block
	}
	if block == "" {
		return strings.Repeat("\n", height-1)
	}
	return block + strings.Repeat("\n", height-n)
}

func ensureTrailingNewline(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
