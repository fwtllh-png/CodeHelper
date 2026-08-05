package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestSelectModeToggleReleasesMouse covers Option B: Ctrl+S releases the
// terminal mouse so the user can select/copy text natively, and toggles back
// to re-acquire it for normal TUI interaction.
func TestSelectModeToggleReleasesMouse(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	if !m.mouseCapture {
		t.Fatal("default model should have mouse capture enabled")
	}

	// Enter select mode: mouse released to the terminal.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m2, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", updated)
	}
	if !m2.selectMode {
		t.Fatal("Ctrl+S should enable select mode")
	}
	if cmd == nil {
		t.Fatal("entering select mode should emit a mouse command")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("mouse release command should produce a message")
	}

	// Exit select mode: mouse re-acquired by the app.
	updated, cmd = m2.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m3, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", updated)
	}
	if m3.selectMode {
		t.Fatal("second Ctrl+S should exit select mode")
	}
	if cmd == nil {
		t.Fatal("exiting select mode should emit a mouse command")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("mouse re-acquire command should produce a message")
	}
}

// TestSelectModeNoopWithoutMouseCapture: when the program runs without
// alt-screen mouse capture, native selection already works and Ctrl+S must be
// a no-op (it must not emit an EnableMouseCellMotion that would break it).
func TestSelectModeNoopWithoutMouseCapture(t *testing.T) {
	m := NewModel(Options{DisableAltScreen: true}, &fakeRuntime{})
	if m.mouseCapture {
		t.Fatal("DisableAltScreen model should not have mouse capture")
	}
	before := len(m.cells)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m2, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", updated)
	}
	if m2.selectMode {
		t.Fatal("select mode must not engage without mouse capture")
	}
	if cmd != nil {
		t.Fatal("no mouse command should be emitted without mouse capture")
	}
	if got := len(m2.cells); got != before+1 {
		t.Fatalf("expected one status note, got %d cells (was %d)", got, before)
	}
}
