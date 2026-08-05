package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTranscriptOverlayOpenClose(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m.width, m.height = 80, 24
	m.ready = true
	m.showWelcome = false
	m = m.appendCell(cellYou, "hello-history")
	m = m.refreshViewport(true)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	m = updated.(Model)
	if m.mode != ModeTranscript {
		t.Fatalf("Ctrl+T should open overlay, mode=%s", m.mode)
	}
	view := m.View()
	if !strings.Contains(view, "transcript") {
		t.Fatalf("overlay missing header: %q", view)
	}
	if !strings.Contains(view, "hello-history") {
		t.Fatalf("overlay missing history: %q", view)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	m = updated.(Model)
	if m.mode != ModeChat {
		t.Fatalf("Ctrl+T should close overlay, mode=%s", m.mode)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.mode != ModeChat {
		t.Fatalf("Esc should close overlay, mode=%s", m.mode)
	}
}

func TestTranscriptOverlayExpandUncappedDetail(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m.width, m.height = 80, 24
	m.ready = true
	m.showWelcome = false
	detail := strings.Repeat("D", 2500)
	m.settledTools = []ToolCard{{
		ID: "t1", Name: "read_file", Status: "done", Detail: detail,
	}}
	m = m.rebuildToolCells()
	m = m.refreshViewport(true)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = updated.(Model)
	content := m.buildOverlayTranscript()
	if !strings.Contains(content, detail) {
		t.Fatalf("overlay content should include uncapped detail len=%d contentlen=%d expanded=%v",
			len(detail), len(content), m.expandedToolID)
	}
	// Main path still caps at 2000 in renderCell.
	main := m.renderCell(m.cells[len(m.cells)-1])
	if strings.Contains(main, detail) {
		t.Fatal("main transcript path should still truncate Detail")
	}
}

func TestResizeCacheHitSameWidth(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m.width, m.height = 80, 24
	m.ready = true
	m.showWelcome = false
	m.viewport.Width = 80
	m = m.appendCell(cellAssistant, "## Title\n\nbody text")
	m = m.renderAssistantMarkdown()
	if len(m.cells) == 0 || m.cells[0].Rendered == "" {
		t.Fatal("expected rendered assistant")
	}
	before := m.cells[0].Rendered
	width := m.cells[0].CacheWidth
	if width == 0 {
		t.Fatal("expected CacheWidth set")
	}
	m = m.invalidateAssistantMarkdown()
	if m.cells[0].Rendered != before {
		t.Fatal("same-width invalidate should keep Rendered cache")
	}
	if m.cells[0].CacheWidth != width {
		t.Fatalf("CacheWidth changed: %d -> %d", width, m.cells[0].CacheWidth)
	}
}

func TestResizeCacheMissOnWidthChange(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m.width, m.height = 80, 24
	m.ready = true
	m.showWelcome = false
	m.viewport.Width = 80
	m = m.appendCell(cellAssistant, "## Wide\n\n"+strings.Repeat("word ", 40))
	m = m.renderAssistantMarkdown()
	before := m.cells[0].Rendered
	beforeW := m.cells[0].CacheWidth

	m.width = 40
	m.viewport.Width = 40
	m = m.invalidateAssistantMarkdown()
	if m.cells[0].CacheWidth == beforeW {
		t.Fatal("expected CacheWidth to update after narrow resize")
	}
	if m.cells[0].Rendered == "" {
		t.Fatal("expected re-rendered content")
	}
	if m.cells[0].Rendered == before && beforeW != m.cells[0].CacheWidth {
		// Content may coincidentally match for short docs; CacheWidth is the contract.
		t.Log("rendered text identical but width cache updated")
	}
}

func TestResizeDebounceSettle(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m.ready = true
	m.width, m.height = 80, 24
	updated, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected debounce tick cmd")
	}
	// Simulate settle with matching seq.
	msg := resizeSettleMsg{seq: m.resizeSeq, width: 100, height: 30}
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if m.width != 100 || m.height != 30 {
		t.Fatalf("settle should apply size, got %dx%d", m.width, m.height)
	}
	// Stale settle ignored.
	updated, _ = m.Update(resizeSettleMsg{seq: m.resizeSeq - 1, width: 50, height: 10})
	m = updated.(Model)
	if m.width != 100 {
		t.Fatalf("stale settle must not shrink width, got %d", m.width)
	}
}
