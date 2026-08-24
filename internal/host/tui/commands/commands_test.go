package commands_test

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/tui/commands"
)

func TestParseCoreSlash(t *testing.T) {
	action, ok := commands.Parse("/help")
	if !ok || action.Kind != commands.KindHelp {
		t.Fatalf("%+v", action)
	}
	action, ok = commands.Parse("/fork thread-b")
	if !ok || action.Kind != commands.KindFork || len(action.Args) != 1 {
		t.Fatalf("%+v", action)
	}
	if _, ok := commands.Parse("hello"); ok {
		t.Fatal("expected non-slash reject")
	}
	if commands.HelpText() == "" {
		t.Fatal("empty help")
	}
}

func TestHelpTextOperableOnly(t *testing.T) {
	help := commands.HelpText()
	if !strings.Contains(help, "CLI-only:") {
		t.Fatalf("missing CLI-only footnote: %s", help)
	}
	main, footnote, ok := strings.Cut(help, "CLI-only:")
	if !ok {
		t.Fatal("expected CLI-only split")
	}
	for _, kind := range commands.StubKinds() {
		token := "/" + string(kind)
		if strings.Contains(main, token+" ") || strings.HasSuffix(strings.TrimSpace(main), token) || strings.Contains(main, token+" |") {
			t.Fatalf("stub %s listed in operable help: %s", token, main)
		}
		if !strings.Contains(footnote, token) {
			t.Fatalf("stub %s missing from CLI-only: %s", token, footnote)
		}
	}
	for _, token := range []string{"/redo", "/copy"} {
		if strings.Contains(help, token) {
			t.Fatalf("noop %s should not appear in help: %s", token, help)
		}
	}
	if strings.Contains(main, "/sandbox") {
		t.Fatalf("/sandbox must not be in operable list: %s", main)
	}
	for _, sample := range []string{"/sandbox", "/doctor", "/memory", "/init", "/apply"} {
		action, ok := commands.Parse(sample)
		if !ok || action.Kind != commands.KindUnknown {
			t.Fatalf("CLI-only command registered in TUI %s => %+v", sample, action)
		}
	}
	action, ok := commands.Parse("/context")
	if !ok || action.Kind != commands.KindContext {
		t.Fatalf("operable /context is not registered: %+v", action)
	}
	for _, sample := range []string{"/redo", "/copy"} {
		action, ok := commands.Parse(sample)
		if !ok || action.Kind != commands.KindUnknown {
			t.Fatalf("no-op command registered in TUI %s => %+v", sample, action)
		}
	}
}

func TestCatalogHasDepth003Kinds(t *testing.T) {
	kinds := commands.AllKinds()
	if len(kinds) < 30 {
		t.Fatalf("AllKinds=%d, want >=30", len(kinds))
	}
	required := []commands.Kind{
		commands.KindClear, commands.KindCompact, commands.KindDiff, commands.KindUndo,
		commands.KindPlugin, commands.KindSkill, commands.KindLane,
	}
	have := map[commands.Kind]bool{}
	for _, kind := range kinds {
		have[kind] = true
	}
	for _, kind := range required {
		if !have[kind] {
			t.Fatalf("missing required kind %s", kind)
		}
	}
	for _, sample := range []string{"/clear", "/compact", "/diff", "/undo", "/plugin", "/skill", "/lane"} {
		action, ok := commands.Parse(sample)
		if !ok || action.Kind == commands.KindUnknown {
			t.Fatalf("parse %s => %+v", sample, action)
		}
	}
}
