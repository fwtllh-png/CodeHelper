package extension

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

func TestBuilderCollectsTypedContributorsAndSeals(t *testing.T) {
	value := &allContributor{descriptor: testDescriptor("fixture")}
	builder := NewBuilder()
	if err := builder.Register(value); err != nil {
		t.Fatal(err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Descriptors()) != 1 ||
		len(registry.ThreadContributors()) != 1 ||
		len(registry.TurnContributors()) != 1 ||
		len(registry.ContextContributors()) != 1 ||
		len(registry.ToolContributors()) != 1 ||
		len(registry.MCPContributors()) != 1 {
		t.Fatalf("registry contributor counts are incomplete: %+v", registry)
	}
	if err := builder.Register(&allContributor{
		descriptor: testDescriptor("late"),
	}); !errors.Is(err, ErrBuilderSealed) {
		t.Fatalf("register after Build() error = %v", err)
	}
	if _, err := builder.Build(); !errors.Is(err, ErrBuilderSealed) {
		t.Fatalf("second Build() error = %v", err)
	}
	descriptors := registry.Descriptors()
	descriptors[0].Version = "mutated"
	if registry.Descriptors()[0].Version == "mutated" {
		t.Fatal("registry descriptor snapshot is mutable")
	}
}

func TestBuilderRejectsDuplicateInvalidAndEmptyExtensions(t *testing.T) {
	builder := NewBuilder()
	if err := builder.Register(&allContributor{
		descriptor: testDescriptor("fixture"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := builder.Register(&allContributor{
		descriptor: testDescriptor("fixture"),
	}); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("duplicate error = %v", err)
	}
	invalid := testDescriptor("invalid")
	invalid.Budget.Timeout = 0
	if err := NewBuilder().Register(&allContributor{
		descriptor: invalid,
	}); err == nil {
		t.Fatal("invalid budget was accepted")
	}
	if err := NewBuilder().Register(descriptorOnly{
		descriptor: testDescriptor("empty"),
	}); err == nil {
		t.Fatal("extension without contributor contract was accepted")
	}
}

func TestToolInvocationOwnsDeadlineOutcomeAndReceipt(t *testing.T) {
	value := &allContributor{descriptor: testDescriptor("fixture")}
	registry := buildRegistry(t, value)
	binding := registry.ToolContributors()[0]
	result, err := binding.Contribute(t.Context(), ToolInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !value.deadlineSeen {
		t.Fatal("tool contributor did not receive a deadline")
	}
	if result.Outcome.Status != OutcomeSucceeded ||
		len(result.Receipt.Outputs) != 1 ||
		result.Receipt.Outputs[0] != "fixture_tool" {
		t.Fatalf("tool invocation = %+v", result)
	}
	if err := result.Receipt.Validate(
		value.descriptor,
		KindTool,
	); err != nil {
		t.Fatal(err)
	}
}

func TestInvocationEnforcesOutputBudgetAndFailurePolicy(t *testing.T) {
	limited := &allContributor{descriptor: testDescriptor("limited"), toolCount: 2}
	registry := buildRegistry(t, limited)
	if _, err := registry.ToolContributors()[0].Contribute(
		t.Context(),
		ToolInput{},
	); err == nil {
		t.Fatal("output budget overflow succeeded")
	}

	failed := &allContributor{
		descriptor: testDescriptor("failed"),
		outcome:    Failure("fixture_failure", errors.New("failed")),
	}
	registry = buildRegistry(t, failed)
	if _, err := registry.ToolContributors()[0].Contribute(
		t.Context(),
		ToolInput{},
	); err == nil {
		t.Fatal("fail-closed outcome succeeded")
	}
	failed.descriptor.FailurePolicy = FailureIsolate
	registry = buildRegistry(t, failed)
	result, err := registry.ToolContributors()[0].Contribute(t.Context(), ToolInput{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome.Status != OutcomeFailed ||
		result.Receipt.Code != "fixture_failure" {
		t.Fatalf("isolated result = %+v", result)
	}
}

func TestNoopRegistryIsImmutableAndEmpty(t *testing.T) {
	registry := NewNoopRegistry()
	if len(registry.Descriptors()) != 0 ||
		len(registry.ThreadContributors()) != 0 ||
		len(registry.TurnContributors()) != 0 ||
		len(registry.ContextContributors()) != 0 ||
		len(registry.ToolContributors()) != 0 ||
		len(registry.MCPContributors()) != 0 {
		t.Fatal("noop extension registry is not empty")
	}
}

func buildRegistry(t *testing.T, values ...Extension) *Registry {
	t.Helper()
	builder := NewBuilder()
	for _, value := range values {
		if err := builder.Register(value); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func testDescriptor(id ID) Descriptor {
	return Descriptor{
		ID: id, Version: "builtin",
		FailurePolicy: FailureFailClosed,
		Budget:        Budget{Timeout: time.Second, MaxOutputs: 1},
	}
}

type descriptorOnly struct {
	descriptor Descriptor
}

func (d descriptorOnly) Descriptor() Descriptor { return d.descriptor }

type allContributor struct {
	descriptor   Descriptor
	deadlineSeen bool
	toolCount    int
	outcome      Outcome
}

func (c *allContributor) Descriptor() Descriptor { return c.descriptor }

func (c *allContributor) OnThreadStart(ctx context.Context, _ ThreadInput) Outcome {
	c.observeDeadline(ctx)
	return c.effectiveOutcome()
}

func (c *allContributor) OnThreadResume(ctx context.Context, _ ThreadInput) Outcome {
	c.observeDeadline(ctx)
	return c.effectiveOutcome()
}

func (c *allContributor) OnThreadStop(ctx context.Context, _ ThreadInput) Outcome {
	c.observeDeadline(ctx)
	return c.effectiveOutcome()
}

func (c *allContributor) OnTurnStart(ctx context.Context, _ TurnInput) Outcome {
	c.observeDeadline(ctx)
	return c.effectiveOutcome()
}

func (c *allContributor) OnTurnStop(ctx context.Context, _ TurnInput) Outcome {
	c.observeDeadline(ctx)
	return c.effectiveOutcome()
}

func (c *allContributor) OnTurnAbort(ctx context.Context, _ TurnInput) Outcome {
	c.observeDeadline(ctx)
	return c.effectiveOutcome()
}

func (c *allContributor) ContributeContext(
	ctx context.Context,
	_ ContextInput,
) ([]ContextItem, Outcome) {
	c.observeDeadline(ctx)
	return []ContextItem{{ID: "fixture_context"}}, c.effectiveOutcome()
}

func (c *allContributor) ContributeTools(
	ctx context.Context,
	_ ToolInput,
) (ToolContribution, Outcome) {
	c.observeDeadline(ctx)
	count := c.toolCount
	if count == 0 {
		count = 1
	}
	registrations := make([]tool.Registration, 0, count)
	for index := range count {
		name := "fixture_tool"
		if count > 1 {
			name += "_" + string(rune('a'+index))
		}
		registrations = append(
			registrations,
			tool.NewRegistration(testTool{name: name}),
		)
	}
	return ToolContribution{Registrations: registrations}, c.effectiveOutcome()
}

func (c *allContributor) ContributeMCP(
	ctx context.Context,
	_ MCPInput,
) ([]MCPContribution, Outcome) {
	c.observeDeadline(ctx)
	return []MCPContribution{{ID: "fixture_mcp"}}, c.effectiveOutcome()
}

func (c *allContributor) observeDeadline(ctx context.Context) {
	_, c.deadlineSeen = ctx.Deadline()
}

func (c *allContributor) effectiveOutcome() Outcome {
	if c.outcome.Status == "" {
		return Success()
	}
	return c.outcome
}

type testTool struct {
	name string
}

func (t testTool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: t.name, Description: "fixture",
		InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
		},
		Visibility:         tool.VisibleModel,
		Capability:         tool.CapabilityRead,
		AccessMode:         tool.AccessRead,
		ParallelPolicy:     tool.ParallelConcurrent,
		SandboxRequirement: tool.SandboxNone,
		Availability:       tool.AvailabilityAvailable,
	}
}

func (testTool) Execute(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "ok"}, nil
}
