package git

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/typed"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type Tool struct {
	root    string
	kind    string
	backend sandbox.Backend
}

func RegisterWithBackend(registry *tool.Registry, root string, backend sandbox.Backend) error {
	if backend == nil {
		return errors.New("Git tools require an injected sandbox backend")
	}
	backend, err := sandbox.BindPolicy(backend, sandbox.Options{WorkspaceRoot: root})
	if err != nil {
		return err
	}
	workspace, err := sandbox.NewWorkspace(root)
	if err != nil {
		return err
	}
	absolute := workspace.Root()
	registry.SetSandboxBackend(backend)
	for _, kind := range []string{
		"git_status", "git_diff", "git_log", "git_remote", "git_branch", "git_show", "git_blame",
	} {
		if err := registry.Register(&Tool{root: absolute, kind: kind, backend: backend}, nil); err != nil {
			return err
		}
	}
	return registerHosted(registry)
}

func (t *Tool) Descriptor() tool.Descriptor {
	properties := map[string]any{
		"staged":   map[string]any{"type": "boolean"},
		"limit":    map[string]any{"type": "integer"},
		"revision": map[string]any{"type": "string"},
		"path":     map[string]any{"type": "string"},
	}
	required := []string{}
	if t.kind == "git_blame" {
		required = []string{"path"}
	}
	return tool.Descriptor{
		Name: t.kind, Description: "Inspect the workspace Git repository", Visibility: tool.VisibleModel,
		Capability: tool.CapabilityRead, AccessMode: tool.AccessTree,
		ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
			Kind: "repo", ID: ".", Access: tool.AccessRead, Tree: true,
		}}},
		ParallelPolicy:     tool.ParallelConcurrent,
		SandboxRequirement: tool.SandboxStrong, Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type":       "object",
			"properties": properties, "required": required, "additionalProperties": false,
		},
	}
}

func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		Staged   bool   `json:"staged"`
		Limit    int    `json:"limit"`
		Revision string `json:"revision"`
		Path     string `json:"path"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	arguments := []string{"status", "--short"}
	switch t.kind {
	case "git_diff":
		arguments = []string{"diff", "--no-ext-diff"}
		if input.Staged {
			arguments = append(arguments, "--cached")
		}
	case "git_log":
		if input.Limit <= 0 {
			input.Limit = 20
		}
		arguments = []string{"log", "--oneline", "-n", strconv.Itoa(input.Limit)}
	case "git_remote":
		arguments = []string{"remote", "-v"}
	case "git_branch":
		arguments = []string{"branch", "--all", "--no-color"}
	case "git_show":
		revision, err := safeRevision(input.Revision)
		if err != nil {
			return tool.Result{}, err
		}
		arguments = []string{"show", "--no-ext-diff", "--format=fuller", revision}
		if input.Path != "" {
			if err := safePath(input.Path); err != nil {
				return tool.Result{}, err
			}
			arguments = append(arguments, "--", input.Path)
		}
	case "git_blame":
		revision, err := safeRevision(input.Revision)
		if err != nil {
			return tool.Result{}, err
		}
		if err := safePath(input.Path); err != nil {
			return tool.Result{}, err
		}
		arguments = []string{"blame", "--line-porcelain", revision, "--", input.Path}
	}
	directory, err := process.OpenPinnedDirectory(t.backend, t.root)
	if err != nil {
		return tool.Result{}, err
	}
	defer directory.Close()
	command, err := process.NewCommand(ctx, process.Options{
		Path: gitExecutable(), Args: arguments, Dir: t.root,
		DirFile: directory, Sandbox: t.backend, RequireStrongSandbox: true,
	})
	if err != nil {
		return tool.Result{}, err
	}
	output, runErr := command.CombinedOutput()
	exitCode := process.ExitCode(runErr)
	return tool.Result{
		Content: string(output), IsError: exitCode != 0,
		Metadata: map[string]any{"exit_code": exitCode},
	}, nil
}

func (*Tool) ExecutionDisposition() tool.ExecutionDisposition {
	return tool.DispositionWaitForTeardown
}

func (t *Tool) ExecuteOutcome(
	ctx context.Context,
	raw json.RawMessage,
) (tool.Result, tool.Outcome, error) {
	return typed.ExecuteOutcome(ctx, t, raw)
}

// gitExecutable prefers the real Command Line Tools / Xcode binary so Apple's
// /usr/bin/git shim does not need xcrun cache writes inside the seatbelt.
func gitExecutable() string {
	for _, candidate := range []string{
		"/Library/Developer/CommandLineTools/usr/bin/git",
		"/Applications/Xcode.app/Contents/Developer/usr/bin/git",
	} {
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate
		}
	}
	return "git"
}

func safeRevision(revision string) (string, error) {
	if revision == "" {
		return "HEAD", nil
	}
	if strings.HasPrefix(revision, "-") || strings.ContainsAny(revision, "\x00\n\r") {
		return "", errors.New("invalid Git revision")
	}
	return revision, nil
}

func safePath(name string) error {
	if name == "" || filepath.IsAbs(name) {
		return errors.New("Git path must be a non-empty relative path")
	}
	clean := filepath.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return errors.New("Git path is outside workspace")
	}
	return nil
}
