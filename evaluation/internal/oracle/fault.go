package oracle

import (
	"errors"
	"fmt"
)

type FaultKind string

const (
	FaultDuplicateEffect   FaultKind = "duplicate_effect"
	FaultDoubleTerminal    FaultKind = "double_terminal"
	FaultStuckRunning      FaultKind = "stuck_running"
	FaultReceiptDrift      FaultKind = "receipt_drift"
	FaultReplayDrift       FaultKind = "replay_drift"
	FaultGuardBypass       FaultKind = "guard_bypass"
	FaultSecurityBypass    FaultKind = "security_bypass"
	FaultWorkspaceEscape   FaultKind = "workspace_scope_violation"
	FaultVerificationFail  FaultKind = "verification_failure"
	FaultResourceLeak      FaultKind = "resource_leak"
	FaultHostDrift         FaultKind = "host_projection_drift"
	FaultTaskQuality       FaultKind = "task_quality_failure"
	FaultMalformedEvidence FaultKind = "malformed_evidence"
)

func Inject(input Input, kind FaultKind) (Input, error) {
	result, err := Clone(input)
	if err != nil {
		return Input{}, err
	}
	switch kind {
	case FaultDuplicateEffect:
		if len(result.Effects) == 0 {
			return Input{}, errors.New("duplicate effect fault requires an effect")
		}
		result.Effects[0].Executions = 2
		result.Effects[0].Results = 2
	case FaultDoubleTerminal:
		if len(result.Runtime.Events) == 0 {
			return Input{}, errors.New("double terminal fault requires Runtime events")
		}
		last := result.Runtime.Events[len(result.Runtime.Events)-1]
		last.Sequence++
		last.Kind = "turn.cancelled"
		result.Runtime.Events = append(result.Runtime.Events, last)
	case FaultStuckRunning:
		var retained []RuntimeEvent
		for _, event := range result.Runtime.Events {
			switch event.Kind {
			case "turn.completed", "turn.failed", "turn.cancelled":
				continue
			default:
				retained = append(retained, event)
			}
		}
		result.Runtime.Events = retained
		result.Runtime.Phase = "running"
		result.Runtime.Projection.RuntimeTerminal = ""
		result.Runtime.Projection.HostTerminal = ""
	case FaultReceiptDrift:
		result.Persistence.ReceiptTerminalDigest = alternateDigest(
			result.Persistence.TerminalDigest,
		)
	case FaultReplayDrift:
		result.Persistence.ReplayDigest = alternateDigest(
			result.Persistence.EventDigest,
		)
	case FaultGuardBypass:
		if len(result.Effects) == 0 {
			return Input{}, errors.New("guard bypass fault requires an effect")
		}
		result.Effects[0].Consequential = true
		result.Effects[0].Executions = 1
		result.Effects[0].Results = 1
		result.Effects[0].Guarded = false
	case FaultSecurityBypass:
		result.Security.PermissionBypass = 1
	case FaultWorkspaceEscape:
		result.Workspace.ChangedPaths = append(
			result.Workspace.ChangedPaths,
			"internal/security/unrelated.go",
		)
	case FaultVerificationFail:
		if len(result.Verification.Commands) == 0 {
			return Input{}, errors.New("verification fault requires a command")
		}
		exitCode := 1
		result.Verification.Commands[0].Status = "failed"
		result.Verification.Commands[0].ExitCode = &exitCode
	case FaultResourceLeak:
		result.Resources.ProcessesAfter = result.Resources.ProcessesBefore + 1
	case FaultHostDrift:
		result.Host.VisibleTerminal = "turn.failed"
	case FaultTaskQuality:
		result.TaskQuality.Passed = 0
	case FaultMalformedEvidence:
		result.Resources.QueueCapacity = -1
	default:
		return Input{}, fmt.Errorf("unsupported oracle fault %q", kind)
	}
	return result, nil
}

func alternateDigest(current string) string {
	if current == "" || current[len(current)-1] != '0' {
		return "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	}
	return "sha256:1111111111111111111111111111111111111111111111111111111111111111"
}
