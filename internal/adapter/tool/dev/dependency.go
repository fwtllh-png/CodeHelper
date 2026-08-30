package dev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/typed"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type dependencyTool struct {
	typed.Contract[dependencyInput, tool.Result]
	root    string
	backend sandbox.Backend
}

type dependencyExecutor struct {
	tool.OutcomeExecutor
	binding tool.TrustedBinding
}

func (e *dependencyExecutor) TrustedBinding() tool.TrustedBinding {
	return e.binding
}

func (*dependencyExecutor) ExecutionDisposition() tool.ExecutionDisposition {
	return tool.DispositionWaitForTeardown
}

type dependencyInput struct {
	Ecosystem      string                       `json:"ecosystem"`
	NetworkTargets []tool.DeclaredNetworkTarget `json:"network_targets"`
}

type dependencyCommand struct {
	Ecosystem string
	Binary    string
	Args      []string
}

func registerDependency(
	registry *tool.Registry,
	root string,
	backend sandbox.Backend,
) error {
	instance := &dependencyTool{root: root, backend: backend}
	runtime, err := typed.Define(typed.Spec[dependencyInput, tool.Result]{
		Descriptor:  instance.Descriptor(),
		Disposition: tool.DispositionWaitForTeardown,
		Validate: func(input dependencyInput) error {
			return tool.ValidateDeclaredNetworkTargets(input.NetworkTargets)
		},
		Run:    instance.run,
		Encode: func(result tool.Result) (tool.Result, error) { return result, nil },
	})
	if err != nil {
		return err
	}
	outcome, ok := runtime.(tool.OutcomeExecutor)
	if !ok {
		return errors.New("dependency resolver typed runtime is incomplete")
	}
	return registry.Register(&dependencyExecutor{
		OutcomeExecutor: outcome, binding: instance.TrustedBinding(),
	})
}

func (t *dependencyTool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "dependency_resolve",
		Description: "Resolve or download declared project dependencies with scripts disabled " +
			"and the workspace read-only. Declare required registry hosts in network_targets.",
		DiscoveryTerms: []string{
			"dependencies", "package install", "resolve packages", "依赖", "安装依赖", "包管理",
		},
		Visibility: tool.VisibleModel, Capability: tool.CapabilityProcess,
		AccessMode: tool.AccessRead,
		ResourceResolver: tool.ResourceResolver{
			Templates: []tool.ResourceTemplate{
				{Kind: "repo", ID: ".", Access: tool.AccessRead, Tree: true},
				{Kind: "process", ID: "dependency-manager", Access: tool.AccessWrite, Tree: true},
			},
			NetworkTargetsField: "network_targets",
		},
		ParallelPolicy: tool.ParallelSerial, RepeatPolicy: tool.RepeatExecute,
		SandboxRequirement: tool.SandboxStrong, Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"ecosystem": map[string]any{
					"type": "string",
					"enum": []any{"auto", "go", "node", "rust", "maven", "gradle"},
				},
				"network_targets": tool.NetworkTargetsInputSchema(),
			},
			"additionalProperties": false,
		},
	}
}

func (t *dependencyTool) TrustedBinding() tool.TrustedBinding {
	binding := tool.TrustedBindingFromDescriptor(t.Descriptor())
	binding.Effect = tool.EffectContract{
		Mode: tool.EffectFixed, Kind: tool.EffectNetworkRead,
		Risk: tool.RiskMedium, Reversibility: tool.Reversible,
		WorkspaceTransaction: tool.TransactionNone,
		Approval:             tool.ApprovalPolicyDefault,
	}
	return binding
}

