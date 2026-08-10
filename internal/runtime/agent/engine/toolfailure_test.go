package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	mcpruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	skillruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

type unreadTool struct{}

type catalogFixtureTool string

func (t catalogFixtureTool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: string(t), Description: "catalog fixture", Visibility: tool.VisibleModel,
		Capability: tool.CapabilityRead, AccessMode: tool.AccessRead,
		ParallelPolicy: tool.ParallelConcurrent, SandboxRequirement: tool.SandboxNone,
		Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
	}
}

func (catalogFixtureTool) Execute(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "ok"}, nil
}

func (unreadTool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "unread", Description: "always reports read-before-edit",
		Visibility: tool.VisibleModel, Capability: tool.CapabilityRead,
		AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelConcurrent,
		SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
	}
}

func (unreadTool) Execute(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{}, &workspacejournal.ReadValidationError{
		Path: "sample.txt", Cause: workspacejournal.ErrUnread,
	}
}

func TestRecoverableToolFailureClassification(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&echoTool{}, nil); err != nil {
		t.Fatal(err)
	}
	_, _, _, unknownErr := registry.Resolve("missing")
	if unknownErr == nil {
		t.Fatal("Resolve() accepted an unknown tool")
	}
	_, schemaErr := tool.NormalizeArguments(
		(&echoTool{}).Descriptor().InputSchema, json.RawMessage(`{"text":42}`),
	)
	if schemaErr == nil {
		t.Fatal("NormalizeArguments() accepted a schema violation")
	}

	tests := map[string]struct {
		err             error
		wantRecoverable bool
		wantContains    string
	}{
		"approval denied": {
			err:             &policy.DecisionError{Code: "approval_denied", Reason: "user declined"},
			wantRecoverable: true, wantContains: "user declined",
		},
		"edit plan stale": {
			err: &policy.DecisionError{
				Code: "edit_plan_stale", Reason: "workspace changed after edit preview",
			},
			wantRecoverable: true, wantContains: "re-read",
		},
		"permission denied": {
			err: &policy.DecisionError{Code: "permission_denied", Reason: "write is denied"},
		},
		"mode denied": {
			err: &policy.DecisionError{Code: "mode_denied", Reason: "plan mode"},
		},
		"unread": {
			err: fmt.Errorf(
				"read-before-edit final check %q: %w", "a.go", workspacejournal.ErrUnread,
			),
			wantRecoverable: true, wantContains: "file_read",
		},
		"stale": {
			err: fmt.Errorf(
				"file read race %q: %w", "a.go", workspacejournal.ErrStale,
			),
			wantRecoverable: true, wantContains: "re-read",
		},
		"invalid arguments": {
			err:             fmt.Errorf("tool %q arguments: %w", "echo", schemaErr),
			wantRecoverable: true, wantContains: "arguments",
		},
		"unknown tool": {
			err: unknownErr, wantRecoverable: true, wantContains: "unknown tool",
		},
		"unavailable tool": {
			err: fmt.Errorf(
				"tool %q is %w: %s", "web_run", tool.ErrToolUnavailable, "driver missing",
			),
			wantRecoverable: true, wantContains: "driver missing",
		},
		"stale catalog": {
			err: fmt.Errorf(
				"%w for tool %q", tool.ErrCatalogStale, "echo",
			),
			wantRecoverable: true, wantContains: "catalog generation",
		},
		"revoked tool": {
			err: fmt.Errorf(
				"%w %q", tool.ErrToolRevoked, "echo",
			),
			wantRecoverable: true, wantContains: "revoked",
		},
		"MCP circuit open": {
			err: fmt.Errorf(
				"call MCP tool: %w", mcpruntime.ErrCircuitOpen,
			),
			wantRecoverable: true, wantContains: "circuit breaker",
		},
		"skill lock drift": {
			err:             fmt.Errorf("load skill: %w", skillruntime.ErrLockDrift),
			wantRecoverable: true, wantContains: "lock drift",
		},
		"precondition": {
			err: fmt.Errorf("tool %q: %w", "file_apply", tool.Precondition(
				errors.New("change 1 (edit b.py): old text matched 2 times, want exactly once"),
			)),
			wantRecoverable: true, wantContains: "the workspace was not changed",
		},
		"tool execution failure": {err: errors.New("intentional failure")},
		"canceled":               {err: context.Canceled},
		"nil":                    {},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			content, recoverable := recoverableToolFailure(test.err)
			if recoverable != test.wantRecoverable {
				t.Fatalf(
					"recoverableToolFailure(%v) recoverable = %v, want %v",
					test.err, recoverable, test.wantRecoverable,
				)
			}
			if !recoverable {
				if content != "" {
					t.Fatalf("aborting failure carried content %q", content)
				}
				return
			}
			if !strings.Contains(content, test.wantContains) {
				t.Fatalf("content = %q, want it to mention %q", content, test.wantContains)
			}
		})
	}
}

