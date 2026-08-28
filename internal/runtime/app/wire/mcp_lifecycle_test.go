package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
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
					"host_trusted": true,
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
	config, err := mcpruntime.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	runtimeAuthority, err := mcpruntime.NewRuntimeAuthority(
		t.TempDir(), "", 1, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	pool, prewarm, err := RegisterMCPConfig(
		registry,
		config,
		runtimeAuthority,
	)
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

func TestMCPContributorRequiresConfigInsideTrustedStateRoot(t *testing.T) {
	trustedRoot := t.TempDir()
	outside := filepath.Join(t.TempDir(), "mcp.json")
	data := []byte(`{
		"version": 1,
		"servers": {
			"deferred": {
				"transport": "stdio",
				"host_trusted": true,
				"command": "/bin/false",
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
	}`)
	if err := os.WriteFile(outside, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := (mcpContributor{
		configPath: outside, trustedConfigRoot: trustedRoot,
		output: &extensionBuildState{},
	}).Contribute(t.Context(), tool.NewRegistry(nil, nil))
	if err == nil || !strings.Contains(err.Error(), "Runtime state directory") {
		t.Fatalf("outside MCP config error = %v", err)
	}

	inside := filepath.Join(trustedRoot, "mcp.json")
	if err := os.WriteFile(inside, data, 0o600); err != nil {
		t.Fatal(err)
	}
	runtimeAuthority, err := mcpruntime.NewRuntimeAuthority(
		t.TempDir(), "", 1, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	output := &extensionBuildState{}
	if _, err := (mcpContributor{
		configPath: inside, trustedConfigRoot: trustedRoot,
		runtimeAuthority: runtimeAuthority, output: output,
	}).Contribute(t.Context(), tool.NewRegistry(nil, nil)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if output.mcpPrewarm != nil {
			output.mcpPrewarm.Stop()
		}
		if output.mcpPool != nil {
			_ = output.mcpPool.ShutdownAll(t.Context())
		}
	})
}

func TestMCPContributorRejectsSymlinkedTrustedConfig(t *testing.T) {
	trustedRoot := t.TempDir()
	outside := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(trustedRoot, "mcp.json")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := (mcpContributor{
		configPath: link, trustedConfigRoot: trustedRoot,
		output: &extensionBuildState{},
	}).Contribute(t.Context(), tool.NewRegistry(nil, nil))
	if err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlinked MCP config error = %v", err)
	}
}
