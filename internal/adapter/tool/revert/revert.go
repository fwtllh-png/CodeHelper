// Package revert exposes model-visible workspace turn restore.
package revert

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

const UnavailableReason = "workspace revert is unavailable"

// Reverter restores workspace files for a prior turn.
type Reverter interface {
	Revert(ctx context.Context, targetTurnID string) (restored []string, conflicts []string, err error)
	DefaultTargetTurnID() (string, error)
}

// Options configures revert_turn registration.
type Options struct {
	Reverter Reverter
}

type Tool struct {
	reverter Reverter
}

type input struct {
	TargetTurnID string `json:"target_turn_id"`
}

// Register adds revert_turn. Without a Reverter the tool stays unavailable.
func Register(registry *tool.Registry, options Options) error {
	if registry == nil {
		return errors.New("registry is required")
	}
	return registry.Register(&Tool{reverter: options.Reverter}, nil)
}

func (t *Tool) Descriptor() tool.Descriptor {
	available := tool.AvailabilityUnavailable
	reason := UnavailableReason
	if t.reverter != nil {
		available = tool.AvailabilityAvailable
		reason = ""
	}
	return tool.Descriptor{
		Name: "revert_turn",
		Description: "Revert workspace file changes from a prior agent turn using the " +
			"workspace journal. Omitting target_turn_id restores the latest recorded turn.",
		Visibility: tool.VisibleModel, Capability: tool.CapabilityWrite,
		AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
		SandboxRequirement: tool.SandboxNone,
		Availability:       available, UnavailableReason: reason,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target_turn_id": map[string]any{"type": "string"},
			},
			"additionalProperties": false,
		},
	}
}

func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	if t.reverter == nil {
		return tool.Result{
			Content: UnavailableReason, IsError: true,
			Metadata: map[string]any{"error_category": "unavailable"},
		}, nil
	}
	var value input
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &value); err != nil {
			return tool.Result{}, err
		}
	}
	target := strings.TrimSpace(value.TargetTurnID)
	if target == "" {
		id, err := t.reverter.DefaultTargetTurnID()
		if err != nil {
			return tool.Result{
				Content: err.Error(), IsError: true,
				Metadata: map[string]any{"error_category": "invalid_argument"},
			}, nil
		}
		target = id
	}
	restored, conflicts, err := t.reverter.Revert(ctx, target)
	receipt := map[string]any{
		"schema_version": 1,
		"target_turn_id": target,
		"restored":       restored,
		"conflicts":      conflicts,
	}
	if restored == nil {
		receipt["restored"] = []string{}
	}
	if conflicts == nil {
		receipt["conflicts"] = []string{}
	}
	if err != nil {
		receipt["error"] = err.Error()
		content, marshalErr := json.Marshal(receipt)
		if marshalErr != nil {
			return tool.Result{}, marshalErr
		}
		return tool.Result{
			Content: string(content), IsError: true,
			Metadata: map[string]any{
				"error_category": "revert_failed",
				"target_turn_id": target,
				"restored":       restored,
				"conflicts":      conflicts,
			},
		}, nil
	}
	content, marshalErr := json.Marshal(receipt)
	if marshalErr != nil {
		return tool.Result{}, marshalErr
	}
	return tool.Result{
		Content: string(content),
		Metadata: map[string]any{
			"target_turn_id": target,
			"restored":       restored,
			"conflicts":      conflicts,
		},
	}, nil
}

// FakeReverter is a hermetic reverter for tests and tool-manifest generation.
type FakeReverter struct {
	DefaultID string
	Restored  []string
	Conflicts []string
	Err       error
	Calls     []string
}

func (f *FakeReverter) DefaultTargetTurnID() (string, error) {
	if f.DefaultID == "" {
		return "", errors.New("no turn to revert")
	}
	return f.DefaultID, nil
}

func (f *FakeReverter) Revert(_ context.Context, targetTurnID string) ([]string, []string, error) {
	f.Calls = append(f.Calls, targetTurnID)
	return append([]string{}, f.Restored...), append([]string{}, f.Conflicts...), f.Err
}

// EngineReverter adapts an agent engine with workspace journal revert.
type EngineReverter struct {
	RevertFn      func(ctx context.Context, targetTurnID string) (restored []string, conflicts []string, err error)
	DefaultTurnFn func() (string, error)
}

func (e EngineReverter) Revert(ctx context.Context, targetTurnID string) ([]string, []string, error) {
	if e.RevertFn == nil {
		return nil, nil, errors.New("revert is not configured")
	}
	return e.RevertFn(ctx, targetTurnID)
}

func (e EngineReverter) DefaultTargetTurnID() (string, error) {
	if e.DefaultTurnFn == nil {
		return "", errors.New("no turn to revert")
	}
	return e.DefaultTurnFn()
}
