package plugin

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManifestV2CompilesIndependentCapabilities(t *testing.T) {
	root, manifest := writeV2CapabilityFixture(t)
	loaded, err := ValidateBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := CompileCapabilityBundle(root, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Capabilities) != 4 {
		t.Fatalf("capabilities = %+v", bundle.Capabilities)
	}
	tokens := make(map[string]struct{}, len(bundle.Capabilities))
	for _, capability := range bundle.Capabilities {
		if capability.SourceDigest == "" ||
			capability.Authority.PermissionDigest == "" ||
			capability.Authority.Token == "" {
			t.Fatalf("capability authority = %+v", capability)
		}
		tokens[capability.Authority.Token] = struct{}{}
	}
	if len(tokens) != 4 {
		t.Fatalf("capability authorities are not independent: %+v", bundle.Capabilities)
	}
	before, err := ManifestCapabilityHash(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Interface.Description = "changed UI metadata"
	after, err := ManifestCapabilityHash(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("interface metadata changed the model capability policy digest")
	}
}

func TestManifestV2RejectsRootEscapeAndDigestDrift(t *testing.T) {
	_, manifest := writeV2CapabilityFixture(t)
	manifest.Bundle.Skills[0].Root = "../escape"
	if _, err := NormalizeManifest(manifest); err == nil {
		t.Fatal("capability root escape was accepted")
	}

	root, manifest := writeV2CapabilityFixture(t)
	if err := os.WriteFile(
		filepath.Join(root, manifest.Bundle.Hooks[0].Config),
		[]byte(`{"version":1,"hooks":{}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileCapabilityBundle(root, manifest); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("digest drift error = %v", err)
	}
}

func TestCapabilityDisableIsScopedAndAuthorityBound(t *testing.T) {
	discovery := t.TempDir()
	bundleRoot := filepath.Join(discovery, "fixture")
	if err := os.Mkdir(bundleRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	writeV2CapabilityFixtureAt(t, bundleRoot)
	registry, err := NewRegistry(RegistryConfig{
		Discovery:     DiscoveryOptions{WorkspaceRoot: discovery},
		StagingRoot:   filepath.Join(t.TempDir(), "staging"),
		StatePath:     filepath.Join(t.TempDir(), "plugins.json"),
		WorkspaceRoot: t.TempDir(), Backend: loaderTestBackend{},
		Now:           func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		WatchInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	if _, err := registry.Trust("fixture"); err != nil {
		t.Fatal(err)
	}
	if err := registry.Enable("fixture"); err != nil {
		t.Fatal(err)
	}
	state, err := registry.state.Read()
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadManifest(bundleRoot)
	if err != nil {
		t.Fatal(err)
	}
	capabilityDigest, err := ManifestCapabilityHash(manifest)
	if err != nil {
		t.Fatal(err)
	}
	receipt := state.Plugins["fixture"].Receipt
	if receipt.Version != manifest.Version ||
		receipt.Publisher != manifest.Publisher ||
		receipt.ContentHash == "" ||
		!equalHash(receipt.CapabilityHash, capabilityDigest) {
		t.Fatalf("package lock receipt = %+v", receipt)
	}
	bundles, err := registry.CapabilityBundles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	skillCapability, ok := bundles[0].Capability(CapabilitySkill, "skills")
	if !ok {
		t.Fatal("skill capability missing")
	}
	hookCapability, ok := bundles[0].Capability(CapabilityHook, "hooks")
	if !ok {
		t.Fatal("hook capability missing")
	}
	if err := registry.DisableCapability("fixture", "hooks"); err != nil {
		t.Fatal(err)
	}
	if err := registry.VerifyCapabilityToken(
		t.Context(), "fixture", bundles[0].Generation,
		skillCapability.Authority.Token,
	); err != nil {
		t.Fatalf("unrelated skill authority was revoked: %v", err)
	}
	if err := registry.VerifyCapabilityToken(
		t.Context(), "fixture", bundles[0].Generation,
		hookCapability.Authority.Token,
	); err == nil {
		t.Fatal("disabled hook authority remained valid")
	}
	bundles, err = registry.CapabilityBundles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	skillCapability, _ = bundles[0].Capability(CapabilitySkill, "skills")
	hookCapability, _ = bundles[0].Capability(CapabilityHook, "hooks")
	if !skillCapability.Enabled || hookCapability.Enabled {
		t.Fatalf("scoped capability state = %+v", bundles[0].Capabilities)
	}
}

func writeV2CapabilityFixture(t *testing.T) (string, Manifest) {
	t.Helper()
	root := t.TempDir()
	return root, writeV2CapabilityFixtureAt(t, root)
}

func writeV2CapabilityFixtureAt(t *testing.T, root string) Manifest {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "skills", "review"), 0o700); err != nil {
		t.Fatal(err)
	}
	executable := []byte("#!/bin/sh\nexit 0\n")
	hookConfig := []byte(`{
  "version": 1,
  "hooks": {
    "ToolCallBefore": [{
      "id": "fixture-hook",
      "command": "/bin/true",
      "timeout": "1s",
      "max_output_bytes": 1024
    }]
  }
}`)
	mcpConfig := []byte(`{
  "version": 1,
  "servers": {
    "fixture": {
      "transport": "stdio",
      "host_trusted": true,
      "command": "/missing/plugin-mcp",
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
	files := map[string][]byte{
		"run.sh":     executable,
		"hooks.json": hookConfig,
		"mcp.json":   mcpConfig,
		filepath.Join("skills", "review", "SKILL.md"): []byte(`---
name: plugin-review
description: Plugin review skill
---
Review the selected change.
`),
		filepath.Join("skills", "review", "skill.toml"): []byte(
			"schema_version = 1\n" +
				"name = \"plugin-review\"\n" +
				"version = \"1.0.0\"\n" +
				"codehelper = \">=0.0.0-0\"\n",
		),
	}
	for path, data := range files {
		target := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o600)
		if path == "run.sh" {
			mode = 0o700
		}
		if err := os.WriteFile(target, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	skillDigest, err := HashBundle(filepath.Join(root, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		SchemaVersion: ManifestSchemaV2,
		Name:          "fixture", Version: "1.0.0", Publisher: "acme",
		CodeHelper: ">=0.0.0-0", Generation: 1,
		Bundle: CapabilityBundle{
			Tools: []ToolCapability{{
				ID: "tool", Executable: "run.sh",
				ExecutableSHA256: hashBytes(executable),
				Permissions: CapabilityInventory{
					Tools:           []string{"plugin_run"},
					FilesystemRoots: []string{"workspace"},
					AllowProcess:    true,
				},
			}},
			Skills: []SkillCapability{{
				ID: "skills", Root: "skills", SHA256: skillDigest,
			}},
			MCP: []MCPCapability{{
				ID: "mcp", Config: "mcp.json", SHA256: hashBytes(mcpConfig),
				Permissions: CapabilityInventory{
					Tools: []string{"mcp"}, AllowProcess: true,
				},
			}},
			Hooks: []HookCapability{{
				ID: "hooks", Config: "hooks.json", SHA256: hashBytes(hookConfig),
				Permissions: CapabilityInventory{
					Tools: []string{"hook"}, AllowProcess: true,
				},
			}},
		},
		Interface: InterfaceMetadata{
			DisplayName: "Fixture", Description: "Fixture extension",
		},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ManifestName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return manifest
}
