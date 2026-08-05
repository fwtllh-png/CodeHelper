package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTranscriptScrollKeepsPriorPages(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m.width, m.height = 80, 24
	m.ready = true
	m.showWelcome = false
	for i := 0; i < 40; i++ {
		m = m.appendCell(cellYou, strings.Repeat("line-", 1)+strings.Repeat("x", i%7))
	}
	m = m.refreshViewport(true)
	if m.viewport.TotalLineCount() < m.viewport.Height {
		t.Fatalf("expected multi-page transcript, lines=%d height=%d", m.viewport.TotalLineCount(), m.viewport.Height)
	}
	bottom := m.viewport.YOffset
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = updated.(Model)
	if m.followTail {
		t.Fatal("PgUp should leave followTail")
	}
	if m.viewport.YOffset >= bottom {
		t.Fatalf("PgUp did not scroll up: before=%d after=%d", bottom, m.viewport.YOffset)
	}
	view := m.View()
	if !strings.Contains(view, "you:") {
		t.Fatalf("scrolled view missing transcript: %q", view)
	}
	if strings.Contains(view, "./bin/codehelper") {
		t.Fatalf("launch command leaked into view: %q", view)
	}
}

func TestRefreshViewportFollowsTailWhenRequested(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m.width, m.height = 80, 20
	m.ready = true
	m.showWelcome = false
	for i := 0; i < 30; i++ {
		m = m.appendCell(cellAssistant, "chunk")
	}
	m.followTail = false
	m = m.refreshViewport(false)
	m.viewport.GotoTop()
	m.followTail = true
	m = m.refreshViewport(false)
	if !m.viewport.AtBottom() {
		t.Fatalf("followTail refresh should pin bottom, Y=%d", m.viewport.YOffset)
	}
}