func (t *dependencyTool) run(
	ctx context.Context,
	input dependencyInput,
) (tool.Result, error) {
	commands, err := detectDependencyCommands(t.root, input.Ecosystem)
	if err != nil {
		return tool.Result{}, err
	}
	receipts := make([]map[string]any, 0, len(commands))
	directory, err := process.OpenPinnedDirectory(t.backend, t.root)
	if err != nil {
		return tool.Result{}, err
	}
	defer directory.Close()
	for _, command := range commands {
		result, err := process.Run(ctx, process.Options{
			Path: command.Binary, Args: command.Args, Dir: t.root,
			DirFile: directory,
			Sandbox: t.backend, RequireSandbox: true,
			WorkspaceReadOnly: true, DenyNetwork: len(input.NetworkTargets) == 0,
			OutputLimitBytes: process.ModelOutputLimitBytes,
		})
		if err != nil {
			return tool.Result{}, err
		}
		receipt := map[string]any{
			"ecosystem": command.Ecosystem, "exit_code": result.ExitCode,
			"stdout": strings.TrimSpace(result.Stdout),
			"stderr": strings.TrimSpace(result.Stderr),
		}
		receipts = append(receipts, receipt)
		if result.ExitCode != 0 {
			content, _ := json.Marshal(map[string]any{"checks": receipts})
			return tool.Result{Content: string(content), IsError: true}, nil
		}
	}
	content, err := json.Marshal(map[string]any{"checks": receipts})
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content:  string(content),
		Metadata: map[string]any{"ecosystems": len(commands)},
	}, nil
}

func detectDependencyCommands(root, requested string) ([]dependencyCommand, error) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		requested = "auto"
	}
	exists := func(name string) bool {
		info, err := os.Stat(filepath.Join(root, name))
		return err == nil && !info.IsDir()
	}
	var ecosystems []string
	if requested != "auto" {
		ecosystems = []string{requested}
	} else {
		if exists("go.mod") {
			ecosystems = append(ecosystems, "go")
		}
		if exists("package.json") {
			ecosystems = append(ecosystems, "node")
		}
		if exists("Cargo.toml") {
			ecosystems = append(ecosystems, "rust")
		}
		if exists("pom.xml") {
			ecosystems = append(ecosystems, "maven")
		}
		if exists("gradlew") || exists("build.gradle") || exists("build.gradle.kts") {
			ecosystems = append(ecosystems, "gradle")
		}
	}
	if len(ecosystems) == 0 {
		return nil, errors.New("no supported dependency manifest was found")
	}
	sort.Strings(ecosystems)
	commands := make([]dependencyCommand, 0, len(ecosystems))
	for _, ecosystem := range ecosystems {
		name, args, err := dependencyInvocation(root, ecosystem, exists)
		if err != nil {
			return nil, err
		}
		binary, err := exec.LookPath(name)
		if err != nil {
			return nil, fmt.Errorf("%s dependency manager %q is unavailable", ecosystem, name)
		}
		commands = append(commands, dependencyCommand{
			Ecosystem: ecosystem, Binary: binary, Args: args,
		})
	}
	return commands, nil
}

func dependencyInvocation(
	root, ecosystem string,
	exists func(string) bool,
) (string, []string, error) {
	switch ecosystem {
	case "go":
		return "go", []string{"mod", "download"}, nil
	case "node":
		if exists("pnpm-lock.yaml") {
			return "pnpm", []string{"fetch", "--frozen-lockfile", "--ignore-scripts"}, nil
		}
		return "npm", []string{
			"install", "--dry-run", "--ignore-scripts", "--package-lock=false",
		}, nil
	case "rust":
		return "cargo", []string{"fetch", "--locked"}, nil
	case "maven":
		return "mvn", []string{"dependency:go-offline", "-DskipTests"}, nil
	case "gradle":
		if exists("gradlew") {
			return filepath.Join(root, "gradlew"), []string{
				"--no-daemon", "dependencies",
			}, nil
		}
		return "gradle", []string{"--no-daemon", "dependencies"}, nil
	default:
		return "", nil, fmt.Errorf("unsupported dependency ecosystem %q", ecosystem)
	}
}
