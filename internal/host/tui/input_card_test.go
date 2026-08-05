package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestInputCardStructuredOptions(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	updated, _ := m.Update(StreamInputMessage("req-1", "pick branch", "main", "dev"))
	model := updated.(Model)
	if model.inputCard == nil || len(model.inputCard.Options) != 2 {
		t.Fatalf("inputCard = %+v", model.inputCard)
	}
	view := model.inputCard.Render()
	if !strings.Contains(view, "1. main") || !strings.Contains(view, "2. dev") {
		t.Fatalf("render missing options: %q", view)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if model.inputCard.Selected != 1 {
		t.Fatalf("selected = %d", model.inputCard.Selected)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if model.inputCard != nil {
		t.Fatal("input card should clear after enter")
	}
	joined := model.buildTranscriptView()
	if !strings.Contains(joined, "input:dev") {
		t.Fatalf("transcript = %q", joined)
	}
}
