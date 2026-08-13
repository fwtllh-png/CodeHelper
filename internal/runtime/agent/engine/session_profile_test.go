package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func TestEffectiveProfilePermissionPreservesHostReadOnlyCeiling(t *testing.T) {
	for _, requested := range []policy.Permission{
		policy.PermissionSuggest,
		policy.PermissionAuto,
		policy.PermissionBypass,
	} {
		if got := effectiveProfilePermission(true, requested); got != policy.PermissionNever {
			t.Fatalf("read-only permission for %s = %s", requested, got)
		}
	}
	if got := effectiveProfilePermission(
		false,
		policy.PermissionBypass,
	); got != policy.PermissionBypass {
		t.Fatalf("trusted permission = %s", got)
	}
}

func TestProfilePermissionCeilingDistinguishesHostFromSessionNever(t *testing.T) {
	sessionNever := Options{
		Security:                 policy.DefaultRuntime(policy.ModeAct, policy.PermissionNever),
		ProfilePermissionCeiling: policy.PermissionSuggest,
	}
	if profileReadOnlyFromOptions(sessionNever) {
		t.Fatal("session-selected never became a Host read-only ceiling")
	}
	hostNever := sessionNever
	hostNever.ProfilePermissionCeiling = policy.PermissionNever
	if !profileReadOnlyFromOptions(hostNever) {
		t.Fatal("Host read-only ceiling was not retained")
	}
}

func TestProfilePermissionCeilingClampsEveryPosture(t *testing.T) {
	for _, requested := range []policy.Permission{
		policy.PermissionNever,
		policy.PermissionSuggest,
		policy.PermissionAuto,
		policy.PermissionBypass,
	} {
		got := effectiveProfilePermissionWithCeiling(
			false, requested, policy.PermissionSuggest,
		)
		want := requested
		if requested == policy.PermissionAuto || requested == policy.PermissionBypass {
			want = policy.PermissionSuggest
		}
		if got != want {
			t.Fatalf("requested %q under suggest ceiling = %q, want %q", requested, got, want)
		}
	}
}

func TestSessionProfileModeRefreshesModelInstructionsAndReceipt(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.ModePromptBudget = promptcontext.Budget{
		MaxBytes: 1 << 10, MaxTokens: 256,
	}
	engine.options.PromptContext, engine.options.ContextReceipts =
		promptcontext.RefreshMode(
			engine.options.PromptContext,
			engine.options.ContextReceipts,
			"act",
			engine.options.ModePromptBudget,
		)
	route := engine.options.Routes.Act()
	profile := protocol.SessionProfile{
		Version: protocol.SessionProfileVersion, Revision: 2,
		Mode: "plan", Provider: route.ProviderID(), Model: route.Model().ID,
		ApprovalPosture: "suggest", ExecutionTarget: "local",
		MaxSteps: 8, PromptCacheRevision: 2,
	}
	if err := engine.ApplySessionProfile(profile); err != nil {
		t.Fatal(err)
	}
	var prompt string
	for _, message := range engine.promptMessages() {
		prompt += message.Text()
	}
	if !strings.Contains(prompt, "Mode: plan") ||
		strings.Contains(prompt, "Mode: act") {
		t.Fatalf("prompt after Plan profile = %q", prompt)
	}
	receipts := engine.contextReceipts()
	if len(receipts) != 1 ||
		receipts[0].Kind != promptcontext.PartitionMode ||
		receipts[0].SourcePath != "session://profile.mode" {
		t.Fatalf("mode receipts = %+v", receipts)
	}
}

func TestSessionProfileToolAllowlistRejectsUnadvertisedExecution(t *testing.T) {
	executor := &profileToolExecutor{}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, &scriptedProvider{}, registry)
	route := engine.options.Routes.Act()
	profile := protocol.SessionProfile{
		Version: protocol.SessionProfileVersion, Revision: 1,
		Mode: "act", Provider: route.ProviderID(), Model: route.Model().ID,
		EnabledToolIDs: []string{"builtin:result_get"}, ApprovalPosture: "suggest",
		ExecutionTarget: "local", MaxSteps: 8, PromptCacheRevision: 1,
	}
	if err := engine.ApplySessionProfile(profile); err != nil {
		t.Fatal(err)
	}
	if definitions := testToolDefinitions(t, engine); len(definitions) != 0 {
		t.Fatalf("definitions = %+v, want only hidden retrieval helper", definitions)
	}
	results, err := engine.runTools(
		context.Background(),
		"turn-profile-tools",
		[]provider.ToolCall{{
			ID: "call-disabled", Name: "profile_write", Arguments: `{}`,
		}},
		map[string]tool.Result{},
		func(State, Event) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].IsError ||
		results[0].Metadata["error_category"] != "tool_disabled" {
		t.Fatalf("results = %+v", results)
	}
	if executor.calls != 0 {
		t.Fatalf("disabled executor ran %d times", executor.calls)
	}
}

