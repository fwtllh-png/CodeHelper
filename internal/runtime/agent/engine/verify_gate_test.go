package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerfixture "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/fixture"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	filetool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/file"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

// scriptedVerifier replays one receipt per pass, holding the last one once the
// script runs out.
type scriptedVerifier struct {
	receipts []verify.Receipt
	err      error
	requests []verify.Request
}

func (v *scriptedVerifier) Verify(
	_ context.Context, request verify.Request,
) (verify.Receipt, error) {
	v.requests = append(v.requests, request)
	if v.err != nil {
		return verify.Receipt{}, v.err
	}
	index := min(len(v.requests)-1, len(v.receipts)-1)
	return v.receipts[index], nil
}

func failedReceipt(message string) verify.Receipt {
	return verify.Receipt{
		Scope: verify.ScopeDiagnostics, Status: verify.StatusFailed, Errors: 1,
		Checks: []verify.Check{{
			Name: "fake", Command: "fake check", Status: verify.StatusFailed,
			ExitCode: 1, Stderr: message,
		}},
	}
}

func passedReceipt() verify.Receipt {
	return verify.Receipt{Scope: verify.ScopeDiagnostics, Status: verify.StatusPassed}
}

// verifyGateFixture wires a real workspace, journal and file tools so the gate
// runs against genuine turn-diff and rollback behaviour.
type verifyGateFixture struct {
	engine   *Engine
	provider *scriptedProvider
	path     string
	verifier *scriptedVerifier
	journal  *workspacejournal.Manager
}

func newVerifyGateFixture(
	t *testing.T, options VerifyOptions, verifier *scriptedVerifier, extraReplies int, maxSteps int,
) verifyGateFixture {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "value.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := contentstore.NewMemory(contentstore.Options{})
	registry := tool.NewRegistry(nil, tool.NewResultStoreWithStore(32<<10, store))
	files, err := filetool.NewWithBackend(root, engineSandboxBackend{root: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := files.Register(registry); err != nil {
		t.Fatal(err)
	}
	journal, err := workspacejournal.New(root, store)
	if err != nil {
		t.Fatal(err)
	}
	streams := []provider.Stream{
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				ID: "read", Name: "file_read", Arguments: `{"path":"value.txt"}`,
			}},
			{Type: provider.EventMessageStop},
		}},
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				ID: "edit", Name: "file_edit",
				Arguments: `{"path":"value.txt","old":"before","new":"after"}`,
			}},
			{Type: provider.EventMessageStop},
		}},
	}
	for range 1 + extraReplies {
		streams = append(streams, &providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "done"},
			{Type: provider.EventMessageStop},
		}})
	}
	runtime := &scriptedProvider{streams: streams}
	options.Runner = verifier
	engine, err := New(Options{
		Provider: runtime, Route: testRoute(t), Tools: registry, Workspace: root,
		MaxOutputTokens: 128, MaxSteps: maxSteps, Journal: journal,
		Diagnostics: fakeDiagnosticRunner{}, Verify: options,
	})
	if err != nil {
		t.Fatal(err)
	}
	return verifyGateFixture{
		engine: engine, provider: runtime, path: path, verifier: verifier,
		journal: journal,
	}
}

