package completion

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

const Name = "turn_complete"

type Tool struct{}

func Register(registry *tool.Registry) error {
	return registry.Register(&Tool{}, nil)
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

func (*Tool) Execute(_ context.Context, raw json.RawMessage) (tool.Result, error) {
	var declaration tool.CompletionDeclaration
	if err := json.Unmarshal(raw, &declaration); err != nil {
		return tool.Result{}, err
	}
	switch declaration.Status {
	case "complete":
		if len(declaration.PendingActions) != 0 {
			return tool.Result{}, errors.New(
				"complete declaration cannot contain pending actions",
			)
		}
	case "incomplete":
		if len(declaration.PendingActions) == 0 {
			return tool.Result{}, errors.New(
				"incomplete declaration requires pending actions",
			)
		}
	default:
		return tool.Result{}, errors.New("unsupported completion status")
	}
	for _, action := range declaration.PendingActions {
		if strings.TrimSpace(action) == "" {
			return tool.Result{}, errors.New("pending action cannot be empty")
		}
	}
	content, err := json.Marshal(map[string]any{
		"status":  "pending_runtime_validation",
		"message": "completion declaration submitted for runtime validation",
	})
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(content),
		Metadata: map[string]any{
			tool.MetadataCompletionDeclaration: declaration,
		},
	}, nil
}
