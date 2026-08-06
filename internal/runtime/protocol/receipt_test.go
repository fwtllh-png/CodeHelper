package protocol

import "testing"

// TestExecutionReceiptNormalizesVerification pins the rule that an unset or
// unknown verification value degrades to not_evaluated, so a receipt can never
// be read as "verified" by accident.
func TestExecutionReceiptNormalizesVerification(t *testing.T) {
	receipt := &ExecutionReceiptData{
		Goal:         "fix add",
		Verification: ReceiptVerification{Diagnostics: "", Tests: "bogus", Verify: ReceiptPassed},
	}
	if err := receipt.validate(); err != nil {
		t.Fatal(err)
	}
	if receipt.Verification.Diagnostics != ReceiptNotEvaluated {
		t.Fatalf("empty diagnostics = %q", receipt.Verification.Diagnostics)
	}
	if receipt.Verification.Tests != ReceiptNotEvaluated {
		t.Fatalf("unknown tests value = %q", receipt.Verification.Tests)
	}
	if receipt.Verification.Verify != ReceiptPassed {
		t.Fatalf("valid verify value was rewritten to %q", receipt.Verification.Verify)
	}
}

func TestTurnVerificationNormalizesUnknownStatus(t *testing.T) {
	data := &TurnVerificationData{Scope: "diagnostics", Action: "passed", Status: "green"}
	if err := data.validate(); err != nil {
		t.Fatal(err)
	}
	if data.Status != ReceiptNotEvaluated {
		t.Fatalf("unknown status = %q, want not_evaluated", data.Status)
	}
}

func TestTurnVerificationRequiresScopeActionAndCheckNames(t *testing.T) {
	tests := map[string]*TurnVerificationData{
		"missing scope":  {Action: "passed", Status: ReceiptPassed},
		"missing action": {Scope: "diagnostics", Status: ReceiptPassed},
		"nameless check": {
			Scope: "diagnostics", Action: "failed", Status: ReceiptFailed,
			Checks: []VerificationCheck{{Command: "go test ./...", Status: ReceiptFailed}},
		},
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if err := data.validate(); err == nil {
				t.Fatalf("validate() accepted %+v", data)
			}
		})
	}
}

// A gate that could not run must survive as unavailable rather than be rewritten
// into not_evaluated, which would hide that verification was attempted.
func TestExecutionReceiptKeepsUnavailableVerification(t *testing.T) {
	receipt := &ExecutionReceiptData{
		Goal:         "fix add",
		Verification: ReceiptVerification{Verify: ReceiptUnavailable},
	}
	if err := receipt.validate(); err != nil {
		t.Fatal(err)
	}
	if receipt.Verification.Verify != ReceiptUnavailable {
		t.Fatalf("verify = %q", receipt.Verification.Verify)
	}
}

func TestExecutionReceiptValidatesDetailedVerificationAndWorkspace(t *testing.T) {
	receipt := &ExecutionReceiptData{
		VerificationDetail: &ReceiptVerificationDetail{
			Mode: "hard", FinalStatus: ReceiptPassed, Action: "passed",
			RepairSteps: 1,
			Attempts: []ReceiptVerificationAttempt{
				{Step: 0, Scope: "affected", Status: ReceiptFailed},
				{Step: 1, Scope: "affected", Status: ReceiptPassed},
			},
		},
		WorkspaceOutcome: &ReceiptWorkspaceOutcome{
			Status: "changed", Changed: []string{"calc.go"},
		},
		ContextSelections: []ReceiptContextSelection{{
			Path: "calc_test.go", Kind: "test", Reasons: []string{"search"},
			Included: false, Truncated: true, TruncationReason: "byte_budget",
		}},
	}
	if err := receipt.validate(); err != nil {
		t.Fatal(err)
	}
	receipt.VerificationDetail.Attempts[0].Scope = ""
	if err := receipt.validate(); err == nil {
		t.Fatal("receipt accepted a detailed verification attempt without scope")
	}
	receipt.VerificationDetail.Attempts[0].Scope = "affected"
	receipt.ContextSelections[0].TruncationReason = ""
	if err := receipt.validate(); err == nil {
		t.Fatal("receipt accepted a truncated context selection without reason")
	}
}

// Evidence is collected now, so claiming otherwise would make the receipt lie
// about its own coverage.
func TestEvidenceIsNoLongerListedAsUncollected(t *testing.T) {
	for _, section := range UncollectedReceiptSections {
		if section == "evidence" {
			t.Fatalf("not_collected still claims evidence: %v", UncollectedReceiptSections)
		}
	}
	if len(UncollectedReceiptSections) == 0 {
		t.Fatal("the list is empty, which would hide the sections still missing")
	}
}

func TestExecutionReceiptRejectsIncompleteEvidence(t *testing.T) {
	tests := map[string]*ReceiptEvidence{
		"kindless fact": {Facts: []ReceiptEvidenceFact{{Path: "a.go"}}},
		"pathless fact": {Facts: []ReceiptEvidenceFact{{Kind: "definition"}}},
		"kindless risk": {Risks: []ReceiptEvidenceRisk{{Path: "a.go"}}},
		"pathless risk": {Risks: []ReceiptEvidenceRisk{{Kind: "changed_without_verification"}}},
	}
	for name, evidence := range tests {
		t.Run(name, func(t *testing.T) {
			receipt := &ExecutionReceiptData{Goal: "fix add", Evidence: evidence}
			if err := receipt.validate(); err == nil {
				t.Fatalf("validate() accepted %+v", evidence)
			}
		})
	}

	complete := &ExecutionReceiptData{Goal: "fix add", Evidence: &ReceiptEvidence{
		Facts: []ReceiptEvidenceFact{{Kind: "definition", Path: "a.go", Line: 3, Turn: 1}},
		Risks: []ReceiptEvidenceRisk{{Kind: "changed_without_verification", Path: "a.go", Turn: 1}},
	}}
	if err := complete.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionReceiptRejectsPathlessChange(t *testing.T) {
	receipt := &ExecutionReceiptData{
		Goal: "fix add", Changes: []ReceiptChange{{Tool: "file_edit"}},
	}
	if err := receipt.validate(); err == nil {
		t.Fatal("change without a path was accepted")
	}
}
