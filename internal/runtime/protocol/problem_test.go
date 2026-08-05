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
