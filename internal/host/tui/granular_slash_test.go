package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fwtllh-png/CodeHelper/internal/host/tui/commands"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func TestGranularSlashUpdatesHost(t *testing.T) {
	host := &granularHost{}
	m := NewModel(Options{}, host)
	updated := m.dispatchSlash(commands.Action{
		Kind: commands.KindGranular, Args: []string{"mcp", "ask"},
	})
	if updated.granular.MCP != policy.SurfaceAsk {
		t.Fatalf("granular = %+v", updated.granular)
	}
	if host.granular.MCP != policy.SurfaceAsk {
		t.Fatalf("host granular = %+v", host.granular)
	}
	joined := updated.buildTranscriptView()
	if !strings.Contains(joined, "granular:mcp:ask") {
		t.Fatalf("transcript = %q", joined)
	}
}

func TestParseGranularCommand(t *testing.T) {
	action, ok := commands.Parse("/granular sandbox deny")
	if !ok || action.Kind != commands.KindGranular {
		t.Fatalf("action = %+v ok=%v", action, ok)
	}
	if len(action.Args) != 2 || action.Args[0] != "sandbox" || action.Args[1] != "deny" {
		t.Fatalf("args = %#v", action.Args)
	}
}

type granularHost struct {
	mode     policy.Mode
	perm     policy.Permission
	granular policy.Granular
}

func (h *granularHost) StartTurn(context.Context, string) error              { return nil }
func (h *granularHost) DecideApproval(context.Context, string, string) error { return nil }
func (h *granularHost) ReplyInput(context.Context, string, string) error     { return nil }
func (h *granularHost) Cancel(context.Context) error                         { return nil }
func (h *granularHost) Close(context.Context) error                          { return nil }
func (h *granularHost) WaitMsg() tea.Cmd                                     { return nil }
func (h *granularHost) SetPolicyMode(mode policy.Mode)                       { h.mode = mode }
func (h *granularHost) SetPermission(permission policy.Permission) {
	h.perm = permission
}
func (h *granularHost) SetGranular(granular policy.Granular) { h.granular = granular }
