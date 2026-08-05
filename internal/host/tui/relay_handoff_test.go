package tui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/tui"
	"github.com/fwtllh-png/CodeHelper/internal/host/tui/commands"
)

func TestRelayAliasesAndHandoff(t *testing.T) {
	action, ok := commands.Parse("/relay finish migration")
	if !ok || action.Kind != commands.KindRelay {
		t.Fatalf("relay => %+v", action)
	}

	dir := t.TempDir()
	host := &recordingHost{}
	model := tui.NewModel(tui.Options{DataDir: dir}, host)
	model.SetLastPlan("<plan title=\"P32\">\nobjective: ship relay\n1. write handoff\n</plan>")
	model = enterSlash(model, "/relay finish migration")
	view := model.View()
	if !strings.Contains(view, "relay:wrote ") {
		t.Fatalf("view=%s", view)
	}
	path := filepath.Join(dir, "handoff.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "# Session relay") ||
		!strings.Contains(text, "finish migration") ||
		!strings.Contains(text, "objective: ship relay") {
		t.Fatalf("handoff=%s", text)
	}

	model = enterSlash(model, "/new")
	view = model.View()
	if !strings.Contains(view, "relay:loaded") || !strings.Contains(strings.ReplaceAll(view, "\n", ""), path) {
		t.Fatalf("new session missing relay note: %s", view)
	}

	model = enterSlash(model, "/relay continue")
	if !strings.Contains(model.View(), "relay:wrote ") {
		t.Fatalf("relay continue view=%s", model.View())
	}
}
