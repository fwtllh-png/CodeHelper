package mcp

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVersionedConfigStrictValidationAndNaming(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	data := `{
		"version": 1,
		"servers": {
			"Fixture Server": {
				"transport": "stdio",
				"command": "fixture",
				"connect_timeout": "3s",
				"tools": {
					"fixture.echo": {
						"capability": "read",
						"access_mode": "read",
						"parallel_policy": "concurrent",
						"sandbox_requirement": "none"
					}
				}
			}
		}
	}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Servers["Fixture Server"].ConnectTimeout != 3*time.Second {
		t.Fatalf("connect timeout = %s", config.Servers["Fixture Server"].ConnectTimeout)
	}
	if got := ModelToolName("Fixture Server", "fixture.echo"); got != "mcp_fixture_server_fixture_echo" {
		t.Fatalf("model tool name = %q", got)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"servers":{},"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("unknown config field was accepted")
	}
}

func TestHTTPConfigTimeoutsAuthReferencesAndHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp-http.json")
	data := `{
		"version": 1,
		"servers": {
			"remote": {
				"transport": "http",
				"url": "https://mcp.invalid/rpc",
				"headers": {"X-Tenant": "tenant-a"},
				"header_env": {"X-API-Key": "MCP_API_KEY"},
				"bearer_token_env": "MCP_BEARER_TOKEN",
				"connect_timeout": "2s",
				"read_timeout": "3s",
				"call_timeout": "4s",
				"shutdown_timeout": "1s",
				"tools": {
					"echo": {
						"capability": "network",
						"access_mode": "read",
						"parallel_policy": "concurrent",
						"sandbox_requirement": "none"
					}
				}
			}
		}
	}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	server := config.Servers["remote"]
	if server.ConnectTimeout != 2*time.Second ||
		server.ReadTimeout != 3*time.Second ||
		server.CallTimeout != 4*time.Second ||
		server.ShutdownTimeout != time.Second {
		t.Fatalf("timeouts = %+v", server)
	}
	firstHash, err := ConfigHash(config)
	if err != nil {
		t.Fatal(err)
	}
	server.Headers["X-Tenant"] = "tenant-b"
	config.Servers["remote"] = server
	secondHash, err := ConfigHash(config)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash == secondHash {
		t.Fatal("secret-bearing header change did not invalidate config hash")
	}
}

func TestPermissionProfileRejectsCapabilityAndResourceEscalation(t *testing.T) {
	base := fixtureServerConfig("safe")
	base.PermissionProfile = &PermissionProfile{
		Capabilities:  []string{"read"},
		ResourceKinds: []string{"workspace"},
		NetworkHosts:  []string{"example.internal"},
	}
	config := Config{
		Version: ConfigVersion, Servers: map[string]ServerConfig{"remote": base},
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}

	escalated := base
	binding := escalated.Tools["safe"]
	binding.Capability = "process"
	escalated.Tools = map[string]ToolBinding{"safe": binding}
	config.Servers["remote"] = escalated
	if err := config.Validate(); err == nil {
		t.Fatal("capability escalation was accepted")
	}

	network := base
	binding = network.Tools["safe"]
	binding.Resources = []ResourceBinding{{
		Kind: "network", ID: "other.internal", Access: "read",
	}}
	network.Tools = map[string]ToolBinding{"safe": binding}
	network.PermissionProfile = &PermissionProfile{
		Capabilities:  []string{"read"},
		ResourceKinds: []string{"network"},
		NetworkHosts:  []string{"example.internal"},
	}
	config.Servers["remote"] = network
	if err := config.Validate(); err == nil {
		t.Fatal("network host escalation was accepted")
	}
}

func TestPermissionProfileRejectsDynamicNetworkHost(t *testing.T) {
	server := fixtureServerConfig("safe")
	binding := server.Tools["safe"]
	binding.Resources = []ResourceBinding{{
		Kind: "network", Field: "host", Access: "read",
	}}
	server.Tools["safe"] = binding
	server.PermissionProfile = &PermissionProfile{
		Capabilities:  []string{"read"},
		ResourceKinds: []string{"network"},
		NetworkHosts:  []string{"example.internal"},
	}
	config := Config{
		Version: ConfigVersion, Servers: map[string]ServerConfig{"remote": server},
	}
	if err := config.Validate(); err == nil {
		t.Fatal("dynamic network host bypassed permission ceiling")
	}
}
