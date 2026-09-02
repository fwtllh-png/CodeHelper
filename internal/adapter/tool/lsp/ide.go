package lsp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	diagnostics "github.com/fwtllh-png/QCode/internal/adapter/lsp"
	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	"github.com/fwtllh-png/QCode/internal/adapter/tool/typed"
	"github.com/fwtllh-png/QCode/internal/security/sandbox"
)

type ideTool struct {
	kind    string
	checker diagnostics.Checker
}

type ideInput struct {
	Path         string `json:"path"`
	Line         int    `json:"line"`
	Character    int    `json:"character"`
	EndLine      int    `json:"end_line"`
	EndCharacter int    `json:"end_character"`
	NewName      string `json:"new_name"`
}

func registerIDETools(
	registry *tool.Registry,
	root string,
	backend sandbox.Backend,
) error {
	for _, kind := range []string{
		"lsp_hover", "lsp_format_edits", "lsp_code_actions", "lsp_rename_edits",
	} {
		instance := &ideTool{
			kind: kind,
			checker: diagnostics.Checker{
				Root: root, Sandbox: backend,
			},
		}
		executor, err := instance.typedExecutor()
		if err != nil {
			return err
		}
		if err := registry.Register(executor); err != nil {
			return err
		}
	}
	return nil
}

func (t *ideTool) Descriptor() tool.Descriptor {
	servers := diagnostics.AvailableServers()
	availability := tool.AvailabilityAvailable
	unavailableReason := ""
	if len(servers) == 0 {
		availability = tool.AvailabilityUnavailable
		unavailableReason = "no supported language server is installed"
	}
	properties := map[string]any{
		"path": map[string]any{"type": "string", "minLength": 1},
	}
	required := []string{"path"}
	if t.kind != "lsp_format_edits" {
		properties["line"] = map[string]any{"type": "integer", "minimum": 1}
		properties["character"] = map[string]any{"type": "integer", "minimum": 1}
		required = append(required, "line", "character")
	}
	if t.kind == "lsp_code_actions" {
		properties["end_line"] = map[string]any{"type": "integer", "minimum": 1}
		properties["end_character"] = map[string]any{"type": "integer", "minimum": 1}
	}
	if t.kind == "lsp_rename_edits" {
		properties["new_name"] = map[string]any{"type": "string", "minLength": 1}
		required = append(required, "new_name")
	}
	description := map[string]string{
		"lsp_hover":        "Return language-server hover information at a source position",
		"lsp_format_edits": "Return language-server formatting edits without writing files",
		"lsp_code_actions": "Return language-server code actions for a source range",
		"lsp_rename_edits": "Return a language-server workspace rename edit without writing files",
	}[t.kind]
	return tool.Descriptor{
		Name: t.kind, Description: description + ". Installed servers: " + strings.Join(servers, ", "),
		DiscoveryTerms:     ideDiscoveryTerms(t.kind),
		Visibility:         tool.VisibleModel,
		Capability:         tool.CapabilityProcess,
		AccessMode:         tool.AccessTree,
		ResourceResolver:   tool.ResourceResolver{Templates: languageServerResources(servers)},
		ParallelPolicy:     tool.ParallelSerial,
		RepeatPolicy:       tool.RepeatExecute,
		SandboxRequirement: tool.SandboxStrong,
		Availability:       availability,
		UnavailableReason:  unavailableReason,
		InputSchema: map[string]any{
			"type": "object", "properties": properties, "required": required,
			"additionalProperties": false,
		},
	}
}

func ideDiscoveryTerms(kind string) []string {
	switch kind {
	case "lsp_hover":
		return []string{"hover", "type information", "悬停", "类型信息"}
	case "lsp_format_edits":
		return []string{"format document", "formatting", "格式化", "代码格式"}
	case "lsp_code_actions":
		return []string{"code action", "quick fix", "代码操作", "快速修复"}
	default:
		return []string{"rename symbol", "rename", "重命名符号", "重构"}
	}
}

func (t *ideTool) typedExecutor() (tool.Executor, error) {
	return typed.Define(typed.Spec[ideInput, diagnostics.IDEResult]{
		Descriptor:  t.Descriptor(),
		Disposition: tool.DispositionWaitForTeardown,
		Run: func(ctx context.Context, input ideInput) (diagnostics.IDEResult, error) {
			query := diagnostics.IDEQuery{
				Path: input.Path, Line: input.Line, Character: input.Character,
				EndLine: input.EndLine, EndCharacter: input.EndCharacter,
				NewName: input.NewName,
			}
			switch t.kind {
			case "lsp_hover":
				return t.checker.Hover(ctx, query)
			case "lsp_format_edits":
				return t.checker.Formatting(ctx, query)
			case "lsp_code_actions":
				return t.checker.CodeActions(ctx, query)
			case "lsp_rename_edits":
				return t.checker.Rename(ctx, query)
			default:
				return diagnostics.IDEResult{}, fmt.Errorf("unsupported LSP tool %q", t.kind)
			}
		},
		Metadata: func(result diagnostics.IDEResult) map[string]any {
			return map[string]any{"method": result.Method, "server": result.Server}
		},
	})
}

func languageServerResources(servers []string) []tool.ResourceTemplate {
	resources := []tool.ResourceTemplate{
		{Kind: "repo", ID: ".", Access: tool.AccessRead, Tree: true},
		{Kind: "process", ID: "lsp", Access: tool.AccessWrite, Tree: true},
	}
	for _, server := range servers {
		spec, err := diagnostics.ResolveServer(serverProbePath(server))
		if err == nil {
			resources = append(resources, tool.ResourceTemplate{
				Kind: "directory", ID: filepath.Dir(spec.Binary),
				Access: tool.AccessRead, Tree: true,
			})
		}
	}
	return resources
}
