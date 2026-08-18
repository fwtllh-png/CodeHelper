package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	mcpruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerfixture "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/fixture"
	skillruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/toolsearch"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

type unreadTool struct{}
type missingPathTool struct{}

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

func (missingPathTool) Descriptor() tool.Descriptor {
	return catalogFixtureTool("missing_path").Descriptor()
}

func (missingPathTool) Execute(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{}, tool.Precondition(tool.WithRecoveryHint(
		errors.New("file does not exist"),
		tool.RecoveryHint{
			ErrorCategory:  "file_not_found",
			RequiredAction: "file_list",
			Path:           "docs/book/_templates/chapter.md",
			RetryOriginal:  false,
		},
	))
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
		"skill not selected": {
			err:             skillruntime.ErrNotSelected,
			wantRecoverable: true, wantContains: "catalog snapshot",
		},
		"skill handle invalid": {
			err: tool.WithRecoveryHint(
				skillruntime.ErrSkillHandleInvalid,
				tool.RecoveryHint{
					ErrorCategory:  skillruntime.ErrorCategoryHandleInvalid,
					RequiredAction: "skills_list",
					RetryOriginal:  false,
				},
			),
			wantRecoverable: true, wantContains: "required_action=skills_list",
		},
		"precondition": {
			err: fmt.Errorf("tool %q: %w", "file_apply", tool.Precondition(
				errors.New("change 1 (edit b.py): old text matched 2 times, want exactly once"),
			)),
			wantRecoverable: true, wantContains: "the workspace was not changed",
		},
		"missing file recovery hint": {
			err: tool.Precondition(tool.WithRecoveryHint(
				errors.New("file does not exist"),
				tool.RecoveryHint{
					ErrorCategory:  "file_not_found",
					RequiredAction: "file_list",
					Path:           "docs/book/_templates/chapter.md",
					RetryOriginal:  false,
				},
			)),
			wantRecoverable: true, wantContains: "required_action=file_list",
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

func TestRecoverableToolResultPreservesGuardExecutionReceipt(t *testing.T) {
	receipt := &tool.ExecutionReceipt{
		Tool: tool.ToolRef{
			Name: "exec_command", Source: "builtin:exec_command",
			CatalogID: "catalog-1", Generation: 1, Revision: 1, Authority: 1,
		},
		Source:         tool.InvocationSourceModel,
		Disposition:    tool.DispositionWaitForTeardown,
		TerminalStatus: tool.OutcomeRejected,
		TerminalOwner:  tool.TerminalOwnerGuard,
	}
	result, recovered := recoverableToolResult(tool.Result{
		Execution: receipt,
		Metadata:  map[string]any{"guard": "retained"},
	}, &policy.DecisionError{
		Code: "approval_denied", Reason: "approval was denied",
	})
	if !recovered || !result.IsError ||
		!strings.Contains(result.Content, "approval was denied") ||
		result.Execution != receipt ||
		result.Execution.TerminalStatus != tool.OutcomeRejected ||
		result.Execution.TerminalOwner != tool.TerminalOwnerGuard ||
		result.Metadata["guard"] != "retained" {
		t.Fatalf("recovered result = %+v", result)
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
			RequiredAction: "replace_failed_change",
			Path:           "docs/chapter.md",
			RetryOriginal:  false,
			FailedChange:   6,
			MatchCount:     0,
			StartLine:      74,
			EndLine:        80,
			CurrentExcerpt: "current text",
		}),
	))

	metadata := toolFailureRecoveryMetadata(err)

	if metadata["error_category"] != "edit_precondition_failed" ||
		metadata["required_action"] != "replace_failed_change" ||
		metadata["path"] != "docs/chapter.md" ||
		metadata["retry_original"] != false ||
		metadata["failed_change"] != 6 ||
		metadata["match_count"] != 0 ||
		metadata["start_line"] != 74 ||
		metadata["end_line"] != 80 ||
		metadata["current_excerpt"] != "current text" {
		t.Fatalf("metadata = %#v", metadata)
	}

	content, recoverable := recoverableToolFailure(err)
	if !recoverable ||
		!strings.Contains(content, "failed_change=6; match_count=0") ||
		!strings.Contains(content, "current_excerpt_lines=74-80:\ncurrent text") {
		t.Fatalf("content = %q, recoverable = %v", content, recoverable)
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

func TestMissingPathRecoveryExposesExactCandidatesToModel(t *testing.T) {
	err := tool.Precondition(tool.WithRecoveryHint(
		errors.New("file does not exist"),
		tool.RecoveryHint{
			ErrorCategory:  "file_not_found",
			RequiredAction: "use_existing_path",
			Path:           "docs/01-prompt-context.md",
			RetryOriginal:  false,
			CandidatePaths: []string{
				"docs/01-prompt-message-context.md",
				"docs/02-workspace-index-editor.md",
			},
		},
	))

	content, recoverable := recoverableToolFailure(err)
	if !recoverable ||
		!strings.Contains(
			content,
			"candidate_paths=docs/01-prompt-message-context.md,"+
				"docs/02-workspace-index-editor.md",
		) {
		t.Fatalf("content = %q, recoverable = %v", content, recoverable)
	}
	metadata := toolFailureRecoveryMetadata(err)
	candidates, ok := metadata["candidate_paths"].([]string)
	if !ok || len(candidates) != 2 ||
		candidates[0] != "docs/01-prompt-message-context.md" {
		t.Fatalf("candidate metadata = %#v", metadata["candidate_paths"])
	}
}

func TestRunToolsFeedsMissingPathRecoveryBackToModel(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(missingPathTool{}, nil); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, &scriptedProvider{}, registry)

	results, err := engine.runTools(
		t.Context(),
		"turn-test",
		[]provider.ToolCall{{
			ID: "call_missing", Name: "missing_path", Arguments: `{}`,
		}},
		make(map[string]tool.Result),
		func(State, Event) error { return nil },
	)

	if err != nil {
		t.Fatalf("runTools() error = %v, want recoverable result", err)
	}
	if len(results) != 1 || !results[0].IsError ||
		!strings.Contains(results[0].Content, "required_action=file_list") {
		t.Fatalf("results = %+v", results)
	}
	if results[0].Metadata["error_category"] != "file_not_found" ||
		results[0].Metadata["required_action"] != "file_list" ||
		results[0].Metadata["path"] != "docs/book/_templates/chapter.md" ||
		results[0].Metadata["retry_original"] != false {
		t.Fatalf("recovery metadata = %#v", results[0].Metadata)
	}
}

