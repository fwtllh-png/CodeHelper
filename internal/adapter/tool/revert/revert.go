// Package revert exposes model-visible workspace turn restore.
package revert

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolresult "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/result"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/typed"
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
	instance := &Tool{reverter: options.Reverter}
	executor, err := instance.typedExecutor()
	if err != nil {
		return err
	}
	return registry.Register(executor, nil)
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
		RepeatPolicy:       tool.RepeatExecute,
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
	executor, err := t.typedExecutor()
	if err != nil {
		return tool.Result{}, err
	}
	return executor.Execute(ctx, raw)
}

func (t *Tool) typedExecutor() (tool.Executor, error) {
	return typed.Define(typed.Spec[input, tool.Result]{
		Descriptor:  t.Descriptor(),
		Disposition: tool.DispositionWaitForTeardown,
		Run:         t.run,
		Encode: func(value tool.Result) (tool.Result, error) {
			return value, nil
		},
	})
}

func (t *Tool) run(ctx context.Context, value input) (tool.Result, error) {
	if t.reverter == nil {
		return toolresult.Unavailable(UnavailableReason), nil
	}
	target := strings.TrimSpace(value.TargetTurnID)
	if target == "" {
		id, err := t.reverter.DefaultTargetTurnID()
		if err != nil {
			return toolresult.Fail(toolresult.Failure{
				Category: "invalid_argument", Message: err.Error(),
			}), nil
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
		result, encodeErr := toolresult.Success(receipt, map[string]any{
			"error_category": "revert_failed",
			"target_turn_id": target,
			"restored":       restored,
			"conflicts":      conflicts,
		})
		result.IsError = true
		return result, encodeErr
	}
	return toolresult.Success(receipt, map[string]any{
		"target_turn_id": target,
		"restored":       restored,
		"conflicts":      conflicts,
	})
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
