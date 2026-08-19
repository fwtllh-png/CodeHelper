package oracle

import (
	"slices"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

func TestBaselinePassesEveryOracle(t *testing.T) {
	report := Evaluate(baselineInput(), AllIDs, spec.RiskP0)
	if report.Status != spec.StatusPassed || report.Primary != nil ||
		len(report.Results) != len(AllIDs) {
		t.Fatalf("baseline report = %+v", report)
	}
	for _, result := range report.Results {
		if result.Status != spec.StatusPassed {
			t.Fatalf("oracle %s status = %s", result.OracleID, result.Status)
		}
	}
}

func TestFaultsAreDetectedByExpectedOracle(t *testing.T) {
	tests := []struct {
		fault     FaultKind
		status    spec.Status
		signature string
		domain    Domain
	}{
		{FaultDuplicateEffect, spec.StatusFailed, "side_effect:effect_duplicate_execution", DomainToolOrGuard},
		{FaultDoubleTerminal, spec.StatusFailed, "runtime:terminal_multiple", DomainRuntimeState},
		{FaultStuckRunning, spec.StatusFailed, "runtime:terminal_missing", DomainRuntimeState},
		{FaultReceiptDrift, spec.StatusFailed, "persistence:receipt_terminal_drift", DomainPersistenceOrRecovery},
		{FaultReplayDrift, spec.StatusFailed, "persistence:replay_drift", DomainPersistenceOrRecovery},
		{FaultGuardBypass, spec.StatusFailed, "side_effect:effect_guard_bypass", DomainToolOrGuard},
		{FaultSecurityBypass, spec.StatusFailed, "security:permission_bypass", DomainToolOrGuard},
		{FaultWorkspaceEscape, spec.StatusFailed, "workspace:workspace_scope_violation", DomainToolOrGuard},
		{FaultVerificationFail, spec.StatusFailed, "verification:verification_failed", DomainPlatformEnvironment},
		{FaultResourceLeak, spec.StatusFailed, "resource:process_leak", DomainRuntimeState},
		{FaultHostDrift, spec.StatusFailed, "host:host_terminal_drift", DomainHostProjection},
		{FaultTaskQuality, spec.StatusFailed, "task_quality:task_assertion_failed", DomainTaskQuality},
		{FaultMalformedEvidence, spec.StatusInvalid, "harness:oracle_input_invalid", DomainEvaluationHarness},
	}
	for _, test := range tests {
		t.Run(string(test.fault), func(t *testing.T) {
			input, err := Inject(baselineInput(), test.fault)
			if err != nil {
				t.Fatal(err)
			}
			report := Evaluate(input, AllIDs, spec.RiskP0)
			if report.Status != test.status ||
				report.FailureSignature != test.signature ||
				report.Primary == nil ||
				report.Primary.Domain != test.domain {
				t.Fatalf("fault report = %+v", report)
			}
		})
	}
}

func TestUnavailableAndMissingEvidenceNeverPass(t *testing.T) {
	input := baselineInput()
	input.Workspace.EvidenceAvailable = false
	report := Evaluate(input, []string{WorkspaceID}, spec.RiskP1)
	if report.Status != spec.StatusInvalid {
		t.Fatalf("missing workspace status = %s, want invalid", report.Status)
	}
	input = baselineInput()
	input.Verification.Commands[0].Status = spec.StatusUnavailable
	input.Verification.Commands[0].ExitCode = nil
	report = Evaluate(input, []string{VerificationID}, spec.RiskP1)
	if report.Status != spec.StatusUnavailable {
		t.Fatalf("unavailable verification status = %s", report.Status)
	}
}

func TestVerificationRequiresMandatoryCommand(t *testing.T) {
	input := baselineInput()
	input.Verification.Commands[0].Mandatory = false
	report := Evaluate(input, []string{VerificationID}, spec.RiskP0)
	if report.Status != spec.StatusInvalid ||
		report.FailureSignature != "verification:required_evidence_invalid" {
		t.Fatalf("optional-only verification report = %+v", report)
	}
}

func TestSideEffectOracleDistinguishesProvedZeroFromMissingEvidence(t *testing.T) {
	input := baselineInput()
	input.Effects = nil
	input.ExpectedEffectCount = 0
	report := Evaluate(input, []string{SideEffectID}, spec.RiskP1)
	if report.Status != spec.StatusPassed {
		t.Fatalf("proved-zero side effect report = %+v", report)
	}
	input = baselineInput()
	input.Evidence = slices.DeleteFunc(input.Evidence, func(proof EvidenceProof) bool {
		return proof.Kind == requiredEvidence[SideEffectID]
	})
	report = Evaluate(input, []string{SideEffectID}, spec.RiskP1)
	if report.Status != spec.StatusInvalid {
		t.Fatalf("missing side-effect evidence report = %+v", report)
	}
}

func TestInvalidOracleSetAndInputAreHarnessFailures(t *testing.T) {
	report := Evaluate(baselineInput(), []string{"unknown"}, spec.RiskP0)
	if report.Status != spec.StatusInvalid || report.Primary == nil ||
		report.Primary.Domain != DomainEvaluationHarness ||
		!strings.Contains(report.FailureSignature, "oracle_set_invalid") {
		t.Fatalf("invalid oracle report = %+v", report)
	}
	input := baselineInput()
	input.Runtime.Events[1].Sequence = input.Runtime.Events[0].Sequence
	report = Evaluate(input, AllIDs, spec.RiskP0)
	if report.Status != spec.StatusInvalid || report.Primary == nil ||
		report.Primary.Domain != DomainEvaluationHarness {
		t.Fatalf("invalid input report = %+v", report)
	}
}

func TestProjectionAndPersistenceFailuresHaveDistinctAttribution(t *testing.T) {
	input := baselineInput()
	input.Runtime.Projection.HostTerminal = "turn.failed"
	report := Evaluate(input, AllIDs, spec.RiskP0)
	if report.Primary == nil || report.Primary.Domain != DomainHostProjection ||
		report.FailureSignature != "runtime:projection_terminal_drift" {
		t.Fatalf("projection report = %+v", report)
	}
}

func baselineInput() Input {
	exitCode := 0
	digestA := "sha256:" + strings.Repeat("a", 64)
	digestB := "sha256:" + strings.Repeat("b", 64)
	digestC := "sha256:" + strings.Repeat("c", 64)
	partition := "sha256:" + strings.Repeat("d", 64)
	proofs := make([]EvidenceProof, 0, len(AllIDs))
	for _, id := range AllIDs {
		proofs = append(proofs, EvidenceProof{
			Kind: requiredEvidence[id], Producer: "fixture",
			Digest: digestA, RunPartition: partition,
		})
	}
	return Input{
		SchemaVersion: SchemaVersion,
		ScenarioID:    "oracle-baseline",
		RunPartition:  partition,
		Evidence:      proofs,
		Runtime: RuntimeFacts{
			EvidenceAvailable: true,
			Phase:             "terminal",
			Events: []RuntimeEvent{
				{
					Sequence: 1, Kind: "turn.started",
					Turn: "turn-001", Operation: "operation-001",
				},
				{
					Sequence: 2, Kind: "tool.start",
					Turn: "turn-001", Operation: "operation-001", Effect: "effect-001",
				},
				{
					Sequence: 3, Kind: "tool.result",
					Turn: "turn-001", Operation: "operation-001", Effect: "effect-001",
				},
				{
					Sequence: 4, Kind: "turn.completed",
					Turn: "turn-001", Operation: "operation-001", ReceiptHash: digestC,
				},
			},
			MailboxCount: 1, MailboxCapacity: 128,
			BudgetUsed: 40, BudgetLimit: 100,
			Projection: ProjectionFact{
				EvidenceAvailable: true,
				RuntimeTerminal:   "turn.completed", HostTerminal: "turn.completed",
			},
		},
		EffectsAvailable:    true,
		ExpectedEffectCount: 1,
		Effects: []EffectFact{{
			ID: "effect-001", Consequential: true,
			Claims: 1, Executions: 1, Results: 1,
			Guarded: true, ApprovalRequired: true, ApprovalBound: true,
		}},
		Workspace: WorkspaceFacts{
			EvidenceAvailable: true,
			BeforeDigest:      digestA, AfterDigest: digestB,
			ChangedPaths:     []string{"internal/example/file.go"},
			AllowedPaths:     []string{"internal/example/**"},
			PreexistingDirty: []string{"README.md"},
			PreservedDirty:   []string{"README.md"},
		},
		Verification: VerificationFacts{
			EvidenceAvailable: true,
			Commands: []VerificationRun{{
				ID: "focused-test", Status: spec.StatusPassed,
				ExitCode: &exitCode, Mandatory: true,
			}},
		},
		Persistence: PersistenceFacts{
			EvidenceAvailable: true,
			EventDigest:       digestA, ReplayDigest: digestA,
			SnapshotDigest: digestB, RebuiltDigest: digestB,
			TerminalDigest: digestC, ReceiptTerminalDigest: digestC,
			OutboxPublications: 1,
		},
		Host: HostFacts{
			EvidenceAvailable: true,
			RuntimeTerminal:   "turn.completed", VisibleTerminal: "turn.completed",
			CursorCommitted: true, Continued: true,
		},
		Security: SecurityFacts{
			EvidenceAvailable: true,
			SandboxRequired:   true, SandboxAvailable: true,
		},
		Resources: ResourceFacts{
			EvidenceAvailable: true,
			ProcessesBefore:   1, ProcessesAfter: 1,
			GoroutinesBefore: 10, GoroutinesAfter: 11, GoroutineTolerance: 2,
			FDsBefore: 8, FDsAfter: 8, FDTolerance: 1,
			SubscribersBefore: 1, SubscribersAfter: 1,
			QueuePeak: 4, QueueCapacity: 128,
			InputBytes: 1000, PersistedBytes: 2000,
			MaxAmplificationMilli: 3000,
			ProcessIDsBefore:      []string{"process-001"},
			ProcessIDsAfter:       []string{"process-001"},
		},
		TaskQuality: TaskQualityFacts{
			EvidenceAvailable: true, Deterministic: true,
			Assertions: 1, Passed: 1,
		},
	}
}
