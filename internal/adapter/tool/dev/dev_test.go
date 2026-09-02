package dev

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	"github.com/fwtllh-png/QCode/internal/security/controlmatrix"
	"github.com/fwtllh-png/QCode/internal/security/sandbox"
	"github.com/fwtllh-png/QCode/internal/testutil/tooltest"
)

type passthroughBackend struct{}

func (passthroughBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "test", Backend: "passthrough", Available: true,
		Effective: controlmatrix.Matrix{
			FilesystemRead:  controlmatrix.FilesystemReadDeclaredRoots,
			FilesystemWrite: controlmatrix.FilesystemWriteExactPaths,
			Network:         controlmatrix.NetworkDenied,
			ProcessTree:     controlmatrix.ProcessTreeGroupKill,
			CrossProcess:    controlmatrix.CrossProcessUnrestricted,
			Syscall:         controlmatrix.SyscallDenyDangerous,
			IPC:             controlmatrix.IPCUnrestricted,
			PathIdentity:    controlmatrix.PathIdentityDescriptorRelative,
			ArtifactOrigin:  controlmatrix.ArtifactOriginUnverifiedPath,
			DurableRecovery: controlmatrix.DurableRecoveryMemoryOnly,
		},
	}
}

func (passthroughBackend) Prepare(
	_ context.Context,
	command sandbox.Command,
) (sandbox.Command, error) {
	command.PreparedReadOnly = command.WorkspaceReadOnly
	command.PreparedWritePaths = append(
		[]string(nil), command.WorkspaceWritePaths...,
	)
	command.PreparedNetworkDenied = command.DenyNetwork
	return command, nil
}

func TestFormatCodeRunsInstalledFormatterForExactPath(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	if err := os.WriteFile(source, []byte("package main\nfunc main( ){}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry(nil, nil)
	backend, err := sandbox.BindPolicy(
		passthroughBackend{}, sandbox.Options{WorkspaceRoot: root},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := registerFormat(registry, root, backend); err != nil {
		t.Fatal(err)
	}
	result, err := tooltest.Execute(t.Context(), registry, tool.Call{
		Name: "format_code", Arguments: json.RawMessage(`{"paths":["main.go"]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || strings.Contains(string(data), "main( )") {
		t.Fatalf("format result=%+v source=%q", result, data)
	}
}

func TestBreakpointCommandRejectsCommandInjection(t *testing.T) {
	for _, value := range []string{"main; shell touch owned", "../main.cpp:4", "main\nquit"} {
		if _, err := breakpointCommand(value); err == nil {
			t.Fatalf("unsafe breakpoint %q accepted", value)
		}
	}
	if command, err := breakpointCommand("src/main.cpp:42"); err != nil ||
		!strings.Contains(command, "--line 42") {
		t.Fatalf("file breakpoint = %q, %v", command, err)
	}
	if command, err := breakpointCommand("Namespace::Run"); err != nil ||
		command != "breakpoint set --name Namespace::Run" {
		t.Fatalf("symbol breakpoint = %q, %v", command, err)
	}
}

func TestDetectDependencyCommandsUsesManifestAndLockedInvocation(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"go.mod", "package.json", "pnpm-lock.yaml"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bin := t.TempDir()
	for _, name := range []string{"go", "pnpm"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	commands, err := detectDependencyCommands(root, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 2 ||
		commands[0].Ecosystem != "go" ||
		strings.Join(commands[0].Args, " ") != "mod download" ||
		commands[1].Ecosystem != "node" ||
		!strings.Contains(strings.Join(commands[1].Args, " "), "frozen-lockfile") {
		t.Fatalf("dependency commands = %+v", commands)
	}
}
