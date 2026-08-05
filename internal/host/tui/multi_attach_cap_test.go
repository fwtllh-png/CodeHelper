package tui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/tui"
)

func TestMultiAttachCap(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.png", "b.png", "c.png", "d.png"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("PNG"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	model := tui.NewModel(tui.Options{Workspace: root}, &recordingHost{})
	model = enterSlash(model, "/attach a.png b.png")
	if !strings.Contains(model.View(), "count=2/3") {
		t.Fatalf("multi attach missing: %q", model.View())
	}
	model = enterSlash(model, "/attach c.png")
	if !strings.Contains(model.View(), "count=3/3") {
		t.Fatalf("third attach missing: %q", model.View())
	}
	model = enterSlash(model, "/attach d.png")
	if !strings.Contains(model.View(), "attach:error: at most 3") {
		t.Fatalf("cap missing: %q", model.View())
	}
}
