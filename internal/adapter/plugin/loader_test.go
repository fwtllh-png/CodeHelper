package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func TestLoaderVerifiesInventorySnapshotsExecutableAndRunsSandboxed(t *testing.T) {
	root := t.TempDir()
	bundleRoot := filepath.Join(root, "plugin")
	if err := os.Mkdir(bundleRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := []byte("#!/bin/sh\nprintf '%s|%s|%s' \"$1\" \"$2\" \"$CODEHELPER_API_KEY\"\n")
	if err := os.WriteFile(filepath.Join(bundleRoot, "run.sh"), executable, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := validManifest(executable)
	writeManifest(t, bundleRoot, manifest)
	receipt, err := Review(bundleRoot, manifest.Capabilities, manifest.Generation, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	loader, err := NewLoader(root, loaderTestBackend{})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loader.Load("plugin", receipt)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	t.Setenv("CODEHELPER_API_KEY", "must-not-reach-plugin")
	result, err := loaded.Run(t.Context(), json.RawMessage(`{"value":"ok"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 ||
		result.Stdout != `fixed|{"value":"ok"}|` ||
		strings.Contains(result.Stdout, "must-not-reach-plugin") {
		t.Fatalf("plugin result = %+v", result)
	}
	if inventory := loaded.Inventory(); len(inventory.Tools) != 1 ||
		inventory.Tools[0] != "plugin_run" {
		t.Fatalf("inventory = %+v", inventory)
	}
}

func TestLoaderRejectsTamperAndCapabilityDrift(t *testing.T) {
	root := t.TempDir()
	bundleRoot := filepath.Join(root, "plugin")
	if err := os.Mkdir(bundleRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := []byte("#!/bin/sh\nexit 0\n")
	executablePath := filepath.Join(bundleRoot, "run.sh")
	if err := os.WriteFile(executablePath, executable, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := validManifest(executable)
	writeManifest(t, bundleRoot, manifest)
	receipt, err := Review(bundleRoot, manifest.Capabilities, manifest.Generation, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	loader, err := NewLoader(root, loaderTestBackend{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executablePath, []byte("#!/bin/sh\nprintf tampered\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := loader.Load("plugin", receipt); err == nil ||
		!strings.Contains(err.Error(), "content changed") {
		t.Fatalf("tampered load error = %v", err)
	}

	if err := os.WriteFile(executablePath, executable, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest.Capabilities.NetworkHosts = []string{"example.invalid"}
	writeManifest(t, bundleRoot, manifest)
	changedReceipt, err := Review(
		bundleRoot, manifest.Capabilities, manifest.Generation, time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loader.Load("plugin", changedReceipt); err == nil ||
		!strings.Contains(err.Error(), "capabilities must exactly match") {
		t.Fatalf("capability load error = %v", err)
	}
}

func validManifest(executable []byte) Manifest {
	sum := sha256.Sum256(executable)
	return Manifest{
		SchemaVersion:    1,
		Name:             "fixture",
		Executable:       "run.sh",
		ExecutableSHA256: hex.EncodeToString(sum[:]),
		Arguments:        []string{"fixed"},
		Generation:       1,
		Capabilities: CapabilityInventory{
			Tools: []string{"plugin_run"}, FilesystemRoots: []string{"workspace"},
			AllowProcess: true,
		},
	}
}

func writeManifest(t *testing.T, root string, manifest Manifest) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ManifestName), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

type loaderTestBackend struct{}

func (loaderTestBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "test", Backend: "passthrough",
		Strength: sandbox.StrengthStrong, Available: true,
	}
}

func (loaderTestBackend) Prepare(
	_ context.Context,
	command sandbox.Command,
) (sandbox.Command, error) {
	return command, nil
}