func (f verifyGateFixture) contents(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A failed gate hands the failure back and the repair round runs on budget that
// is granted on top of MaxSteps, so it never costs the model a normal step.
func TestVerifyGateRepairRoundUsesExtraStepBudget(t *testing.T) {
	verifier := &scriptedVerifier{receipts: []verify.Receipt{
		failedReceipt("value.txt:1:1: still wrong"), passedReceipt(),
	}}
	// MaxSteps is exactly the three steps the happy path needs (read, edit,
	// answer); the repair round is a fourth model call.
	fixture := newVerifyGateFixture(t, VerifyOptions{
		Mode: VerifyModeHard, Scope: verify.ScopeDiagnostics, MaxRepairSteps: 1,
	}, verifier, 1, 3)
	var verifications []*VerificationReceipt

	result, err := fixture.engine.RunForTurn(t.Context(), "turn-1", "edit", func(event Event) error {
		if event.State == Verifying && event.Verification != nil {
			verifications = append(verifications, event.Verification)
		}
		return nil
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.State != Completed {
		t.Fatalf("result = %+v", result)
	}
	if len(verifications) != 2 ||
		verifications[0].Action != string(verifyActionRepair) ||
		verifications[1].Action != string(verifyActionPassed) {
		t.Fatalf("verifications = %+v", verifications)
	}
	if verifications[1].RepairSteps != 1 {
		t.Fatalf("repair steps = %d, want 1", verifications[1].RepairSteps)
	}
	if len(result.Verification.Attempts) != 2 ||
		result.Verification.Workspace == nil ||
		result.Verification.Workspace.Status != "changed" {
		t.Fatalf("verification receipt = %+v", result.Verification)
	}
	if fixture.contents(t) != "after\n" {
		t.Fatalf("workspace = %q, want the passing edit kept", fixture.contents(t))
	}
	if len(fixture.verifier.requests) != 2 {
		t.Fatalf("verifier requests = %+v", fixture.verifier.requests)
	}
	request := fixture.verifier.requests[0]
	if len(request.Paths) != 1 || !strings.HasSuffix(request.Paths[0], "value.txt") {
		t.Fatalf("verified paths = %v, want the changed file", request.Paths)
	}
	if len(request.Diagnostics) == 0 {
		t.Fatal("gate did not pass the turn's post-edit diagnostics to the runner")
	}
	// The repair prompt must reach the model as user feedback, not as a faked
	// tool result.
	last := fixture.provider.requests[len(fixture.provider.requests)-1]
	var feedback provider.Message
	for _, message := range last.Messages {
		if message.Role == provider.RoleUser && strings.Contains(message.Text(), "[verify]") {
			feedback = message
			break
		}
	}
	if feedback.Role != provider.RoleUser || !strings.Contains(feedback.Text(), "[verify]") ||
		!strings.Contains(feedback.Text(), "still wrong") {
		t.Fatalf("repair feedback = %+v", feedback)
	}
}

func TestWorkspaceVerificationBlockRetainsDraftForContinue(t *testing.T) {
	verifier := &scriptedVerifier{receipts: []verify.Receipt{
		failedReceipt("value.txt:1:1: still wrong"),
		failedReceipt("value.txt:1:1: still wrong"),
	}}
	fixture := newVerifyGateFixture(t, VerifyOptions{
		Mode: VerifyModeSoft, Scope: verify.ScopeDiagnostics, MaxRepairSteps: 1,
	}, verifier, 1, 3)

	blocked, err := fixture.engine.RunForTurnWithIntentAndAttachments(
		t.Context(),
		"turn-blocked",
		"edit",
		protocol.TurnIntentWorkspaceChange,
		nil,
		func(Event) error { return nil },
	)

	if err == nil || protocol.CodeOf(err) != protocol.CodeConflict {
		t.Fatalf("blocked result = %+v error = %v", blocked, err)
	}
	if blocked.Verification == nil ||
		blocked.Verification.Action != string(verifyActionBlocked) ||
		blocked.Verification.Workspace == nil ||
		blocked.Verification.Workspace.Status != "draft" {
		t.Fatalf("blocked verification = %+v", blocked.Verification)
	}
	if !fixture.journal.HasDraft("turn-blocked") ||
		fixture.contents(t) != "after\n" {
		t.Fatalf(
			"draft retained=%v contents=%q",
			fixture.journal.HasDraft("turn-blocked"),
			fixture.contents(t),
		)
	}

	fixture.verifier.receipts = append(
		fixture.verifier.receipts,
		passedReceipt(),
	)
	fixture.provider.streams = append(fixture.provider.streams,
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "done"},
			{Type: provider.EventMessageStop},
		}},
	)
	recovery := protocol.TurnRecoveryContext{
		Action: protocol.TurnRecoveryContinue, SourceTurnID: "turn-blocked",
	}
	continued, err := fixture.engine.RunForTurnWithRequest(
		t.Context(),
		"turn-continued",
		TurnRequest{
			Prompt: "continue repair", Intent: protocol.TurnIntentWorkspaceChange,
			Recovery: &recovery,
		},
		func(Event) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if continued.State != Completed ||
		fixture.contents(t) != "after\n" ||
		fixture.journal.HasDraft("turn-blocked") ||
		fixture.journal.HasDraft("turn-continued") {
		t.Fatalf(
			"continued = %+v contents=%q source_draft=%v recovery_draft=%v",
			continued,
			fixture.contents(t),
			fixture.journal.HasDraft("turn-blocked"),
			fixture.journal.HasDraft("turn-continued"),
		)
	}
	lastRequest := fixture.verifier.requests[len(fixture.verifier.requests)-1]
	if len(lastRequest.Paths) != 1 ||
		!strings.HasSuffix(lastRequest.Paths[0], "value.txt") {
		t.Fatalf("continued verification paths = %v", lastRequest.Paths)
	}
	if _, err := fixture.engine.RevertWorkspace(
		t.Context(),
		"turn-continued",
	); err != nil {
		t.Fatal(err)
	}
	if fixture.contents(t) != "before\n" {
		t.Fatalf("reverted recovery = %q, want original baseline", fixture.contents(t))
	}
}

func TestWorkspaceChangeFailsClosedWhenVerificationIsUnavailable(t *testing.T) {
	verifier := &scriptedVerifier{receipts: []verify.Receipt{{
		Scope: verify.ScopeDiagnostics, Status: verify.StatusUnavailable,
		Message: "no post-edit diagnostics covered the changed files",
	}}}
	fixture := newVerifyGateFixture(t, VerifyOptions{
		Mode: VerifyModeSoft, Scope: verify.ScopeDiagnostics,
	}, verifier, 0, 3)

	result, err := fixture.engine.RunForTurnWithIntentAndAttachments(
		t.Context(),
		"turn-unavailable",
		"edit",
		protocol.TurnIntentWorkspaceChange,
		nil,
		func(Event) error { return nil },
	)

	if err == nil || protocol.CodeOf(err) != protocol.CodeConflict {
		t.Fatalf("result = %+v, error = %v, want conflict", result, err)
	}
	if !strings.Contains(err.Error(), "uncovered_paths=value.txt") {
		t.Fatalf("error = %v", err)
	}
	if result.State == Completed {
		t.Fatalf("unavailable verification completed the turn: %+v", result)
	}
}

func TestVerifyGateHardFailureFailsTurnAndRollsBack(t *testing.T) {
	verifier := &scriptedVerifier{receipts: []verify.Receipt{failedReceipt("broken")}}
	fixture := newVerifyGateFixture(t, VerifyOptions{
		Mode: VerifyModeHard, OnFailure: VerifyOnFailureFail, Scope: verify.ScopeDiagnostics,
	}, verifier, 0, 8)
	var states []State

	result, err := fixture.engine.RunForTurn(t.Context(), "turn-1", "edit", func(event Event) error {
		states = append(states, event.State)
		return nil
	})

	if err == nil || !strings.Contains(err.Error(), "verification") {
		t.Fatalf("RunForTurn() error = %v, want a verification failure", err)
	}
	if result.State != Failed {
		t.Fatalf("result state = %q", result.State)
	}
	if result.Verification == nil || result.Verification.Workspace == nil ||
		result.Verification.Workspace.Status != "restored" ||
		len(result.Verification.Workspace.Restored) != 1 {
		t.Fatalf("verification receipt = %+v", result.Verification)
	}
	assertOneTerminal(t, states, Failed)
	if fixture.contents(t) != "before\n" {
		t.Fatalf("workspace = %q, want the failed turn rolled back", fixture.contents(t))
	}
}

// revert keeps the turn successful (the model's explanation survives) while the
// workspace goes back to its pre-turn state.
func TestVerifyGateRevertCompletesButRestoresWorkspace(t *testing.T) {
	verifier := &scriptedVerifier{receipts: []verify.Receipt{failedReceipt("broken")}}
	fixture := newVerifyGateFixture(t, VerifyOptions{
		Mode: VerifyModeHard, OnFailure: VerifyOnFailureRevert, Scope: verify.ScopeDiagnostics,
	}, verifier, 0, 8)
	var states []State
	// A host that reads files when the turn completes must already see the
	// restored workspace, so the rollback cannot wait for RunForTurn to return.
	onCompletion := ""

	result, err := fixture.engine.RunForTurn(t.Context(), "turn-1", "edit", func(event Event) error {
		states = append(states, event.State)
		if event.State == Completed {
			onCompletion = fixture.contents(t)
		}
		return nil
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.State != Completed || result.Text != "done" {
		t.Fatalf("result = %+v", result)
	}
	if result.Verification == nil || result.Verification.Action != string(verifyActionReverted) {
		t.Fatalf("verification = %+v", result.Verification)
	}
	if result.Verification.Workspace == nil ||
		result.Verification.Workspace.Status != "restored" ||
		len(result.Verification.Workspace.Restored) != 1 {
		t.Fatalf("workspace receipt = %+v", result.Verification.Workspace)
	}
	assertOneTerminal(t, states, Completed)
	if onCompletion != "before\n" {
		t.Fatalf("workspace at completion = %q, want the reverted edit already undone", onCompletion)
	}
	if fixture.contents(t) != "before\n" {
		t.Fatalf("workspace = %q, want the reverted edit undone", fixture.contents(t))
	}
}

func TestVerifyGateSoftModeReportsWithoutChangingTheOutcome(t *testing.T) {
	verifier := &scriptedVerifier{receipts: []verify.Receipt{failedReceipt("broken")}}
	fixture := newVerifyGateFixture(t, VerifyOptions{
		Mode: VerifyModeSoft, OnFailure: VerifyOnFailureFail, Scope: verify.ScopeDiagnostics,
	}, verifier, 0, 8)

	result, err := fixture.engine.RunForTurn(t.Context(), "turn-1", "edit", nil)

	if err != nil {
		t.Fatal(err)
	}
	if result.State != Completed ||
		result.Verification.Action != string(verifyActionReported) {
		t.Fatalf("result = %+v verification = %+v", result, result.Verification)
	}
	if fixture.contents(t) != "after\n" {
		t.Fatalf("workspace = %q, want a soft gate to keep the edit", fixture.contents(t))
	}
}

// A runner that cannot run means a hard gate cannot be honoured, so it fails the
// turn; a soft gate records it as unavailable and lets the turn stand.
func TestVerifyGateRunnerErrorDependsOnMode(t *testing.T) {
	tests := map[string]struct {
		mode      string
		wantError bool
		wantState State
	}{
		"hard": {mode: VerifyModeHard, wantError: true, wantState: Failed},
		"soft": {mode: VerifyModeSoft, wantState: Completed},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			verifier := &scriptedVerifier{err: errors.New("sandbox unavailable")}
			fixture := newVerifyGateFixture(t, VerifyOptions{
				Mode: test.mode, Scope: verify.ScopeRepository,
			}, verifier, 0, 8)

			result, err := fixture.engine.RunForTurn(t.Context(), "turn-1", "edit", nil)

			if (err != nil) != test.wantError {
				t.Fatalf("RunForTurn() error = %v, wantError %v", err, test.wantError)
			}
			if result.State != test.wantState {
				t.Fatalf("result state = %q, want %q", result.State, test.wantState)
			}
			if !test.wantError &&
				result.Verification.Status != verify.StatusUnavailable {
				t.Fatalf("verification = %+v", result.Verification)
			}
		})
	}
}

// Turns that change no file must not pay for verification at all.
func TestVerifyGateSkipsTurnsWithoutFileChanges(t *testing.T) {
	verifier := &scriptedVerifier{receipts: []verify.Receipt{failedReceipt("never runs")}}
	runtime := &scriptedProvider{streams: []provider.Stream{
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "nothing to change"},
			{Type: provider.EventMessageStop},
		}},
	}}
	engine, err := New(Options{
		Provider: runtime, Route: testRoute(t), Tools: tool.NewRegistry(nil, nil),
		MaxOutputTokens: 128,
		Verify: VerifyOptions{
			Mode: VerifyModeHard, Scope: verify.ScopeDiagnostics, Runner: verifier,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := engine.RunForTurn(t.Context(), "turn-1", "explain", nil)

	if err != nil {
		t.Fatal(err)
	}
	if result.State != Completed || result.Verification != nil {
		t.Fatalf("result = %+v verification = %+v", result, result.Verification)
	}
	if len(verifier.requests) != 0 {
		t.Fatalf("verifier ran %d times for a read-only turn", len(verifier.requests))
	}
}

// patchLikeTool writes files that its arguments never name, the way file_patch
// carries paths inside the diff body.
type patchLikeTool struct{ root string }

func (patchLikeTool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "fake_patch", Description: "apply a patch", Visibility: tool.VisibleModel,
		Capability: tool.CapabilityWrite, AccessMode: tool.AccessTree,
		ResourceResolver:   tool.ResourceResolver{PatchField: "patch"},
		ParallelPolicy:     tool.ParallelConcurrent,
		SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"patch": map[string]any{"type": "string", "minLength": float64(1)},
			},
			"required": []string{"patch"}, "additionalProperties": false,
		},
	}
}