func TestSessionProfileToolAllowlistDoesNotBypassGuard(t *testing.T) {
	executor := &profileToolExecutor{}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	engine, err := New(Options{
		Provider: &scriptedProvider{}, Route: testRoute(t), Tools: registry,
		MaxOutputTokens: 128,
		Authorize:       func(provider.ToolCall) bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	route := engine.options.Routes.Act()
	profile := protocol.SessionProfile{
		Version: protocol.SessionProfileVersion, Revision: 1,
		Mode: "act", Provider: route.ProviderID(), Model: route.Model().ID,
		EnabledToolIDs: []string{"builtin:profile_write"}, ApprovalPosture: "suggest",
		ExecutionTarget: "local", MaxSteps: 8, PromptCacheRevision: 1,
	}
	if err := engine.ApplySessionProfile(profile); err != nil {
		t.Fatal(err)
	}
	results, err := engine.runTools(
		context.Background(),
		"turn-profile-guard",
		[]provider.ToolCall{{
			ID: "call-guarded", Name: "profile_write", Arguments: `{}`,
		}},
		map[string]tool.Result{},
		func(State, Event) error { return nil },
	)
	if err == nil && (len(results) != 1 || !results[0].IsError) {
		t.Fatalf("guard accepted selected write tool: %+v", results)
	}
	if executor.calls != 0 {
		t.Fatalf("guarded executor ran %d times", executor.calls)
	}
}

func TestSessionProfileToolAllowlistBindsSourceAcrossRevocation(t *testing.T) {
	original := &profileToolExecutor{}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(original, nil); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, &scriptedProvider{}, registry)
	route := engine.options.Routes.Act()
	profile := protocol.SessionProfile{
		Version: protocol.SessionProfileVersion, Revision: 1,
		Mode: "act", Provider: route.ProviderID(), Model: route.Model().ID,
		EnabledToolIDs:  []string{"builtin:profile_write"},
		ApprovalPosture: "suggest", ExecutionTarget: "local",
		MaxSteps: 8, PromptCacheRevision: 1,
	}
	if err := engine.ApplySessionProfile(profile); err != nil {
		t.Fatal(err)
	}
	snapshot, err := registry.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := snapshot.Lookup("profile_write")
	if !ok {
		t.Fatal("profile_write is missing")
	}
	if _, err := registry.Revoke(
		entry.Source,
		"profile_write",
		registry.Generation(),
	); err != nil {
		t.Fatal(err)
	}
	replacement := &profileToolExecutor{}
	if _, err := registry.Reconcile(
		"dynamic:replacement",
		registry.Generation(),
		[]tool.Registration{tool.NewRegistration(replacement)},
	); err != nil {
		t.Fatal(err)
	}
	if definitions := testToolDefinitions(t, engine); len(definitions) != 0 {
		t.Fatalf("same-name replacement inherited allowlist: %+v", definitions)
	}
	results, err := engine.runTools(
		context.Background(),
		"turn-profile-source",
		[]provider.ToolCall{{
			ID: "call-replacement", Name: "profile_write", Arguments: `{}`,
		}},
		map[string]tool.Result{},
		func(State, Event) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].IsError ||
		results[0].Metadata["error_category"] != "tool_disabled" {
		t.Fatalf("replacement result = %+v", results)
	}
	if replacement.calls != 0 {
		t.Fatalf("same-name replacement ran %d times", replacement.calls)
	}
}

type profileToolExecutor struct{ calls int }

func (e *profileToolExecutor) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "profile_write", Description: "Profile allowlist fixture",
		InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
		},
		Visibility: tool.VisibleModel, Capability: tool.CapabilityWrite,
		AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
		SandboxRequirement: tool.SandboxNone,
		Availability:       tool.AvailabilityAvailable,
	}
}

func (e *profileToolExecutor) Execute(
	context.Context,
	json.RawMessage,
) (tool.Result, error) {
	e.calls++
	return tool.Result{Content: "executed"}, nil
}
