package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type unavailableVerifier struct{}

func (unavailableVerifier) Verify(
	_ context.Context, request verify.Request,
) (verify.Receipt, error) {
	return verify.Receipt{
		Scope: request.Scope, Status: verify.StatusUnavailable,
		Message: "diagnostics unavailable",
	}, nil
}

func TestQualityEvidenceCoversCurrentMutationRevision(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	result := qualityEvidenceResult(
		verify.StatusPassed, []string{"a.go", "b.go"},
	)
	engine.bindVerificationEvidence(provider.ToolCall{
		ID: "verify-1", Name: "quality_verify",
	}, &result, false, 1)

	receipt, uncovered := engine.qualityVerificationReceipt([]string{"a.go", "b.go"}, 1)
	if receipt.Status != verify.StatusPassed || len(uncovered) != 0 {
		t.Fatalf("receipt = %+v, uncovered = %v", receipt, uncovered)
	}
	evidence := result.Outcome.Facts.Verification
	if evidence == nil || evidence.CallID != "verify-1" || evidence.MutationRevision != 1 {
		t.Fatalf("bound evidence = %#v", result.Outcome)
	}
}

func TestQualityEvidenceCoversAbsoluteRecoveryDraftPaths(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.Workspace = t.TempDir()
	result := qualityEvidenceResult(verify.StatusPassed, []string{"a.go"})
	engine.bindVerificationEvidence(provider.ToolCall{
		ID: "verify-1", Name: "quality_verify",
	}, &result, false, 1)

	receipt, uncovered := engine.qualityVerificationReceipt(
		[]string{filepath.Join(engine.options.Workspace, "a.go")},
		1,
	)
	if receipt.Status != verify.StatusPassed || len(uncovered) != 0 {
		t.Fatalf("receipt = %+v, uncovered = %v", receipt, uncovered)
	}
}

func TestQualityEvidenceRejectsPartialAndStaleCoverage(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	result := qualityEvidenceResult(verify.StatusPassed, []string{"a.go"})
	engine.bindVerificationEvidence(provider.ToolCall{
		ID: "verify-1", Name: "quality_test",
	}, &result, false, 1)

	receipt, uncovered := engine.qualityVerificationReceipt([]string{"a.go", "b.go"}, 1)
	if receipt.Status != verify.StatusUnavailable ||
		len(uncovered) != 1 || uncovered[0] != "b.go" {
		t.Fatalf("receipt = %+v, uncovered = %v", receipt, uncovered)
	}
	receipt, uncovered = engine.qualityVerificationReceipt([]string{"a.go"}, 2)
	if receipt.Status != verify.StatusUnavailable ||
		len(uncovered) != 1 || uncovered[0] != "a.go" {
		t.Fatalf("stale receipt = %+v, uncovered = %v", receipt, uncovered)
	}
}

func TestQualityEvidenceRetainsFailureWithoutGrantingCoverage(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	result := qualityEvidenceResult(verify.StatusFailed, []string{"a.go"})
	result.IsError = true
	engine.bindVerificationEvidence(provider.ToolCall{
		ID: "verify-failed", Name: "quality_test",
	}, &result, false, 1)

	if accepted, _ := result.Metadata["verification_evidence_accepted"].(bool); !accepted {
		t.Fatalf("failed structured evidence was discarded: %#v", result.Metadata)
	}
	candidate := engine.completionCandidate(
		provider.ToolCall{ID: "complete", Name: "turn_complete"},
		tool.Result{}, false, 1, 1,
	)
	if len(candidate.QualityCalls) != 0 {
		t.Fatalf("failed quality calls bound to completion: %+v", candidate.QualityCalls)
	}
	receipt, uncovered := engine.qualityVerificationReceipt([]string{"a.go"}, 1)
	if receipt.Status != verify.StatusFailed || receipt.Errors != 1 ||
		len(receipt.Checks) != 1 ||
		receipt.Checks[0].Status != verify.StatusFailed ||
		len(uncovered) != 1 || uncovered[0] != "a.go" {
		t.Fatalf("receipt = %+v, uncovered = %v", receipt, uncovered)
	}
}

func TestLaterPassingQualityRetrySupersedesSameFailedCommand(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	failed := qualityEvidenceResult(verify.StatusFailed, []string{"a.go"})
	failed.IsError = true
	engine.bindVerificationEvidence(provider.ToolCall{
		ID: "verify-failed", Name: "quality_test",
	}, &failed, false, 1)
	passed := qualityEvidenceResult(verify.StatusPassed, []string{"a.go"})
	engine.bindVerificationEvidence(provider.ToolCall{
		ID: "verify-passed", Name: "quality_test",
	}, &passed, false, 1)

	receipt, uncovered := engine.qualityVerificationReceipt([]string{"a.go"}, 1)
	if receipt.Status != verify.StatusPassed || len(uncovered) != 0 ||
		len(receipt.Checks) != 1 ||
		receipt.Checks[0].Status != verify.StatusPassed {
		t.Fatalf("receipt = %+v, uncovered = %v", receipt, uncovered)
	}
}

