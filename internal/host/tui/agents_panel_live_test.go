package tui_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/host/tui"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
)

// The agents panel has to read the child-agent manager, which is the only thing
// that knows a child exists. The old /agent path drew a card from a forked parent
// thread instead, so the panel could show an agent that was never spawned and
// miss every agent that was.
func TestAgentsPanelShowsWhatTheSubagentManagerHas(t *testing.T) {
	workspace := t.TempDir()
	tools := true
	session, err := wire.NewExec(context.Background(), wire.ExecOptions{
		FixturePath: filepath.Join("..", "..", "..", "testdata", "providers", "openai"),
		Permission:  "bypass",
		ConfigOverrides: config.Overrides{
			Workspace: &workspace, Tools: &tools,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	host, err := tui.NewSessionHost(session)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = host.Close(ctx)
	})

	manager := session.Subagents()
	if manager == nil {
		t.Fatal("a session with tools must expose its subagent manager")
	}
	model := tui.NewModel(tui.Options{Workspace: workspace}, host)

	// Nothing spawned yet: the panel says none rather than unavailable, because the
	// manager exists and has an answer.
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("5"), Alt: true})
	model = updated.(tui.Model)
	if !strings.Contains(model.View(), "agents: none") {
		t.Fatalf("empty agents panel = %q", model.View())
	}

	child, err := manager.Spawn("", subagent.RoleExplore, "read the tree")
	if err != nil {
		t.Fatal(err)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(tui.Model)
	view := model.View()
	if !strings.Contains(view, "count=1") || !strings.Contains(view, child.ID) {
		t.Fatalf("agents panel = %q, want the spawned child", view)
	}
	// Role, stance and isolation are what decide whether a child may write, so the
	// row carries them rather than just an id.
	if !strings.Contains(view, string(child.Role)) || !strings.Contains(view, string(child.Stance)) {
		t.Fatalf("agents panel = %q, want role and stance", view)
	}
	if !strings.Contains(view, string(subagent.StatusPendingInit)) {
		t.Fatalf("agents panel = %q, want the child's real status", view)
	}

	// A closed child stays visible: "where did my agent go" is exactly the question
	// this panel is for.
	if err := manager.Close(child.ID); err != nil {
		t.Fatal(err)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(tui.Model)
	if !strings.Contains(model.View(), child.ID) {
		t.Fatalf("agents panel dropped a closed child: %q", model.View())
	}
}
