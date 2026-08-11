package completion

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolresult "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/result"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/typed"
)

const Name = "turn_complete"

type Tool struct{}

type output struct {
	Status      string                     `json:"status"`
	Message     string                     `json:"message"`
	Declaration tool.CompletionDeclaration `json:"-"`
}

func Register(registry *tool.Registry) error {
	executor, err := (&Tool{}).typedExecutor()
	if err != nil {
		return err
	}
	return registry.Register(executor, nil)
}

func (*Tool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: Name,
		Description: "Report the state of tool-assisted work. Use status=complete only after " +
			"every pending action, the last mutation, and all required quality checks; set " +
			"pending_actions to an empty array. If work remains, use status=incomplete with " +
			"the concrete pending actions so the runtime continues the current turn.",
		Visibility:         tool.VisibleModel,
		Capability:         tool.CapabilityRead,
		AccessMode:         tool.AccessRead,
		ParallelPolicy:     tool.ParallelSerial,
		RepeatPolicy:       tool.RepeatExecute,
		SandboxRequirement: tool.SandboxNone,
		Availability:       tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": map[string]any{
					"type": "string", "enum": []string{"complete", "incomplete"},
				},
				"summary": map[string]any{
					"type": "string", "minLength": 1, "maxLength": 4096,
				},
				"pending_actions": map[string]any{
					"type": "array", "maxItems": 32,
					"items": map[string]any{
						"type": "string", "minLength": 1, "maxLength": 256,
					},
				},
			},
			"required": []string{
				"status", "summary", "pending_actions",
			},
			"additionalProperties": false,
		},
	}
}

func (*Tool) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	executor, err := (&Tool{}).typedExecutor()
	if err != nil {
		return tool.Result{}, err
	}
	return executor.Execute(ctx, raw)
}

func (t *Tool) typedExecutor() (tool.Executor, error) {
	return typed.Define(typed.Spec[tool.CompletionDeclaration, output]{
		Descriptor: t.Descriptor(),
		Validate:   validateDeclaration,
		Run: func(_ context.Context, declaration tool.CompletionDeclaration) (output, error) {
			return output{
				Status:      "pending_runtime_validation",
				Message:     "completion declaration submitted for runtime validation",
				Declaration: declaration,
			}, nil
		},
		Encode: func(value output) (tool.Result, error) {
			return toolresult.Success(value, nil)
		},
		Metadata: func(value output) map[string]any {
			return map[string]any{tool.MetadataCompletionDeclaration: value.Declaration}
		},
	})
}

func validateDeclaration(declaration tool.CompletionDeclaration) error {
	switch declaration.Status {
	case "complete":
		if len(declaration.PendingActions) != 0 {
			return errors.New(
				"complete declaration cannot contain pending actions",
			)
		}
	case "incomplete":
		if len(declaration.PendingActions) == 0 {
			return errors.New(
				"incomplete declaration requires pending actions",
			)
		}
	default:
		return errors.New("unsupported completion status")
	}
	for _, action := range declaration.PendingActions {
		if strings.TrimSpace(action) == "" {
			return errors.New("pending action cannot be empty")
		}
	}
	return nil
}