func TestCorrectedQualityCommandSupersedesFailedCoverage(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	failed := qualityEvidenceResult(verify.StatusFailed, []string{"a.go"})
	failed.IsError = true
	failed.Outcome.Facts.Verification.CommandDigest = "sha256:bad-command"
	engine.bindVerificationEvidence(provider.ToolCall{
		ID: "verify-failed", Name: "quality_test",
	}, &failed, false, 1)
	passed := qualityEvidenceResult(verify.StatusPassed, []string{"a.go"})
	passed.Outcome.Facts.Verification.CommandDigest = "sha256:fixed-command"
	engine.bindVerificationEvidence(provider.ToolCall{
		ID: "verify-passed", Name: "quality_test",
	}, &passed, false, 1)

	receipt, uncovered := engine.qualityVerificationReceipt([]string{"a.go"}, 1)
	if receipt.Status != verify.StatusPassed || len(uncovered) != 0 ||
		len(receipt.Checks) != 1 ||
		receipt.Checks[0].Command != "sha256:fixed-command" {
		t.Fatalf("receipt = %+v, uncovered = %v", receipt, uncovered)
	}
}

func TestGenericVerifyCannotSupersedeFailedProcessSmoke(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	failedSmoke := qualityEvidenceResult(verify.StatusFailed, []string{"a.go"})
	failedSmoke.IsError = true
	failedSmoke.Outcome.Facts.Verification.Kind = "process_smoke"
	failedSmoke.Outcome.Facts.Verification.CommandDigest = "sha256:smoke"
	engine.bindVerificationEvidence(provider.ToolCall{
		ID: "smoke-failed", Name: "quality_process_smoke",
	}, &failedSmoke, false, 1)

	passedVerify := qualityEvidenceResult(verify.StatusPassed, []string{"a.go"})
	passedVerify.Outcome.Facts.Verification.Kind = "verify"
	passedVerify.Outcome.Facts.Verification.CommandDigest = "sha256:verify"
	engine.bindVerificationEvidence(provider.ToolCall{
		ID: "verify-passed", Name: "quality_verify",
	}, &passedVerify, false, 1)

	receipt, uncovered := engine.qualityVerificationReceipt([]string{"a.go"}, 1)
	if receipt.Status != verify.StatusFailed || receipt.Errors != 1 ||
		len(uncovered) != 0 {
		t.Fatalf("generic verify cleared process smoke failure: %+v, %v", receipt, uncovered)
	}

	passedSmoke := qualityEvidenceResult(verify.StatusPassed, []string{"a.go"})
	passedSmoke.Outcome.Facts.Verification.Kind = "process_smoke"
	passedSmoke.Outcome.Facts.Verification.CommandDigest = "sha256:smoke-retry"
	engine.bindVerificationEvidence(provider.ToolCall{
		ID: "smoke-passed", Name: "quality_process_smoke",
	}, &passedSmoke, false, 1)

	receipt, uncovered = engine.qualityVerificationReceipt([]string{"a.go"}, 1)
	if receipt.Status != verify.StatusPassed || len(uncovered) != 0 {
		t.Fatalf("passing process smoke did not clear prior failure: %+v, %v", receipt, uncovered)
	}
}

func TestQualityEvidenceRejectsSameBatchMutationAndGenericShell(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	result := qualityEvidenceResult(verify.StatusPassed, []string{"a.go"})
	engine.bindVerificationEvidence(provider.ToolCall{
		ID: "verify-1", Name: "quality_verify",
	}, &result, true, 1)
	if accepted, _ := result.Metadata["verification_evidence_accepted"].(bool); accepted {
		t.Fatalf("same-batch evidence was accepted: %#v", result.Metadata)
	}
	shellResult := qualityEvidenceResult(verify.StatusPassed, []string{"a.go"})
	shellResult.Execution.VerificationEvidenceAuthorized = false
	engine.bindVerificationEvidence(provider.ToolCall{
		ID: "shell-1", Name: "exec_command",
	}, &shellResult, false, 1)
	receipt, uncovered := engine.qualityVerificationReceipt([]string{"a.go"}, 1)
	if receipt.Status != verify.StatusUnavailable || len(uncovered) != 1 {
		t.Fatalf("generic shell produced evidence: %+v, %v", receipt, uncovered)
	}
}

