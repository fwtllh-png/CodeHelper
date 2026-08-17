// Package extensiontest provides reusable contract checks for extension implementations.
package extensiontest

import (
	"context"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
)

type Harness struct {
	tb       testing.TB
	registry *extension.Registry
}

func New(tb testing.TB, values ...extension.Extension) *Harness {
	tb.Helper()
	builder := extension.NewBuilder()
	for _, value := range values {
		if err := builder.Register(value); err != nil {
			tb.Fatalf("register extension: %v", err)
		}
	}
	registry, err := builder.Build()
	if err != nil {
		tb.Fatalf("build extension registry: %v", err)
	}
	return &Harness{tb: tb, registry: registry}
}

func (h *Harness) Tool(
	ctx context.Context,
	id extension.ID,
	input extension.ToolInput,
) extension.Invocation[extension.ToolContribution] {
	h.tb.Helper()
	for _, binding := range h.registry.ToolContributors() {
		if binding.Descriptor().ID != id {
			continue
		}
		result, err := binding.Contribute(ctx, input)
		if err != nil {
			h.tb.Fatalf("invoke tool contributor %q: %v", id, err)
		}
		h.RequireReceipt(result.Receipt, binding.Descriptor(), extension.KindTool)
		return result
	}
	h.tb.Fatalf("tool contributor %q was not registered", id)
	return extension.Invocation[extension.ToolContribution]{}
}

func (h *Harness) RequireReceipt(
	receipt extension.Receipt,
	descriptor extension.Descriptor,
	kind extension.ContributorKind,
) {
	h.tb.Helper()
	if err := receipt.Validate(descriptor, kind); err != nil {
		h.tb.Fatalf("extension receipt: %v", err)
	}
}
