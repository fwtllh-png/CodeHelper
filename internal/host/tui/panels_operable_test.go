package tui_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fwtllh-png/CodeHelper/internal/host/tui"
	"github.com/fwtllh-png/CodeHelper/internal/host/tui/commands"
)

func TestPanelsOperableCopy(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, ".codehelper")
	fleetRoot := filepath.Join(dataDir, "fleet")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	mcpPath := filepath.Join(dataDir, "mcp.json")
	if err := os.WriteFile(mcpPath, []byte(`{"version":1,"servers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	model := tui.NewModel(tui.Options{DataDir: dataDir, FleetRoot: fleetRoot, MCPConfig: mcpPath}, &recordingHost{})

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1"), Alt: true})
	model = updated.(tui.Model)
	if !strings.Contains(model.View(), "mcp:") || !strings.Contains(model.View(), "error") {
		t.Fatalf("mcp empty error missing: %q", model.View())
	}

	seed := map[string]any{
		"version": 1,
		"servers": map[string]any{
			"local": map[string]any{
				"transport": "stdio", "command": "echo",
				"tools": map[string]any{
					"default": map[string]any{
						"capability": "read", "access_mode": "read",
						"parallel_policy": "serial", "sandbox_requirement": "none",
					},
				},
			},
		},
	}
	data, _ := json.Marshal(seed)
	if err := os.WriteFile(mcpPath, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(tui.Model)
	if !strings.Contains(model.View(), "mcp:reconnect ok") {
		t.Fatalf("mcp reconnect missing: %q", model.View())
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2"), Alt: true})
	model = updated.(tui.Model)
	if !strings.Contains(model.View(), "fleet:") ||
		!strings.Contains(model.View(), "readonly audit trail") {
		t.Fatalf("fleet operable copy missing: %q", model.View())
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3"), Alt: true})
	model = updated.(tui.Model)
	if !strings.Contains(model.View(), "workflow:") || !strings.Contains(model.View(), "readonly") {
		t.Fatalf("workflow copy missing: %q", model.View())
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4"), Alt: true})
	model = updated.(tui.Model)
	if !strings.Contains(model.View(), "settings:") || !strings.Contains(model.View(), "readonly") {
		t.Fatalf("settings readonly missing: %q", model.View())
	}
}

func TestAttachAndEmptyStates(t *testing.T) {
	action, ok := commands.Parse("/attach shot.png")
	if !ok || action.Kind != commands.KindAttach {
		t.Fatalf("attach => %+v", action)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "shot.png"), []byte("PNG"), 0o600); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, ".codehelper")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	model := tui.NewModel(tui.Options{DataDir: dataDir, Workspace: root}, &recordingHost{})
	model = enterSlash(model, "/attach shot.png")
	if !strings.Contains(model.View(), "attach:ok path=shot.png") {
		t.Fatalf("attach missing: %q", model.View())
	}
	model = enterSlash(model, "/task")
	if !strings.Contains(model.View(), "task:empty") {
		t.Fatalf("task empty missing: %q", model.View())
	}
	model = enterSlash(model, "/automation")
	if !strings.Contains(model.View(), "automation:empty") {
		t.Fatalf("automation empty missing: %q", model.View())
	}
}
