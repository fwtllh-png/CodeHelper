package tool

import (
	"context"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
)

type finishOnlyContextKey struct{}

func WithFinishOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, finishOnlyContextKey{}, true)
}

func FinishOnlyEnabled(ctx context.Context) bool {
	enabled, _ := ctx.Value(finishOnlyContextKey{}).(bool)
	return enabled
}

func FinishOnlyAllowed(name string, descriptor Descriptor) bool {
	if descriptor.Capability == CapabilityWrite {
		return true
	}
	switch name {
	case "turn_complete",
		"update_plan",
		"submit_plan",
		"request_user_input",
		"wait_agent",
		"list_agents",
		"file_read",
		"exec_command",
		"write_stdin",
		"quality_test",
		"quality_diagnostics",
		"quality_review",
		"quality_verify",
		"quality_process_smoke":
		return true
	default:
		return false
	}
}

func ConvergenceDefinitionAllowed(definition provider.ToolDefinition) bool {
	switch definition.Name {
	case "turn_complete", "request_user_input":
		return true
	default:
		return false
	}
}

// RemainingBusinessCalls reserves the current Sample and one follow-up when
// the advertised surface can produce more business work. Terminal-only calls
// do not need that follow-up.
func RemainingBusinessCalls(
	definitions []provider.ToolDefinition,
	terminalOnly bool,
) uint64 {
	const currentCall uint64 = 1
	if terminalOnly {
		return currentCall
	}
	for _, definition := range definitions {
		if !ConvergenceDefinitionAllowed(definition) {
			return currentCall + 1
		}
	}
	return currentCall
}

func FinishOnlyDefinitionAllowed(
	catalog CatalogSnapshot,
	definition provider.ToolDefinition,
) bool {
	entry, ok := catalog.Lookup(definition.Name)
	return ok && FinishOnlyAllowed(definition.Name, entry.Descriptor)
}