func TestRunToolsFeedsMissingResultHandleBackToModel(t *testing.T) {
	engine := newEngine(
		t,
		&scriptedProvider{},
		tool.NewRegistry(nil, tool.NewResultStore(32<<10)),
	)
	results, err := engine.runTools(
		t.Context(),
		"turn-test",
		[]provider.ToolCall{{
			ID:        "call_missing_result",
			Name:      "result_get",
			Arguments: `{"handle":"tool-call-id"}`,
		}},
		make(map[string]tool.Result),
		func(State, Event) error { return nil },
	)
	if err != nil {
		t.Fatalf("runTools() error = %v, want recoverable result", err)
	}
	if len(results) != 1 || !results[0].IsError ||
		results[0].Metadata["error_category"] != "result_handle_not_found" ||
		results[0].Metadata["required_action"] != "use_advertised_result_handle" ||
		results[0].Metadata["retry_original"] != false {
		t.Fatalf("results = %+v", results)
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
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				ID: "call_unknown", Name: "read", Arguments: `{"path":"README.md"}`,
			}},
			{Type: provider.EventMessageStop},
		}},
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
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
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				ID: "call_deferred", Name: "deferred_echo", Arguments: `{"text":"hello"}`,
			}},
			{Type: provider.EventMessageStop},
		}},
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
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

func TestCatalogWithoutToolSearchDoesNotTruncateEagerTools(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	const count = 30
	for index := range count {
		name := fmt.Sprintf("eager_%02d", index)
		if err := registry.Register(catalogFixtureTool(name), nil); err != nil {
			t.Fatal(err)
		}
	}
	engine := newEngine(t, &scriptedProvider{}, registry)

	definitions := testToolDefinitions(t, engine)
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
	if _, _, err := engine.toolDefinitionsFromSnapshot(snapshot, TurnRequest{}); !errors.Is(err, tool.ErrCatalogLimit) {
		t.Fatalf("count budget error = %v, want catalog limit", err)
	}
	engine.options.MaxToolDefinitions = 128
	engine.options.MaxToolSchemaBytes = 1
	if _, _, err := engine.toolDefinitionsFromSnapshot(snapshot, TurnRequest{}); !errors.Is(err, tool.ErrCatalogLimit) {
		t.Fatalf("schema budget error = %v, want catalog limit", err)
	}
}

