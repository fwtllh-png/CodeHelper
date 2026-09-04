package protocol

import (
	"strings"
	"testing"
)

const testDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func validCase() Case {
	return Case{
		Version: Version, ID: "single-file-fix", Revision: 1,
		Digest: testDigest, Category: "single_file_fix", Prompt: "fix it",
		Execution:   Execution{Tools: true, MaxSteps: 6, TimeoutMS: 60_000},
		Expectation: Expectation{Terminal: TerminalCompleted},
	}
}

func TestCaseValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Case)
		wantErr string
	}{
		{name: "valid"},
		{name: "unknown version", mutate: func(c *Case) { c.Version++ }, wantErr: "unsupported"},
		{name: "missing revision", mutate: func(c *Case) { c.Revision = 0 }, wantErr: "revision"},
		{name: "missing digest", mutate: func(c *Case) { c.Digest = "" }, wantErr: "digest"},
		{name: "invalid terminal", mutate: func(c *Case) { c.Expectation.Terminal = "done" }, wantErr: "terminal status"},
		{name: "negative execution limit", mutate: func(c *Case) { c.Execution.MaxSteps = -1 }, wantErr: "limits"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validCase()
			if test.mutate != nil {
				test.mutate(&value)
			}
			assertValidation(t, value.Validate(), test.wantErr)
		})
	}
}

func validResult() Result {
	return Result{
		Version: Version, ID: "run-1", Revision: 1, Digest: testDigest,
		CaseID: "single-file-fix", CaseRevision: 1, CaseDigest: testDigest,
		Status: ResultPassed, Terminal: TerminalCompleted,
		Verification: &Verification{Status: "passed", Action: "passed"},
		Usage:        Usage{Calls: 1},
	}
}

func TestResultValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Result)
		wantErr string
	}{
		{name: "valid"},
		{name: "unavailable without terminal", mutate: func(r *Result) {
			r.Status, r.Terminal = ResultUnavailable, ""
		}},
		{name: "unknown version", mutate: func(r *Result) { r.Version++ }, wantErr: "unsupported"},
		{name: "missing revision", mutate: func(r *Result) { r.Revision = 0 }, wantErr: "revision"},
		{name: "missing digest", mutate: func(r *Result) { r.Digest = "" }, wantErr: "digest"},
		{name: "missing case revision", mutate: func(r *Result) { r.CaseRevision = 0 }, wantErr: "case identity"},
		{name: "missing case digest", mutate: func(r *Result) { r.CaseDigest = "" }, wantErr: "case identity"},
		{name: "invalid status", mutate: func(r *Result) { r.Status = "complete" }, wantErr: "result status"},
		{name: "invalid terminal", mutate: func(r *Result) { r.Terminal = "done" }, wantErr: "terminal status"},
		{name: "unavailable with terminal", mutate: func(r *Result) { r.Status = ResultUnavailable }, wantErr: "must not have"},
		{name: "invalid counters", mutate: func(r *Result) { r.Usage.UnpricedCalls = 2 }, wantErr: "counters"},
		{name: "invalid verification", mutate: func(r *Result) { r.Verification.Status = "unknown" }, wantErr: "verification"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validResult()
			if test.mutate != nil {
				test.mutate(&value)
			}
			assertValidation(t, value.Validate(), test.wantErr)
		})
	}
}

func assertValidation(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Validate() error = %v, want containing %q", err, want)
	}
}
