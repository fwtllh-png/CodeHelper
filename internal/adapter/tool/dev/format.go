package dev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	"github.com/fwtllh-png/QCode/internal/adapter/tool/typed"
	"github.com/fwtllh-png/QCode/internal/platform/process"
	"github.com/fwtllh-png/QCode/internal/security/sandbox"
)

type formatTool struct {
	typed.Contract[formatInput, tool.Result]
	root       string
	workspace  *sandbox.Workspace
	backend    sandbox.Backend
	formatters map[string]formatter
}

type formatter struct {
	Name   string
	Binary string
	Args   []string
}

type formatInput struct {
	Paths []string `json:"paths"`
}

func registerFormat(
	registry *tool.Registry,
	root string,
	backend sandbox.Backend,
) error {
	workspace, err := sandbox.NewWorkspace(root)
	if err != nil {
		return err
	}
	instance := &formatTool{
		root: workspace.Root(), workspace: workspace, backend: backend,
		formatters: installedFormatters(),
	}
	contract, err := typed.NewResultContract(typed.ResultSpec[formatInput]{
		Name: "format_code", Disposition: tool.DispositionWaitForTeardown,
		Run: instance.run,
	})
	if err != nil {
		return err
	}
	instance.Contract = contract
	return registry.Register(instance)
}

func installedFormatters() map[string]formatter {
	candidates := map[string]formatter{
		".go":   {Name: "gofmt", Binary: "gofmt", Args: []string{"-w"}},
		".c":    {Name: "clang-format", Binary: "clang-format", Args: []string{"-i"}},
		".cc":   {Name: "clang-format", Binary: "clang-format", Args: []string{"-i"}},
		".cpp":  {Name: "clang-format", Binary: "clang-format", Args: []string{"-i"}},
		".cxx":  {Name: "clang-format", Binary: "clang-format", Args: []string{"-i"}},
		".h":    {Name: "clang-format", Binary: "clang-format", Args: []string{"-i"}},
		".hpp":  {Name: "clang-format", Binary: "clang-format", Args: []string{"-i"}},
		".rs":   {Name: "rustfmt", Binary: "rustfmt"},
		".py":   {Name: "black", Binary: "black", Args: []string{"--quiet"}},
		".java": {Name: "google-java-format", Binary: "google-java-format", Args: []string{"--replace"}},
		".js":   {Name: "prettier", Binary: "prettier", Args: []string{"--write"}},
		".jsx":  {Name: "prettier", Binary: "prettier", Args: []string{"--write"}},
		".ts":   {Name: "prettier", Binary: "prettier", Args: []string{"--write"}},
		".tsx":  {Name: "prettier", Binary: "prettier", Args: []string{"--write"}},
		".json": {Name: "prettier", Binary: "prettier", Args: []string{"--write"}},
		".css":  {Name: "prettier", Binary: "prettier", Args: []string{"--write"}},
		".md":   {Name: "prettier", Binary: "prettier", Args: []string{"--write"}},
	}
	resolved := make(map[string]formatter)
	for extension, candidate := range candidates {
		path, err := exec.LookPath(candidate.Binary)
		if err != nil {
			continue
		}
		candidate.Binary = path
		resolved[extension] = candidate
	}
	return resolved
}

func (t *formatTool) Descriptor() tool.Descriptor {
	names := make(map[string]struct{})
	resources := []tool.ResourceTemplate{{
		Kind: "process", ID: "formatter", Access: tool.AccessWrite, Tree: true,
	}}
	for _, formatter := range t.formatters {
		names[formatter.Name] = struct{}{}
		resources = append(resources, tool.ResourceTemplate{
			Kind: "directory", ID: filepath.Dir(formatter.Binary),
			Access: tool.AccessRead, Tree: true,
		})
	}
	available := make([]string, 0, len(names))
	for name := range names {
		available = append(available, name)
	}
	sort.Strings(available)
	availability := tool.AvailabilityAvailable
	reason := ""
	if len(available) == 0 {
		availability = tool.AvailabilityUnavailable
		reason = "no supported formatter is installed"
	}
	return tool.Descriptor{
		Name: "format_code",
		Description: "Format exact source files with installed native formatters. Available: " +
			strings.Join(available, ", "),
		DiscoveryTerms: []string{"format code", "formatter", "格式化代码", "代码格式"},
		Visibility:     tool.VisibleModel, Capability: tool.CapabilityProcess,
		AccessMode: tool.AccessWrite,
		ResourceResolver: tool.ResourceResolver{
			Templates: resources, PathsField: "paths",
		},
		ParallelPolicy: tool.ParallelSerial, RepeatPolicy: tool.RepeatExecute,
		SandboxRequirement: tool.SandboxStrong,
		Availability:       availability, UnavailableReason: reason,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"paths": map[string]any{
					"type": "array", "minItems": 1, "maxItems": 512,
					"items": map[string]any{"type": "string", "minLength": 1},
				},
			},
			"required": []string{"paths"}, "additionalProperties": false,
		},
	}
}

func (t *formatTool) TrustedBinding() tool.TrustedBinding {
	binding := tool.TrustedBindingFromDescriptor(t.Descriptor())
	binding.Effect = tool.EffectContract{
		Mode: tool.EffectFixed, Kind: tool.EffectWorkspaceEdit,
		Risk: tool.RiskMedium, Reversibility: tool.Reversible,
		WorkspaceTransaction:   tool.TransactionBeforeImage,
		RequireReadBeforeWrite: true,
		Approval:               tool.ApprovalPolicyDefault,
	}
	binding.ValidateMissingWriteParent = false
	return binding
}

func (t *formatTool) run(ctx context.Context, input formatInput) (tool.Result, error) {
	if len(input.Paths) == 0 {
		return tool.Result{}, errors.New("at least one path is required")
	}
	formatted := make([]string, 0, len(input.Paths))
	directory, err := process.OpenPinnedDirectory(t.backend, t.root)
	if err != nil {
		return tool.Result{}, err
	}
	defer directory.Close()
	for _, name := range input.Paths {
		path, err := t.workspace.Resolve(name, sandbox.MustExist)
		if err != nil {
			return tool.Result{}, err
		}
		formatter, ok := t.formatters[strings.ToLower(filepath.Ext(path))]
		if !ok {
			return tool.Result{}, fmt.Errorf("no installed formatter supports %q", name)
		}
		arguments := append([]string(nil), formatter.Args...)
		arguments = append(arguments, path)
		result, err := process.Run(ctx, process.Options{
			Path: formatter.Binary, Args: arguments, Dir: t.root,
			DirFile: directory,
			Sandbox: t.backend, RequireSandbox: true,
			WorkspaceReadOnly: true, WorkspaceWritePaths: []string{path},
			DenyNetwork:      true,
			OutputLimitBytes: process.ModelOutputLimitBytes,
		})
		if err != nil {
			return tool.Result{}, err
		}
		if result.ExitCode != 0 {
			return tool.Result{}, fmt.Errorf(
				"%s failed for %s: %s", formatter.Name, name,
				strings.TrimSpace(result.Stderr+"\n"+result.Stdout),
			)
		}
		formatted = append(formatted, name)
	}
	content, err := json.Marshal(map[string]any{"formatted": formatted})
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content:  string(content),
		Metadata: map[string]any{"formatted_paths": formatted},
	}, nil
}
