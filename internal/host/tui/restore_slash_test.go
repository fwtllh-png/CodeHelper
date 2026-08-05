package tui_test

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/tui/commands"
)

func TestRestoreSlash(t *testing.T) {
	action, ok := commands.Parse("/restore")
	if !ok || action.Kind != commands.KindRestore {
		t.Fatalf("restore => %+v ok=%v", action, ok)
	}
	if !strings.Contains(commands.HelpText(), "/restore") {
		t.Fatalf("help missing /restore: %s", commands.HelpText())
	}
}