func (p patchLikeTool) Execute(context.Context, json.RawMessage) (tool.Result, error) {
	path := filepath.Join(p.root, "value.txt")
	if err := os.WriteFile(path, []byte("after\n"), 0o600); err != nil {
		return tool.Result{}, err
	}
	return tool.Result{Content: "patched"}, nil
}

// The gate must cover changes made by tools whose arguments carry no path: that
// was the hole through which a patch-only turn committed unverified.
func TestVerifyGateCoversToolsWhoseArgumentsCarryNoPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "value.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := contentstore.NewMemory(contentstore.Options{})
	registry := tool.NewRegistry(nil, tool.NewResultStoreWithStore(32<<10, store))
	if err := registry.Register(patchLikeTool{root: root}, nil); err != nil {
		t.Fatal(err)
	}
	journal, err := workspacejournal.New(root, store)
	if err != nil {
		t.Fatal(err)
	}
	verifier := &scriptedVerifier{receipts: []verify.Receipt{failedReceipt("broken")}}
	runtime := &scriptedProvider{streams: []provider.Stream{
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				ID: "patch", Name: "fake_patch",
				Arguments: `{"patch":"--- a/value.txt\n+++ b/value.txt\n"}`,
			}},
			{Type: provider.EventMessageStop},
		}},
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "done"},
			{Type: provider.EventMessageStop},
		}},
	}}
	engine, err := New(Options{
		Provider: runtime, Route: testRoute(t), Tools: registry, Workspace: root,
		Security:        policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass),
		MaxOutputTokens: 128, MaxSteps: 8, Journal: journal,
		Diagnostics: fakeDiagnosticRunner{},
		Verify: VerifyOptions{
			Mode: VerifyModeHard, OnFailure: VerifyOnFailureFail,
			Scope: verify.ScopeDiagnostics, Runner: verifier,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := engine.RunForTurn(t.Context(), "turn-1", "patch it", nil)

	if err == nil || !strings.Contains(err.Error(), "verification") {
		t.Fatalf("RunForTurn() error = %v, want the gate to fail the turn", err)
	}
	if result.State != Failed {
		t.Fatalf("result state = %q, want the gate to fail the turn", result.State)
	}
	if len(verifier.requests) != 1 {
		t.Fatalf("verifier ran %d times, want the gate to run once", len(verifier.requests))
	}
	paths := verifier.requests[0].Paths
	if len(paths) != 1 || !strings.HasSuffix(paths[0], "value.txt") {
		t.Fatalf("verified paths = %v, want the patched file", paths)
	}
	data, err := os.ReadFile(filepath.Join(root, "value.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "before\n" {
		t.Fatalf("workspace = %q, want the failed turn rolled back", data)
	}
}

// A tool that rewrites a file with the bytes already on disk changed nothing, so
// the gate has nothing to verify.
func TestVerifyGateSkipsWritesThatChangeNoBytes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "value.txt"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := contentstore.NewMemory(contentstore.Options{})
	registry := tool.NewRegistry(nil, tool.NewResultStoreWithStore(32<<10, store))
	if err := registry.Register(patchLikeTool{root: root}, nil); err != nil {
		t.Fatal(err)
	}
	verifier := &scriptedVerifier{receipts: []verify.Receipt{failedReceipt("never runs")}}
	runtime := &scriptedProvider{streams: []provider.Stream{
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				ID: "patch", Name: "fake_patch",
				Arguments: `{"patch":"--- a/value.txt\n+++ b/value.txt\n"}`,
			}},
			{Type: provider.EventMessageStop},
		}},
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "done"},
			{Type: provider.EventMessageStop},
		}},
	}}
	engine, err := New(Options{
		Provider: runtime, Route: testRoute(t), Tools: registry, Workspace: root,
		Security:        policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass),
		MaxOutputTokens: 128, MaxSteps: 8,
		Diagnostics: fakeDiagnosticRunner{},
		Verify: VerifyOptions{
			Mode: VerifyModeHard, OnFailure: VerifyOnFailureFail,
			Scope: verify.ScopeDiagnostics, Runner: verifier,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := engine.RunForTurn(t.Context(), "turn-1", "patch it", nil)

	if err != nil {
		t.Fatal(err)
	}
	if result.State != Completed || result.Verification != nil {
		t.Fatalf("result = %+v verification = %+v", result, result.Verification)
	}
	if len(verifier.requests) != 0 {
		t.Fatalf("verifier ran %d times for a turn that changed nothing", len(verifier.requests))
	}
	if len(engine.TurnDiff()) != 0 {
		t.Fatalf("turn diff = %+v, want no observed change", engine.TurnDiff())
	}
}

func TestVerifyGateDefaultsToSoft(t *testing.T) {
	verifier := &scriptedVerifier{receipts: []verify.Receipt{failedReceipt("reported")}}
	fixture := newVerifyGateFixture(t, VerifyOptions{}, verifier, 0, 8)

	result, err := fixture.engine.RunForTurn(t.Context(), "turn-1", "edit", nil)

	if err != nil {
		t.Fatal(err)
	}
	if result.State != Completed || len(verifier.requests) != 1 {
		t.Fatalf("result = %+v requests = %d", result, len(verifier.requests))
	}
	if result.Verification == nil ||
		result.Verification.Mode != VerifyModeSoft ||
		result.Verification.Status != verify.StatusFailed ||
		result.Verification.Action != string(verifyActionReported) {
		t.Fatalf("verification = %+v", result.Verification)
	}
	if fixture.contents(t) != "after\n" {
		t.Fatalf("workspace = %q", fixture.contents(t))
	}
}

func TestVerifyGateCanStillBeExplicitlyDisabled(t *testing.T) {
	verifier := &scriptedVerifier{receipts: []verify.Receipt{failedReceipt("never runs")}}
	fixture := newVerifyGateFixture(
		t, VerifyOptions{Mode: VerifyModeOff}, verifier, 0, 8,
	)

	result, err := fixture.engine.RunForTurn(t.Context(), "turn-1", "edit", nil)

	if err != nil {
		t.Fatal(err)
	}
	if result.State != Completed || result.Verification != nil || len(verifier.requests) != 0 {
		t.Fatalf("result = %+v requests = %d", result, len(verifier.requests))
	}
}
