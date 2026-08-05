package lsp

import (
	"context"
	"encoding/json"
	"errors"

	diagnostics "github.com/fwtllh-png/CodeHelper/internal/adapter/lsp"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type Tool struct {
	checker diagnostics.Checker
}

func RegisterWithBackend(registry *tool.Registry, root string, backend sandbox.Backend) error {
	if backend == nil {
		return errors.New("LSP tools require an injected sandbox backend")
	}
	backend, err := sandbox.BindPolicy(backend, sandbox.Options{WorkspaceRoot: root})
	if err != nil {
		return err
	}
	registry.SetSandboxBackend(backend)
	return registry.Register(&Tool{
		checker: diagnostics.Checker{Root: root, Sandbox: backend},
	}, nil)
}

func (t *Tool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "lsp_diagnostics", Description: "Open and edit documents through a language server and return structured diagnostics", Visibility: tool.VisibleModel,
		Capability: tool.CapabilityProcess, AccessMode: tool.AccessTree,
		ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{
			{Kind: "repo", ID: ".", Access: tool.AccessRead, Tree: true},
			{Kind: "process", ID: "lsp", Access: tool.AccessWrite, Tree: true},
		}},
		ParallelPolicy:     tool.ParallelSerial,
		SandboxRequirement: tool.SandboxStrong, Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"files":   map[string]any{"type": "array"},
				"changes": map[string]any{"type": "array"},
			},
			"required":             []string{"files"},
			"additionalProperties": false,
		},
	}
}

func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		Files   []string             `json:"files"`
		Changes []diagnostics.Change `json:"changes"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	values, err := t.checker.Analyze(ctx, input.Files, input.Changes)
	if err != nil {
		return tool.Result{}, err
	}
	output, err := json.Marshal(values)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content:  string(output),
		Metadata: map[string]any{"diagnostics": values, "diagnostic_count": len(values)},
	}, nil
}