func TestEditPlanStaleRecoveryMetadataRequiresNewPlan(t *testing.T) {
	err := &policy.DecisionError{
		Code: "edit_plan_stale", Reason: "workspace changed after edit preview",
	}
	metadata := toolFailureRecoveryMetadata(err)
	if metadata["error_category"] != "edit_plan_stale" ||
		metadata["required_action"] != "file_read" ||
		metadata["retry_original"] != false ||
		metadata["approval_required"] != true {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestToolFailureRecoveryMetadataUsesStructuredEditHint(t *testing.T) {
	err := fmt.Errorf("plan workspace edit: %w", tool.Precondition(
		tool.WithRecoveryHint(errors.New("old text did not match"), tool.RecoveryHint{
			ErrorCategory:  "edit_precondition_failed",
			RequiredAction: "file_read",
			Path:           "docs/chapter.md",
			RetryOriginal:  false,
		}),
	))

	metadata := toolFailureRecoveryMetadata(err)

	if metadata["error_category"] != "edit_precondition_failed" ||
		metadata["required_action"] != "file_read" ||
		metadata["path"] != "docs/chapter.md" ||
		metadata["retry_original"] != false {
		t.Fatalf("metadata = %#v", metadata)
	}
}

// Read-before-edit violations are the model's own mistake, so runTools reports
// them as failed tool results and lets the turn continue.
func TestRunToolsFeedsRecoverableFailureBackToModel(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(unreadTool{}, nil); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, &scriptedProvider{}, registry)
	var emitted []tool.Result

	results, err := engine.runTools(
		t.Context(),
		"turn-test",
		[]provider.ToolCall{{ID: "call_1", Name: "unread", Arguments: `{}`}},
		make(map[string]tool.Result),
		func(_ State, event Event) error {
			if event.Result != nil {
				emitted = append(emitted, *event.Result)
			}
			return nil
		},
	)

	if err != nil {
		t.Fatalf("runTools() error = %v, want the failure fed back instead", err)
	}
	if len(results) != 1 || !results[0].IsError ||
		!strings.Contains(results[0].Content, "read-before-edit") {
		t.Fatalf("results = %+v", results)
	}
	if results[0].Metadata["error_category"] != "read_before_edit_required" ||
		results[0].Metadata["required_action"] != "file_read" ||
		results[0].Metadata["path"] != "sample.txt" ||
		results[0].Metadata["retry_original"] != true {
		t.Fatalf("recovery metadata = %#v", results[0].Metadata)
	}
	if len(emitted) != 1 || !emitted[0].IsError {
		t.Fatalf("emitted results = %+v", emitted)
	}
	if len(engine.TurnDiff()) != 0 {
		t.Fatalf("failed edit recorded a turn diff: %+v", engine.TurnDiff())
	}
}

func TestRunToolsCategorizesRevokedCatalogEntry(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	change, err := registry.Reconcile(
		"dynamic:test", 0,
		[]tool.Registration{tool.NewRegistration(&echoTool{})},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Revoke("dynamic:test", "echo", change.Generation)
	if err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, &scriptedProvider{}, registry)
	results, err := engine.runTools(
		t.Context(),
		"turn-test",
		[]provider.ToolCall{{ID: "call_1", Name: "echo", Arguments: `{"text":"hello"}`}},
		make(map[string]tool.Result),
		func(State, Event) error { return nil },
	)
	if err != nil {
		t.Fatalf("runTools() error = %v, want recoverable result", err)
	}
	if len(results) != 1 || !results[0].IsError {
		t.Fatalf("results = %+v", results)
	}
	if category, _ := results[0].Metadata["error_category"].(string); category != tool.ErrorCategoryToolRevoked {
		t.Fatalf("error_category = %q, want %q", category, tool.ErrorCategoryToolRevoked)
	}
}

func TestToolFailureCategoryIncludesMCPCircuit(t *testing.T) {
	err := fmt.Errorf("remote call: %w", mcpruntime.ErrCircuitOpen)
	if got := toolFailureCategory(err); got != mcpruntime.ErrorCategoryCircuitOpen {
		t.Fatalf("category = %q, want %q", got, mcpruntime.ErrorCategoryCircuitOpen)
	}
}

func TestToolFailureCategoryIncludesSkillDependency(t *testing.T) {
	err := fmt.Errorf("load skill: %w", skillruntime.ErrDependencyConflict)
	if got := toolFailureCategory(err); got != skillruntime.ErrorCategoryDependencyConflict {
		t.Fatalf("category = %q, want %q", got, skillruntime.ErrorCategoryDependencyConflict)
	}
}

func TestEngineFeedsSampledUnknownToolBackToModel(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				ID: "call_unknown", Name: "read", Arguments: `{"path":"README.md"}`,
			}},
			{Type: provider.EventMessageStop},
		}},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "recovered"},
			{Type: provider.EventMessageStop},
		}},
		textStream("recovered"),
	}}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&echoTool{}, nil); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, runtime, registry)

	result, err := engine.Run(t.Context(), "inspect the repository", nil)
	if err != nil {
		t.Fatalf("Run() error = %v, want the model to recover", err)
	}
	if result.State != Completed || result.Text != "recovered" {
		t.Fatalf("result = %+v", result)
	}
	if len(runtime.requests) != 3 {
		t.Fatalf("provider requests = %d, want 3", len(runtime.requests))
	}
	var failure *provider.ToolResult
	for _, message := range runtime.requests[1].Messages {
		for _, block := range message.Blocks {
			if block.ToolResult != nil && block.ToolResult.CallID == "call_unknown" {
				failure = block.ToolResult
			}
		}
	}
	if failure == nil || !failure.IsError || !strings.Contains(failure.Content, "unknown tool") {
		t.Fatalf("unknown-tool result = %+v", failure)
	}
}

