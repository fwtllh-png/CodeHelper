package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestMCPHealthEventUpdatesPanel(t *testing.T) {
	changedAt := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	message := mapRuntimeEvent(protocol.Event{
		Kind: protocol.EventMCPHealthChanged,
		Data: &protocol.MCPHealthChangedData{
			Server: "remote", PreviousState: "degraded", State: "open",
			ConsecutiveFailures: 3, LastError: "timeout", ChangedAt: changedAt,
		},
	})
	stream, ok := message.(streamMsg)
	if !ok || stream.mcpHealth == nil {
		t.Fatalf("message = %#v", message)
	}
	configPath := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(configPath, []byte(`{
		"version": 1,
		"servers": {
			"remote": {
				"transport": "stdio",
				"command": "fixture",
				"tools": {
					"echo": {
						"capability": "read",
						"access_mode": "read",
						"parallel_policy": "concurrent",
						"sandbox_requirement": "none"
					}
				}
			}
		}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	model := Model{
		mcpConfig: configPath,
		mcpHealth: map[string]protocol.MCPHealthChangedData{
			stream.mcpHealth.Server: *stream.mcpHealth,
		},
	}
	panel := model.renderPanel(PanelMCP)
	if !strings.Contains(panel, "remote:open(3)") {
		t.Fatalf("panel = %q", panel)
	}
}
