package wire

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

func TestMCPContributorDefersAdapterUntilBackgroundRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(
		path,
		[]byte(`{
			"version": 1,
			"servers": {
				"deferred": {
					"transport": "stdio",
					"command": "/bin/false",
					"tools": {
						"deferred.echo": {
							"capability": "read",
							"access_mode": "read",
							"parallel_policy": "concurrent",
							"sandbox_requirement": "none"
						}
					}
				}
			}
		}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry(nil, nil)
	pool, prewarm, err := RegisterMCPTools(t.Context(), registry, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		prewarm.Stop()
		_ = pool.ShutdownAll(t.Context())
	})
	if prewarm.adapter != nil || prewarm.cancel != nil {
		t.Fatal("MCP contributor started background adapter work")
	}
}
