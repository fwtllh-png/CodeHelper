package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProblemUsesStableCode(t *testing.T) {
	cause := errors.New("transport text that may change")
	err := NewProblem(CodeUnavailable, "provider unavailable", true, cause)

	if !IsCode(err, CodeUnavailable) {
		t.Fatalf("CodeOf() = %q, want %q", CodeOf(err), CodeUnavailable)
	}
	if !errors.Is(err, cause) {
		t.Fatal("Problem does not unwrap its cause")
	}
	data, marshalErr := json.Marshal(err)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	golden, readErr := os.ReadFile(filepath.Join("testdata", "problem.golden.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != strings.TrimSpace(string(golden)) {
		t.Fatalf("Problem JSON = %s", data)
	}
}

func TestUnknownProblemCodeDefaultsToInternalFailure(t *testing.T) {
	problem := NewProblem(ErrorCode("future_code"), "unknown remote failure", true, nil)
	if problem.Code != CodeInternal || problem.Retryable {
		t.Fatalf("NewProblem() = %+v, want non-retryable internal", problem)
	}
	manuallyConstructed := &Problem{Version: ProblemVersion, Code: ErrorCode("future_code")}
	if got := CodeOf(manuallyConstructed); got != CodeInternal {
		t.Fatalf("CodeOf(unknown) = %q, want internal", got)
	}
}

func TestProblemRateLimitMetadataJSON(t *testing.T) {
	problem := NewProblem(CodeUnavailable, "provider rate limited", true, nil)
	problem.HTTPStatus = 429
	problem.RateLimit = &RateLimitMetadata{
		Limit: "100", Remaining: "0", Reset: "60", RetryAfterMS: 1200,
	}
	data, err := json.Marshal(problem)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Problem
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.HTTPStatus != 429 || decoded.RateLimit == nil || decoded.RateLimit.RetryAfterMS != 1200 {
		t.Fatalf("round-trip failed: %+v", decoded)
	}
}

func TestProblemDetailsPreserveMachineReadableCheckpointReason(t *testing.T) {
	problem := NewProblemWithDetails(
		CodeConflict,
		"Checkpoint Profile Revision is stale",
		true,
		ProblemDetails{
			Reason:           ProblemReasonStaleProfileRevision,
			ResourceID:       "checkpoint-1",
			ExpectedRevision: 2,
			ActualRevision:   3,
		},
		nil,
	)
	data, err := json.Marshal(problem)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Problem
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Details == nil ||
		decoded.Details.Reason != ProblemReasonStaleProfileRevision ||
		decoded.Details.ExpectedRevision != 2 ||
		decoded.Details.ActualRevision != 3 {
		t.Fatalf("problem details = %+v", decoded.Details)
	}
}

func TestCodeOfContextErrors(t *testing.T) {
	if got := CodeOf(context.Canceled); got != CodeCanceled {
		t.Fatalf("CodeOf(context.Canceled) = %q", got)
	}
	if got := CodeOf(context.DeadlineExceeded); got != CodeDeadlineExceeded {
		t.Fatalf("CodeOf(context.DeadlineExceeded) = %q", got)
	}
}

func TestWrapProblemNilCauseReturnsNil(t *testing.T) {
	if err := WrapProblem(CodeInternal, "failed", false, nil); err != nil {
		t.Fatalf("WrapProblem() = %v, want nil", err)
	}
}

func TestUnclassifiedErrorDefaultsToRecoverableUnavailableFault(t *testing.T) {
	problem := ProblemOf(errors.New("external boundary failed"))
	if problem.Code != CodeUnavailable ||
		!problem.Retryable ||
		problem.Fault == nil ||
		problem.Fault.Disposition != FaultResumeTurn {
		t.Fatalf("problem = %+v", problem)
	}
}

func TestInternalProblemMustCarryFailTurnDisposition(t *testing.T) {
	problem := NewProblem(CodeInternal, "kernel invariant failed", false, nil)
	if problem.Fault == nil ||
		problem.Fault.Disposition != FaultFailTurn {
		t.Fatalf("problem = %+v", problem)
	}
}

func TestFaultMetadataRoundTripsRecoveryOwnershipAndDeadline(t *testing.T) {
	problem := NewFault(
		CodeDeadlineExceeded,
		"provider stream idle timeout",
		true,
		FaultMetadata{
			Origin:      FaultOriginProvider,
			Stage:       FaultStageStreamIdle,
			OperationID: "sample-1",
			RetryOwner:  FaultRetryOwnerEngine,
			ResumeHint:  FaultResumeRetryStep,
			SideEffects: SideEffectUnchanged,
			Deadline: &DeadlineMetadata{
				Scope: DeadlineProviderStreamIdle, TimeoutMS: 30000,
				Renewable: true,
			},
		},
		context.DeadlineExceeded,
	)
	data, err := json.Marshal(problem)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Problem
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Fault == nil ||
		decoded.Fault.Stage != FaultStageStreamIdle ||
		decoded.Fault.OperationID != "sample-1" ||
		decoded.Fault.RetryOwner != FaultRetryOwnerEngine ||
		decoded.Fault.ResumeHint != FaultResumeRetryStep ||
		decoded.Fault.Deadline == nil ||
		decoded.Fault.Deadline.Scope != DeadlineProviderStreamIdle ||
		!decoded.Fault.Deadline.Renewable {
		t.Fatalf("decoded fault = %+v", decoded.Fault)
	}
}

func TestRecoveryDecisionRequiresMatchingOwnerAndSafeReplay(t *testing.T) {
	problem := NewFault(
		CodeUnavailable,
		"provider unavailable",
		true,
		FaultMetadata{
			Origin: FaultOriginProvider, Stage: FaultStageModelSample,
			RetryOwner:  FaultRetryOwnerEngine,
			SideEffects: SideEffectUnchanged,
		},
		nil,
	)
	retry := DecideRecovery(problem, RecoveryContext{
		Owner: FaultRetryOwnerEngine, Idempotent: true,
		Attempt: 1, MaxAttempts: 3,
	})
	if retry.Action != RecoveryRetry {
		t.Fatalf("matching owner decision = %+v", retry)
	}
	for name, context := range map[string]RecoveryContext{
		"wrong owner": {
			Owner: FaultRetryOwnerWorker, Idempotent: true,
			Attempt: 1, MaxAttempts: 3,
		},
		"not idempotent": {
			Owner: FaultRetryOwnerEngine, Idempotent: false,
			Attempt: 1, MaxAttempts: 3,
		},
		"progress": {
			Owner: FaultRetryOwnerEngine, Idempotent: true, Progress: true,
			Attempt: 1, MaxAttempts: 3,
		},
		"exhausted": {
			Owner: FaultRetryOwnerEngine, Idempotent: true,
			Attempt: 3, MaxAttempts: 3,
		},
	} {
		t.Run(name, func(t *testing.T) {
			decision := DecideRecovery(problem, context)
			if decision.Action != RecoveryResume {
				t.Fatalf("decision = %+v", decision)
			}
		})
	}
}
