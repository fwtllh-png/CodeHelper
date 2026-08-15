package tui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fwtllh-png/CodeHelper/internal/host/tui"
	"github.com/fwtllh-png/CodeHelper/internal/host/tui/commands"
)

func TestPermissionsSlashAndAlwaysApproval(t *testing.T) {
	action, ok := commands.Parse("/permissions")
	if !ok || action.Kind != commands.KindPermissions {
		t.Fatalf("permissions => %+v", action)
	}

	root := t.TempDir()
	dataDir := filepath.Join(root, ".codehelper")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	permPath := filepath.Join(dataDir, "permissions.toml")
	if err := os.WriteFile(permPath, []byte("[[deny]]\ntool = \"exec_command\"\ncommand_prefix = \"rm\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	host := &recordingHost{}
	model := tui.NewModel(tui.Options{DataDir: dataDir}, host)
	model = enterSlash(model, "/permissions")
	view := model.View()
	if !strings.Contains(view, "permissions:path=") || !strings.Contains(view, "deny=1") {
		t.Fatalf("view=%s", view)
	}

	updated, _ := model.Update(tui.StreamApprovalMessage("req-always", "exec_command rm -rf /"))
	model = updated.(tui.Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("always")})
	model = updated.(tui.Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(tui.Model)
	if len(host.approvals) != 1 || host.approvals[0] != "req-always:always" {
		t.Fatalf("approvals=%v", host.approvals)
	}
}
