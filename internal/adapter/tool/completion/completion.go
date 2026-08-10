package completion

import (
	"context"
	"encoding/json"

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
		Description: "Declare that a workspace change is complete. Call only after the last " +
			"mutation and all required quality checks; then provide the user-facing final answer.",
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
					"type": "string", "enum": []string{"complete"},
				},
				"summary": map[string]any{
					"type": "string", "minLength": 1, "maxLength": 4096,
				},
				"changed_paths": map[string]any{
					"type": "array", "minItems": 1, "maxItems": 512,
					"uniqueItems": true,
					"items":       map[string]any{"type": "string", "minLength": 1},
				},
				"verification_call_ids": map[string]any{
					"type": "array", "maxItems": 128, "uniqueItems": true,
					"items": map[string]any{"type": "string", "minLength": 1},
				},
				"pending_actions": map[string]any{
					"type": "array", "maxItems": 0,
					"items": map[string]any{"type": "string"},
				},
			},
			"required": []string{
				"status", "summary", "changed_paths",
				"verification_call_ids", "pending_actions",
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
	content, err := json.Marshal(map[string]any{
		"status":  "recorded",
		"message": "completion declaration recorded; provide the final answer",
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
