package wire

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
)

func TestDiagnosticCommandReadRootsIncludeScriptAndInterpreterTrees(t *testing.T) {
	root := t.TempDir()
	commandDir := filepath.Join(root, "bin")
	commandPackage := filepath.Join(root, "command-package")
	nodeBin := filepath.Join(root, "node", "bin")
	for _, directory := range []string{commandDir, commandPackage, nodeBin} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{
		filepath.Join(commandPackage, "cli.mjs"): "#!/usr/bin/env node\n",
		filepath.Join(nodeBin, "node"):           "#!/bin/sh\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(
		filepath.Join(commandPackage, "cli.mjs"),
		filepath.Join(commandDir, "markdownlint-cli2"),
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", commandDir+string(os.PathListSeparator)+nodeBin)

	roots := diagnosticCommandReadRoots(map[string]diagnostics.Command{
		".md": {Name: "markdownlint-cli2", Args: []string{"{path}"}},
	})
	canonicalPackage, err := filepath.EvalSymlinks(commandPackage)
	if err != nil {
		t.Fatal(err)
	}
	canonicalNode, err := filepath.EvalSymlinks(filepath.Join(root, "node"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(roots, canonicalPackage) ||
		!slices.Contains(roots, canonicalNode) {
		t.Fatalf(
			"read roots %q do not contain command package %q and Node tree %q",
			roots,
			canonicalPackage,
			canonicalNode,
		)
	}
}
