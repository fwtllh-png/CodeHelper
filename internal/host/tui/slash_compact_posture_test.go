package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fwtllh-png/CodeHelper/internal/host/tui"
	"github.com/fwtllh-png/CodeHelper/internal/host/tui/commands"
)

func TestSlashCompactDiffStatus(t *testing.T) {
	root := t.TempDir()
	model := tui.NewModel(tui.Options{DataDir: root}, nil)
	model = enterSlash(model, "/compact")
	if !strings.Contains(model.View(), "compact:ok") {
		t.Fatalf("compact missing: %q", model.View())
	}
	model = enterSlash(model, "/diff")
	if !strings.Contains(model.View(), "diff:") {
		t.Fatalf("diff missing: %q", model.View())
	}
	model = enterSlash(model, "/status")
	if !strings.Contains(model.View(), "status:") {
		t.Fatalf("status missing: %q", model.View())
	}
	model = enterSlash(model, "/undo")
	if !strings.Contains(model.View(), "undo:") {
		t.Fatalf("undo missing: %q", model.View())
	}
}

func TestPostureCycleAndAgent(t *testing.T) {
	root := t.TempDir()
	model := tui.NewModel(tui.Options{DataDir: root}, nil)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	model = updated.(tui.Model)
	if !strings.Contains(model.View(), "posture:") {
		t.Fatalf("posture cycle missing: %q", model.View())
	}
	// /agent observes child agents. It used to fork a thread on the parent runtime
	// and call that a child agent, which is why a prompt argument now gets told
	// where spawning actually lives instead of quietly starting a turn.
	model = enterSlash(model, "/agent do work")
	view := model.View()
	if !strings.Contains(view, "panel:agents") {
		t.Fatalf("agents panel missing: %q", view)
	}
	if !strings.Contains(view, "spawning is the model's agent tool") {
		t.Fatalf("prompt argument was accepted silently: %q", view)
	}
	if strings.Contains(view, "agent:start") {
		t.Fatalf("/agent must not start a turn: %q", view)
	}
	_ = commands.KindAgent
}

func enterSlash(model tui.Model, line string) tui.Model {
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(line)})
	model = updated.(tui.Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return updated.(tui.Model)
}