func TestEngineDoesNotExecuteUnadvertisedCatalogTool(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				ID: "call_deferred", Name: "deferred_echo", Arguments: `{"text":"hello"}`,
			}},
			{Type: provider.EventMessageStop},
		}},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "recovered"},
			{Type: provider.EventMessageStop},
		}},
		textStream("recovered"),
	}}
	registry := tool.NewRegistry(nil, nil)
	descriptor := (&echoTool{}).Descriptor()
	descriptor.Name = "deferred_echo"
	loaded := false
	if err := registry.RegisterDeferred(descriptor, func() (tool.Executor, error) {
		loaded = true
		return nil, errors.New("loader must not run")
	}); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, runtime, registry)

	result, err := engine.Run(t.Context(), "inspect the repository", nil)
	if err != nil {
		t.Fatalf("Run() error = %v, want the model to recover", err)
	}
	if result.State != Completed || loaded {
		t.Fatalf("result = %+v loaded = %v", result, loaded)
	}
}

func TestToolSearchThresholdDoesNotTruncateEagerTools(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	const count = 30
	for index := range count {
		name := fmt.Sprintf("eager_%02d", index)
		if err := registry.Register(catalogFixtureTool(name), nil); err != nil {
			t.Fatal(err)
		}
	}
	engine := newEngine(t, &scriptedProvider{}, registry)
	engine.options.ToolSearchThreshold = 4

	definitions := engine.toolDefinitions()
	advertised := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		advertised[definition.Name] = true
	}
	for index := range count {
		name := fmt.Sprintf("eager_%02d", index)
		if !advertised[name] {
			t.Fatalf("eager tool %q was omitted from %d definitions", name, len(definitions))
		}
	}
}

func TestToolDefinitionsEnforceCountAndSchemaBudgets(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	for _, name := range []string{"first_budgeted", "second_budgeted"} {
		if err := registry.Register(catalogFixtureTool(name), nil); err != nil {
			t.Fatal(err)
		}
	}
	engine := newEngine(t, &scriptedProvider{}, registry)
	snapshot, err := registry.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	engine.options.MaxToolDefinitions = 1
	if _, _, err := engine.toolDefinitionsFromSnapshot(snapshot); !errors.Is(err, tool.ErrCatalogLimit) {
		t.Fatalf("count budget error = %v, want catalog limit", err)
	}
	engine.options.MaxToolDefinitions = 128
	engine.options.MaxToolSchemaBytes = 1
	if _, _, err := engine.toolDefinitionsFromSnapshot(snapshot); !errors.Is(err, tool.ErrCatalogLimit) {
		t.Fatalf("schema budget error = %v, want catalog limit", err)
	}
}
