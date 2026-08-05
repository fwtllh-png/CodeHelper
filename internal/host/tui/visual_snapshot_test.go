package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
