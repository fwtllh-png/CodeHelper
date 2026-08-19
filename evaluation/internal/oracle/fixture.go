package oracle

import (
	"fmt"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

func NewBaseline(scenarioID, fixtureID string) Input {
	digestA := spec.DigestString("fixture:" + fixtureID + ":a")
	digestB := spec.DigestString("fixture:" + fixtureID + ":b")
	digestC := spec.DigestString("fixture:" + fixtureID + ":c")
	partition := spec.DigestString("fixture-partition:" + fixtureID)
	proofs := make([]EvidenceProof, 0, len(AllIDs))
	for _, id := range AllIDs {
		proofs = append(proofs, EvidenceProof{
			Kind: requiredEvidence[id], Producer: "fixture",
			Digest:       spec.DigestString("evidence:" + fixtureID + ":" + id),
			RunPartition: partition,
		})
	}
	exitCode := 0
	return Input{
		SchemaVersion: SchemaVersion,
		ScenarioID:    scenarioID,
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

func Specialize(input Input, profile string, expectedFacts []string) (Input, error) {
	result, err := Clone(input)
	if err != nil {
		return Input{}, err
	}
	switch profile {
	case "terminal":
	case "parked":
		result.Runtime.Events = result.Runtime.Events[:3]
		result.Runtime.Phase = "parked"
		result.Runtime.ParkOwner = "input-001"
		result.Runtime.ParkDeadline = "deadline-001"
		result.Runtime.Projection.RuntimeTerminal = ""
		result.Runtime.Projection.HostTerminal = ""
	case "effect-zero":
		result.Effects = nil
		result.ExpectedEffectCount = 0
	case "workspace-readonly":
		result.Workspace.AfterDigest = result.Workspace.BeforeDigest
		result.Workspace.ChangedPaths = nil
		result.Workspace.AllowedPaths = []string{"README.md"}
	case "verification":
	case "persistence":
	case "host":
	case "security":
	case "resource":
	case "task-quality":
	default:
		return Input{}, fmt.Errorf("unsupported fixture profile %q", profile)
	}
	result.TaskQuality.Assertions = max(1, len(expectedFacts))
	result.TaskQuality.Passed = result.TaskQuality.Assertions
	result.Runtime.BudgetLimit += int64(len(expectedFacts))
	result.Resources.InputBytes += int64(len(expectedFacts))
	return result, nil
}
