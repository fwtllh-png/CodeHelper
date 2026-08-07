package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Visual goldens: fixed width + MotionStill, ANSI stripped.
// Refresh with: UPDATE_GOLDEN=1 go test ./internal/host/tui/ -run VisualSnapshot -count=1

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func normalizeView(s string) string {
	s = ansiEscape.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimRight(s, "\n") + "\n"
	return s
}

func goldenPath(name string) string {
	return filepath.Join("testdata", "visual", name+".golden")
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	got = normalizeView(got)
	path := goldenPath(name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run with UPDATE_GOLDEN=1 to create): %v", path, err)
	}
	if got != string(want) {
		t.Fatalf("golden mismatch %s\n--- got ---\n%s--- want ---\n%s", name, got, want)
	}
}

func TestVisualSnapshotTranscript(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m.width, m.height = 80, 24
	m.ready = true
	m.showWelcome = false
	m.motion = MotionStill
	m.viewport.Width = 80
	m = m.appendCell(cellYou, "list the files")
	m = m.appendCell(cellAssistant, "Here are the files:\n- wrap.go\n- app.go")
	m.cells[len(m.cells)-1].Rendered = "assistant\nHere are the files:\n- wrap.go\n- app.go"
	m.cells[len(m.cells)-1].CacheWidth = 80
	m = m.noteStatus("status:ok")
	m.settledTools = []ToolCard{{
		ID: "t1", Name: "read_file", Status: "done", Detail: "path=wrap.go",
	}}
	m = m.rebuildToolCells()
	m = m.refreshViewport(true)
	assertGolden(t, "transcript", m.buildTranscriptView())
}

func TestVisualSnapshotOverlay(t *testing.T) {
	m := NewModel(Options{}, &fakeRuntime{})
	m.width, m.height = 80, 24
	m.ready = true
	m.showWelcome = false
	m.motion = MotionStill
	m = m.appendCell(cellYou, "hello")
	m = m.appendCell(cellAssistant, "world")
	m.cells[len(m.cells)-1].Rendered = "assistant\nworld"
	m.cells[len(m.cells)-1].CacheWidth = 80
	m = m.openTranscriptOverlay()
	assertGolden(t, "overlay", m.buildOverlayTranscript())
}

func TestVisualSnapshotExperienceWidths(t *testing.T) {
	for _, width := range []int{80, 120, 160} {
		t.Run(fmt.Sprintf("%d", width), func(t *testing.T) {
			m := NewModel(Options{
				Provider: "openai", Model: "gpt-4.1", Workspace: "/workspace",
			}, &fakeRuntime{})
			updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
			m = updated.(Model)
			m.showWelcome = false
			m.motion = MotionStill
			m = m.appendCell(
				cellYou,
				"inspect the current implementation, propose one guarded edit, and verify it",
			)
			m = m.appendCell(
				cellAssistant,
				"Inspection is complete. The edit remains bound to the displayed plan.",
			)
			m.settledTools = []ToolCard{
				{ID: "read", Name: "file_read", Status: "done", Detail: "path=internal/host/tui/view.go"},
				{ID: "verify", Name: "verify", Status: "failed", Detail: "error: focused test failed"},
			}
			m = m.rebuildToolCells()
			m.approvalCard = &ApprovalCard{
				ID:      "approval_1",
				Message: "file_edit · internal/host/tui/view.go",
				Status:  "pending",
				Kind:    approvalKindPatch,
				Preview: "--- view.go\n+++ view.go\n@@\n-old\n+new",
			}
			m.mode = ModeApprove
			m = m.refreshViewport(true)
			assertGolden(t, fmt.Sprintf("experience-%d", width), m.View())
		})
	}
}