func TestVerifyGateAcceptsFullCurrentQualityCoverage(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	scope := attachTestScope(t, engine)
	engine.options.Verify = VerifyOptions{
		Mode: VerifyModeSoft, Scope: verify.ScopeDiagnostics,
		Runner: unavailableVerifier{}, MaxRepairSteps: 1,
	}
	scope.state.diff.Record(turnkernel.TurnDiffEntry{Path: "a.go", Kind: "modified"})
	result := qualityEvidenceResult(verify.StatusPassed, []string{"a.go"})
	engine.bindVerificationEvidence(provider.ToolCall{
		ID: "verify-1", Name: "quality_verify",
	}, &result, false, 1)
	kernel := newEngineTurnKernel(
		protocol.TurnIntentAnswer, "act", nil, 0, nil, nil,
	)
	seedKernelMutation(t, kernel)
	gate := verifyGate{
		engine: engine,
		kernel: kernel,
	}

	outcome, err := gate.evaluate(t.Context(), func(State, Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if outcome.action != verifyActionPassed || outcome.receipt == nil ||
		outcome.receipt.Status != verify.StatusPassed ||
		outcome.receipt.Scope != verify.ScopeQuality {
		t.Fatalf("outcome = %+v", outcome)
	}
}

func TestVerifyGateRequestsStructuredRepairForMissingCoverage(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	scope := attachTestScope(t, engine)
	engine.options.Verify = VerifyOptions{
		Mode: VerifyModeSoft, Scope: verify.ScopeDiagnostics,
		Runner: unavailableVerifier{}, MaxRepairSteps: 1,
	}
	scope.state.diff.Record(turnkernel.TurnDiffEntry{Path: "a.go", Kind: "modified"})
	kernel := newEngineTurnKernel(
		protocol.TurnIntentAnswer, "act", nil, 0, nil, nil,
	)
	seedKernelMutation(t, kernel)
	gate := verifyGate{
		engine: engine, kernel: kernel,
	}

	outcome, err := gate.evaluate(t.Context(), func(State, Event) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if outcome.action != verifyActionRepair || outcome.receipt == nil ||
		!strings.Contains(outcome.receipt.Message, "uncovered_paths=a.go") {
		t.Fatalf("outcome = %+v", outcome)
	}
	feedback := verifyFeedback(outcome.receipt, 1)
	if !strings.Contains(feedback.Text(), "required_action=quality_verify") {
		t.Fatalf("feedback = %q", feedback.Text())
	}
}

func TestFailedQualityFeedbackRequiresPassingStructuredRetry(t *testing.T) {
	receipt := &VerificationReceipt{
		Receipt: verify.Receipt{
			Scope: verify.ScopeQuality, Status: verify.StatusFailed,
			Message: "structured quality command failed",
			Checks: []verify.Check{{
				Name: "test", Command: "sha256:test",
				Status: verify.StatusFailed, ExitCode: 1,
			}},
		},
		UncoveredPaths: []string{"a.go"},
	}
	feedback := verifyFeedback(receipt, 1).Text()
	for _, expected := range []string{
		"required_action=repair_quality_verification",
		`uncovered_paths=["a.go"]`,
		"network_targets",
		"Do not call turn_complete",
	} {
		if !strings.Contains(feedback, expected) {
			t.Fatalf("feedback %q does not contain %q", feedback, expected)
		}
	}
}

func seedKernelMutation(t *testing.T, kernel *turnkernel.RuntimeKernel) {
	t.Helper()
	call := provider.ToolCall{ID: "write-1", Name: "file_write"}
	if err := kernel.StartTools([]provider.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	if err := kernel.StartTool(call.ID); err != nil {
		t.Fatal(err)
	}
	if err := kernel.CloseTool(
		call,
		tool.Result{},
		[]tool.WorkspaceChange{{
			Path: "a.go",
			Kind: tool.WorkspaceModified,
		}},
	); err != nil {
		t.Fatal(err)
	}
}

func TestStrictVerifyGateDoesNotReportFailedVerification(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	kernel := newEngineTurnKernel(
		protocol.TurnIntentAnswer, "act", nil, 0, nil, nil,
	)
	seedKernelMutation(t, kernel)
	gate := verifyGate{
		engine: engine, kernel: kernel,
	}
	if err := kernel.BeginVerification(); err != nil {
		t.Fatal(err)
	}
	command := gate.verificationCommand(
		turnkernel.VerificationFailed,
		nil,
		"broken",
	)
	action, err := kernel.FinishVerification(command)
	if err != nil {
		t.Fatal(err)
	}
	if action != turnkernel.VerificationActionRepair {
		t.Fatalf("first strict failed verification action = %q", action)
	}
	if err := kernel.BeginVerification(); err != nil {
		t.Fatal(err)
	}
	action, err = kernel.FinishVerification(command)
	if err != nil {
		t.Fatal(err)
	}
	if action != turnkernel.VerificationActionBlocked {
		t.Fatalf("strict failed verification action = %q", action)
	}
}

func qualityEvidenceResult(status string, paths []string) tool.Result {
	return tool.Result{
		Execution: &tool.ExecutionReceipt{
			VerificationEvidenceAuthorized: true,
		},
		Outcome: &tool.Outcome{Facts: &tool.OutcomeFacts{
			Verification: &verify.Evidence{
				SchemaVersion: 1, Kind: "verify", Status: status,
				CoveredPaths: paths, CommandDigest: "sha256:test",
			},
		}},
	}
}