func TestToolSelectionKeepsCoreAndBoundedRelevantDefinitions(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := toolsearch.Register(registry); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"search_text", "search_files", "search_definition", "search_references",
		"file_read", "file_list", "file_write", "file_edit", "file_apply",
		"shell_read", "exec_command", "write_stdin", "quality_test",
		"quality_verify", "project_map", "special_deploy", "unrelated_fixture",
	} {
		if err := registry.Register(catalogFixtureTool(name), nil); err != nil {
			t.Fatal(err)
		}
	}
	engine := newEngine(t, &scriptedProvider{}, registry)
	snapshot, err := registry.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	request := TurnRequest{Prompt: "deploy the release"}
	first, advertised, err := engine.toolDefinitionsFromSnapshot(snapshot, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"tool_search", "search_text", "file_read", "file_write",
		"exec_command", "write_stdin", "quality_test", "quality_verify",
		"special_deploy",
	} {
		if !advertised[name] {
			t.Fatalf("required or relevant tool %q omitted from %v", name, advertised)
		}
	}
	if advertised["unrelated_fixture"] {
		t.Fatalf("unrelated tool was advertised: %v", advertised)
	}
	encoded, _ := json.Marshal(first)
	if tokens := (len(encoded) + 3) / 4; tokens > 4000 {
		t.Fatalf("initial tool definitions = %d tokens, want <= 4000", tokens)
	}
	second, _, err := engine.toolDefinitionsFromSnapshot(snapshot, request)
	if err != nil {
		t.Fatal(err)
	}
	repeated, _ := json.Marshal(second)
	if string(repeated) != string(encoded) {
		t.Fatal("unchanged selection changed provider definitions")
	}
}

func TestCatalogReceiptUsesLastProviderToolDefinitions(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	for _, name := range []string{
		"turn_complete", "update_plan", "quality_test", "shell_read", "exec_command",
	} {
		if err := registry.Register(catalogFixtureTool(name), nil); err != nil {
			t.Fatal(err)
		}
	}
	engine := newEngine(t, &scriptedProvider{}, registry)
	scope := attachTestScope(t, engine)
	snapshot, err := registry.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	scope.spec.Catalog = snapshot
	engine.recordSampledTools(scope, snapshot, []provider.ToolDefinition{
		{Name: "turn_complete"}, {Name: "update_plan"}, {Name: "quality_test"},
	})

	receipt := engine.CatalogReceipt()
	if receipt == nil ||
		slices.Contains(receipt.Advertised, "shell_read") ||
		slices.Contains(receipt.Advertised, "exec_command") ||
		!slices.Contains(receipt.Advertised, "turn_complete") ||
		receipt.OmittedCount != 3 {
		t.Fatalf("catalog receipt = %+v", receipt)
	}
}

func TestToolSearchRefreshesScopeCatalogAndBinding(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := toolsearch.Register(registry); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(catalogFixtureTool("special_deploy"), nil); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, &scriptedProvider{}, registry)
	before, err := registry.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	scope := attachTestScope(t, engine)
	scope.spec.Catalog = before
	if changed := engine.catalogChange(before); changed == nil ||
		len(changed.Added) == 0 {
		t.Fatalf("initial catalog change = %+v", changed)
	}
	entry, _ := before.Lookup("special_deploy")
	if _, err := registry.Materialize(entry.Name, entry.Revision); err != nil {
		t.Fatal(err)
	}
	if err := engine.refreshScopeCatalog(); err != nil {
		t.Fatal(err)
	}
	current := engine.scopeCatalog(scope)
	changed := engine.catalogChange(current)
	if changed == nil || len(changed.Replaced) != 1 ||
		changed.Replaced[0].Name != "special_deploy" {
		t.Fatalf("materialized catalog delta = %+v", changed)
	}
	_, advertised, err := engine.toolDefinitionsFromSnapshot(
		current,
		TurnRequest{Prompt: "continue"},
	)
	if err != nil || !advertised["special_deploy"] {
		t.Fatalf("advertised = %v err=%v", advertised, err)
	}
	binding, ok := current.Binding("special_deploy")
	if !ok {
		t.Fatal("materialized binding missing")
	}
	if _, err := registry.ResolveCatalogToolID("special_deploy", binding); err != nil {
		t.Fatalf("materialized binding rejected: %v", err)
	}
}

func TestToolSearchMaterializesForTheNextSample(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := toolsearch.Register(registry); err != nil {
		t.Fatal(err)
	}
	executor := &countingCatalogExecutor{
		descriptor: catalogFixtureTool("special_deploy").Descriptor(),
	}
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	runtime := &scriptedProvider{streams: []provider.Stream{
		toolCallStream("search", toolsearch.ToolName, `{"query":"special_deploy"}`),
		toolCallStream("deploy", "special_deploy", `{}`),
		textStream("done"),
		textStream("done"),
	}}
	engine := newEngine(t, runtime, registry)
	if _, err := engine.Run(t.Context(), "find a capability and use it", nil); err != nil {
		t.Fatal(err)
	}
	if executor.calls.Load() != 1 || len(runtime.requests) < 2 {
		t.Fatalf("calls=%d requests=%d", executor.calls.Load(), len(runtime.requests))
	}
	contains := func(request provider.ModelRequest, name string) bool {
		for _, definition := range request.Tools {
			if definition.Name == name {
				return true
			}
		}
		return false
	}
	if contains(runtime.requests[0], "special_deploy") ||
		!contains(runtime.requests[1], "special_deploy") {
		t.Fatal("materialized tool was not added only after tool_search")
	}
}
