package hooks

import (
	"bytes"
	"context"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
)

// Adapter bridges lifecycle hooks to the existing guard boundary. New wiring
// should call Manager.ToolCallBefore directly so updatedInput can be prepared
// again and ask can enter the host approval flow.
type Adapter struct {
	Manager *Manager
}

func (a *Adapter) Before(ctx context.Context, invocation toolguard.Invocation) error {
	if a == nil || a.Manager == nil {
		return nil
	}
	result, err := a.Manager.ToolCallBefore(ctx, ToolCallBeforeInput{
		CallID: invocation.CallID,
		Tool:   invocation.Tool,
		Input:  invocation.Arguments,
	})
	if err != nil {
		return err
	}
	if result.Action == ActionDeny || result.Action == ActionAsk ||
		(result.HookID != "" && !bytes.Equal(result.UpdatedInput, invocation.Arguments)) {
		return &BeforeError{Result: result}
	}
	return nil
}

func (a *Adapter) After(
	ctx context.Context,
	invocation toolguard.Invocation,
	result tool.Result,
	executeErr error,
) {
	if a == nil || a.Manager == nil {
		return
	}
	input := ToolCallAfterInput{
		CallID: invocation.CallID,
		Tool:   invocation.Tool,
		Input:  invocation.Arguments,
		Result: result,
	}
	if executeErr != nil {
		input.Error = executeErr.Error()
	}
	a.Manager.ToolCallAfter(ctx, input)
}

// PermissionRequest implements toolguard.PermissionRequester (N20).
func (a *Adapter) PermissionRequest(
	ctx context.Context, invocation toolguard.Invocation,
) (toolguard.PermissionDecision, error) {
	if a == nil || a.Manager == nil {
		return toolguard.PermissionDecision{}, nil
	}
	result, err := a.Manager.PermissionRequest(ctx, ToolCallBeforeInput{
		CallID: invocation.CallID,
		Tool:   invocation.Tool,
		Input:  invocation.Arguments,
	})
	if err != nil {
		return toolguard.PermissionDecision{}, err
	}
	action := toolguard.PermissionAction("")
	switch result.Action {
	case ActionAllow:
		action = toolguard.PermissionAllow
	case ActionDeny:
		action = toolguard.PermissionDeny
	case ActionAsk:
		action = toolguard.PermissionAsk
	}
	return toolguard.PermissionDecision{
		Action: action, Reason: result.Reason, HookID: result.HookID,
	}, nil
}

var _ toolguard.Hooks = (*Adapter)(nil)
var _ toolguard.PermissionRequester = (*Adapter)(nil)
