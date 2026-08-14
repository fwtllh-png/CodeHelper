package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
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
	evidence, ok := result.Metadata[verify.EvidenceMetadataKey].(verify.Evidence)
	if !ok || evidence.CallID != "verify-1" || evidence.MutationRevision != 1 {
		t.Fatalf("bound evidence = %#v", result.Metadata)
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
	engine.bindVerificationEvidence(provider.ToolCall{
		ID: "shell-1", Name: "shell_run",
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
	scope.state.diff.Record(TurnDiffEntry{Path: "a.go", Kind: "modified"})
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
	scope.state.diff.Record(TurnDiffEntry{Path: "a.go", Kind: "modified"})
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

func seedKernelMutation(t *testing.T, kernel *engineTurnKernel) {
	t.Helper()
	call := provider.ToolCall{ID: "write-1", Name: "file_write"}
	if err := kernel.startTools([]provider.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	if err := kernel.startTool(call.ID); err != nil {
		t.Fatal(err)
	}
	if err := kernel.closeTool(
		call,
		tool.Result{},
		[]toolguard.FileChange{{
			Path: "a.go",
			Kind: toolguard.FileModified,
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
	if err := kernel.beginVerification(); err != nil {
		t.Fatal(err)
	}
	command := gate.verificationCommand(
		turnkernel.VerificationFailed,
		nil,
		"broken",
	)
	action, err := kernel.finishVerification(command)
	if err != nil {
		t.Fatal(err)
	}
	if action != turnkernel.VerificationActionRepair {
		t.Fatalf("first strict failed verification action = %q", action)
	}
	if err := kernel.beginVerification(); err != nil {
		t.Fatal(err)
	}
	action, err = kernel.finishVerification(command)
	if err != nil {
		t.Fatal(err)
	}
	if action != turnkernel.VerificationActionBlocked {
		t.Fatalf("strict failed verification action = %q", action)
	}
}

func qualityEvidenceResult(status string, paths []string) tool.Result {
	return tool.Result{Metadata: map[string]any{
		verify.EvidenceMetadataKey: verify.Evidence{
			SchemaVersion: 1, Kind: "verify", Status: status,
			CoveredPaths: paths, CommandDigest: "sha256:test",
		},
	}}
}
