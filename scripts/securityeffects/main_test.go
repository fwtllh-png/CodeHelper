package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositorySideEffectInventory(t *testing.T) {
	var output bytes.Buffer
	if err := run(
		filepath.Clean("../.."),
		"testdata/contracts/security-side-effect-entrypoints.json",
		&output,
	); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "security side-effect inventory valid") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestUnownedProcessEntryPointFails(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/unsafe/run.go", `package unsafe

import "os/exec"

func run() {
	_ = exec.Command("untrusted")
}
`)
	writeFixture(t, root, "policy.json", `{
  "version": 1,
  "id": "SEC-EXEC-BOUNDARY-001",
  "rules": []
}`)
	var output bytes.Buffer
	err := run(root, "policy.json", &output)
	if err == nil || !strings.Contains(err.Error(), "unowned process side effect") {
		t.Fatalf("run() error = %v", err)
	}
}

func writeFixture(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
