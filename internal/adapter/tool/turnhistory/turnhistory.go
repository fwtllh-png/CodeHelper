package turnhistory

import (
	"context"
	"errors"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/typed"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
)

const Name = agentcontext.TurnHistoryToolName

type Lookup func(turn uint64) ([]provider.Message, error)

type input struct {
	Turn     uint64 `json:"turn"`
	From     string `json:"from,omitempty"`
	MaxBytes int    `json:"max_bytes,omitempty"`
}

func Register(registry *tool.Registry, lookup Lookup) error {
	if registry == nil {
		return errors.New("turn_history requires a registry")
	}
	if lookup == nil {
		return errors.New("turn_history requires a history lookup")
	}
	if _, _, _, err := registry.Resolve(Name); err == nil {
		return nil
	}
	executor, err := typed.Define(typed.Spec[input, tool.Result]{
		Descriptor: tool.Descriptor{
			Name: Name,
			Description: "Read a closed turn's durable transcript by turn id. " +
				"The first page is the turn tail (final conclusions, audit " +
				"lists, P2s). Use from=head only for the start of the turn. " +
				"If truncated, page with result_get mode=tail or mode=query " +
				"(for example query=P2); default result_get mode=summary " +
				"does not reconstruct lists. Do not search the workspace " +
				"for conversation-only lists. First write is final.",
			Visibility:         tool.VisibleModel,
			Capability:         tool.CapabilityRead,
			AccessMode:         tool.AccessRead,
			ParallelPolicy:     tool.ParallelConcurrent,
			RepeatPolicy:       tool.RepeatReplaySameTurn,
			SandboxRequirement: tool.SandboxNone,
			Availability:       tool.AvailabilityAvailable,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"turn": map[string]any{"type": "integer", "minimum": float64(1)},
					"from": map[string]any{
						"type": "string", "enum": []any{"tail", "head"},
					},
					"max_bytes": map[string]any{"type": "integer", "minimum": float64(0)},
				},
				"required":             []string{"turn"},
				"additionalProperties": false,
			},
		},
		Disposition: tool.DispositionWaitForTeardown,
		Validate: func(value input) error {
			if value.Turn == 0 {
				return errors.New("turn is required")
			}
			if value.From != "" && value.From != "tail" && value.From != "head" {
				return errors.New("from must be tail or head")
			}
			if value.MaxBytes < 0 {
				return errors.New("max_bytes must not be negative")
			}
			return nil
		},
		Run: func(_ context.Context, value input) (tool.Result, error) {
			messages, err := lookup(value.Turn)
			if err != nil {
				return tool.Result{}, err
			}
			if len(messages) == 0 {
				return tool.Result{}, fmt.Errorf("turn %d is not in durable history", value.Turn)
			}
			full := agentcontext.RenderTurnTranscript(messages)
			content := full
			if value.MaxBytes > 0 && len(full) > value.MaxBytes {
				if value.From == "head" {
					content = agentcontext.TruncateUTF8(full, value.MaxBytes)
				} else {
					content = agentcontext.TruncateUTF8Tail(full, value.MaxBytes)
				}
				return tool.Result{
					Content: content, Truncated: true, OriginalBytes: len(full),
				}, nil
			}
			return tool.Result{Content: content}, nil
		},
		Encode: func(value tool.Result) (tool.Result, error) {
			return value, nil
		},
	})
	if err != nil {
		return err
	}
	return registry.Register(executor)
}
