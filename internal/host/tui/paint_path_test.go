package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestViewDoesNotRebuildWithoutRefresh(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m.width, m.height = 80, 24
	m.ready = true
	m.showWelcome = false
	m = m.appendCell(cellAssistant, "paint-old-marker")
	m = m.refreshViewport(true)
	if !strings.Contains(m.View(), "paint-old-marker") {
		t.Fatalf("expected refreshed content in View: %q", m.View())
	}

	m = m.appendCell(cellAssistant, "paint-new-marker")
	view := m.View()
	if strings.Contains(view, "paint-new-marker") {
		t.Fatalf("View must not rebuild from cells without refresh: %q", view)
	}
	if !strings.Contains(view, "paint-old-marker") {
		t.Fatalf("View should still show last refreshed content: %q", view)
	}

	m = m.refreshViewport(false)
	if !strings.Contains(m.View(), "paint-new-marker") {
		t.Fatalf("after refresh View should show new content: %q", m.View())
	}
}

func TestSpinnerTickPreservesScrollOffset(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m.width, m.height = 80, 20
	m.ready = true
	m.showWelcome = false
	m.motion = MotionFull
	for i := 0; i < 40; i++ {
		m = m.appendCell(cellYou, "scroll-line-"+strings.Repeat("x", i%5))
	}
	m = m.refreshViewport(true)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = updated.(Model)
	if m.followTail {
		t.Fatal("expected followTail false after PgUp")
	}
	m = m.upsertActiveTool(ToolCard{ID: "t1", Name: "read_file", Status: "running", Detail: "path=a.go"})
	m = m.refreshViewport(false)
	if m.followTail {
		t.Fatal("refresh without forceFollow should keep followTail false")
	}
	before := m.viewport.YOffset
	updated, _ = m.Update(tickMsg(time.Now()))
	m = updated.(Model)
	if m.viewport.YOffset != before {
		t.Fatalf("spinner tick moved scroll: before=%d after=%d", before, m.viewport.YOffset)
	}
	if m.followTail {
		t.Fatal("spinner tick should not re-attach followTail")
	}
}

func TestRequestPaintCoalescesWithinWindow(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m.width, m.height = 80, 24
	m.ready = true
	m.showWelcome = false
	m = m.appendCell(cellAssistant, "coalesce-alpha")
	m = m.requestPaint(true)
	if !strings.Contains(m.viewport.View(), "coalesce-alpha") {
		t.Fatalf("first paint should apply: %q", m.viewport.View())
	}
	if m.viewportDirty {
		t.Fatal("first paint should clear dirty")
	}

	m = m.appendCell(cellAssistant, "coalesce-beta")
	m = m.requestPaint(false)
	if !m.viewportDirty {
		t.Fatal("second paint within coalesce window should only mark dirty")
	}
	if strings.Contains(m.viewport.View(), "coalesce-beta") {
		t.Fatalf("coalesced paint must not flush early: %q", m.viewport.View())
	}

	m = m.flushViewportIfDirty()
	if m.viewportDirty {
		t.Fatal("flush should clear dirty")
	}
	view := m.viewport.View()
	if !strings.Contains(view, "coalesce-beta") {
		t.Fatalf("flush should show latest content: %q", view)
	}
	if !strings.Contains(view, "coalesce-alpha") {
		t.Fatalf("flush should retain prior content: %q", view)
	}
}

func TestRequestPaintPendingFollowOnFlush(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m.width, m.height = 80, 20
	m.ready = true
	m.showWelcome = false
	for i := 0; i < 30; i++ {
		m = m.appendCell(cellAssistant, "chunk")
	}
	m = m.requestPaint(true)
	m.viewport.GotoTop()
	m.followTail = false

	m = m.appendCell(cellAssistant, "follow-tail-marker")
	m = m.requestPaint(true)
	if !m.viewportDirty || !m.pendingFollow {
		t.Fatalf("expected dirty+pendingFollow, dirty=%v pending=%v", m.viewportDirty, m.pendingFollow)
	}
	m = m.flushViewportIfDirty()
	if !m.followTail || !m.viewport.AtBottom() {
		t.Fatalf("flush with pendingFollow should pin bottom, follow=%v atBottom=%v", m.followTail, m.viewport.AtBottom())
	}
	if !strings.Contains(m.viewport.View(), "follow-tail-marker") {
		t.Fatalf("missing marker after flush: %q", m.viewport.View())
	}
}
