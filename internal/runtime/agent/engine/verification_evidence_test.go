package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
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
	engine.advanceMutationRevision()
	result := qualityEvidenceResult(
		verify.StatusPassed, []string{"a.go", "b.go"},
	)
	engine.bindVerificationEvidence(provider.ToolCall{
		ID: "verify-1", Name: "quality_verify",
	}, &result, false)

	receipt, uncovered := engine.qualityVerificationReceipt([]string{"a.go", "b.go"})
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
	engine.advanceMutationRevision()
	result := qualityEvidenceResult(verify.StatusPassed, []string{"a.go"})
	engine.bindVerificationEvidence(provider.ToolCall{
		ID: "verify-1", Name: "quality_test",
	}, &result, false)

	receipt, uncovered := engine.qualityVerificationReceipt([]string{"a.go", "b.go"})
	if receipt.Status != verify.StatusUnavailable ||
		len(uncovered) != 1 || uncovered[0] != "b.go" {
		t.Fatalf("receipt = %+v, uncovered = %v", receipt, uncovered)
	}
	engine.advanceMutationRevision()
	receipt, uncovered = engine.qualityVerificationReceipt([]string{"a.go"})
	if receipt.Status != verify.StatusUnavailable ||
		len(uncovered) != 1 || uncovered[0] != "a.go" {
		t.Fatalf("stale receipt = %+v, uncovered = %v", receipt, uncovered)
	}
}

func TestQualityEvidenceRejectsSameBatchMutationAndGenericShell(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.advanceMutationRevision()
	result := qualityEvidenceResult(verify.StatusPassed, []string{"a.go"})
	engine.bindVerificationEvidence(provider.ToolCall{
		ID: "verify-1", Name: "quality_verify",
	}, &result, true)
	if accepted, _ := result.Metadata["verification_evidence_accepted"].(bool); accepted {
		t.Fatalf("same-batch evidence was accepted: %#v", result.Metadata)
	}
	shellResult := qualityEvidenceResult(verify.StatusPassed, []string{"a.go"})
	engine.bindVerificationEvidence(provider.ToolCall{
		ID: "shell-1", Name: "shell_run",
	}, &shellResult, false)
	receipt, uncovered := engine.qualityVerificationReceipt([]string{"a.go"})
	if receipt.Status != verify.StatusUnavailable || len(uncovered) != 1 {
		t.Fatalf("generic shell produced evidence: %+v, %v", receipt, uncovered)
	}
}

func TestVerifyGateAcceptsFullCurrentQualityCoverage(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.Verify = VerifyOptions{
		Mode: VerifyModeSoft, Scope: verify.ScopeDiagnostics,
		Runner: unavailableVerifier{}, MaxRepairSteps: 1,
	}
	engine.turnDiff.Record(TurnDiffEntry{Path: "a.go", Kind: "modified"})
	engine.advanceMutationRevision()
	result := qualityEvidenceResult(verify.StatusPassed, []string{"a.go"})
	engine.bindVerificationEvidence(provider.ToolCall{
		ID: "verify-1", Name: "quality_verify",
	}, &result, false)
	gate := verifyGate{engine: engine, requirePassed: true}

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
	engine.options.Verify = VerifyOptions{
		Mode: VerifyModeSoft, Scope: verify.ScopeDiagnostics,
		Runner: unavailableVerifier{}, MaxRepairSteps: 1,
	}
	engine.turnDiff.Record(TurnDiffEntry{Path: "a.go", Kind: "modified"})
	gate := verifyGate{engine: engine, requirePassed: true}

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

func TestStrictVerifyGateDoesNotReportFailedVerification(t *testing.T) {
	gate := verifyGate{requirePassed: true, repairs: 1}
	action := gate.decide(VerifyOptions{
		Mode: VerifyModeSoft, MaxRepairSteps: 1,
	}, failedReceipt("broken"))
	if action != verifyActionFailed {
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
