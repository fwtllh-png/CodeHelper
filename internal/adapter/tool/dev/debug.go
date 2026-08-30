package dev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/typed"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type debugTool struct {
	typed.Contract[debugInput, tool.Result]
	root      string
	workspace *sandbox.Workspace
	backend   sandbox.Backend
	lldb      string
}

type debugInput struct {
	Path        string   `json:"path"`
	Args        []string `json:"args"`
	Breakpoints []string `json:"breakpoints"`
}

func registerDebugger(
	registry *tool.Registry,
	root string,
	backend sandbox.Backend,
) error {
	workspace, err := sandbox.NewWorkspace(root)
	if err != nil {
		return err
	}
	lldb, _ := exec.LookPath("lldb")
	instance := &debugTool{
		root: workspace.Root(), workspace: workspace, backend: backend, lldb: lldb,
	}
	contract, err := typed.NewResultContract(typed.ResultSpec[debugInput]{
		Name: "debug_run", Disposition: tool.DispositionWaitForTeardown,
		Run: instance.run,
	})
	if err != nil {
		return err
	}
	instance.Contract = contract
	return registry.Register(instance)
}

func (t *debugTool) Descriptor() tool.Descriptor {
	availability := tool.AvailabilityAvailable
	reason := ""
	if t.lldb == "" {
		availability = tool.AvailabilityUnavailable
		reason = "lldb is not installed"
	}
	resources := []tool.ResourceTemplate{
		{Kind: "file", Field: "path", Access: tool.AccessRead},
		{Kind: "process", ID: "debuggee", Access: tool.AccessWrite, Tree: true},
	}
	if t.lldb != "" {
		resources = append(resources, tool.ResourceTemplate{
			Kind: "directory", ID: filepath.Dir(t.lldb),
			Access: tool.AccessRead, Tree: true,
		})
	}
	return tool.Descriptor{
		Name: "debug_run",
		Description: "Run a workspace executable under LLDB in batch mode, set validated " +
			"symbol or file:line breakpoints, and return the debugger transcript",
		DiscoveryTerms: []string{"debug", "breakpoint", "backtrace", "调试", "断点", "堆栈"},
		Visibility:     tool.VisibleModel, Capability: tool.CapabilityProcess,
		AccessMode:       tool.AccessRead,
		ResourceResolver: tool.ResourceResolver{Templates: resources},
		ParallelPolicy:   tool.ParallelSerial, RepeatPolicy: tool.RepeatExecute,
		SandboxRequirement: tool.SandboxStrong,
		Availability:       availability, UnavailableReason: reason,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "minLength": 1},
				"args": map[string]any{
					"type": "array", "maxItems": 128,
					"items": map[string]any{"type": "string"},
				},
				"breakpoints": map[string]any{
					"type": "array", "maxItems": 64,
					"items": map[string]any{"type": "string", "minLength": 1},
				},
			},
			"required": []string{"path"}, "additionalProperties": false,
		},
	}
}

func (t *debugTool) TrustedBinding() tool.TrustedBinding {
	binding := tool.TrustedBindingFromDescriptor(t.Descriptor())
	binding.Effect = tool.EffectContract{
		Mode: tool.EffectFixed, Kind: tool.EffectProcessReadOnly,
		Risk: tool.RiskMedium, Reversibility: tool.Reversible,
		WorkspaceTransaction: tool.TransactionNone,
		Approval:             tool.ApprovalPolicyDefault,
	}
	return binding
}

func (t *debugTool) run(ctx context.Context, input debugInput) (tool.Result, error) {
	if t.lldb == "" {
		return tool.Result{}, errors.New("lldb is not installed")
	}
	target, err := t.workspace.Resolve(input.Path, sandbox.MustExist)
	if err != nil {
		return tool.Result{}, err
	}
	arguments := []string{"--batch"}
	for _, breakpoint := range input.Breakpoints {
		command, err := breakpointCommand(breakpoint)
		if err != nil {
			return tool.Result{}, err
		}
		arguments = append(arguments, "-o", command)
	}
	arguments = append(arguments, "-o", "run", "-o", "thread backtrace all", "--", target)
	arguments = append(arguments, input.Args...)
	directory, err := process.OpenPinnedDirectory(t.backend, t.root)
	if err != nil {
		return tool.Result{}, err
	}
	defer directory.Close()
	result, err := process.Run(ctx, process.Options{
		Path: t.lldb, Args: arguments, Dir: t.root,
		DirFile: directory,
		Sandbox: t.backend, RequireSandbox: true,
		WorkspaceReadOnly: true, DenyNetwork: true,
		OutputLimitBytes: process.ModelOutputLimitBytes,
	})
	if err != nil {
		return tool.Result{}, err
	}
	transcript := strings.TrimSpace(result.Stdout + "\n" + result.Stderr)
	payload, err := json.Marshal(map[string]any{
		"exit_code": result.ExitCode, "transcript": transcript,
	})
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(payload), IsError: result.ExitCode != 0,
		Metadata: map[string]any{
			"exit_code": result.ExitCode, "breakpoint_count": len(input.Breakpoints),
		},
	}, nil
}

func breakpointCommand(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return "", errors.New("breakpoint must not be empty")
	}
	if index := strings.LastIndexByte(value, ':'); index > 0 {
		line, err := strconv.Atoi(value[index+1:])
		path := value[:index]
		if err == nil && line > 0 && validBreakpointPath(path) {
			return fmt.Sprintf(
				"breakpoint set --file %q --line %d",
				path, line,
			), nil
		}
	}
	for _, character := range value {
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) &&
			character != '_' && character != ':' && character != '~' &&
			character != '.' {
			return "", fmt.Errorf("invalid symbol breakpoint %q", value)
		}
	}
	return "breakpoint set --name " + value, nil
}

func validBreakpointPath(value string) bool {
	return value != "" && !filepath.IsAbs(value) &&
		value != ".." && !strings.HasPrefix(filepath.Clean(value), ".."+string(filepath.Separator)) &&
		!strings.ContainsAny(value, `"'`+"\x00\r\n")
}
