package oracle

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

type evaluator func(Input) Result

var evaluators = map[string]evaluator{
	RuntimeID:      evaluateRuntime,
	SideEffectID:   evaluateSideEffect,
	WorkspaceID:    evaluateWorkspace,
	VerificationID: evaluateVerification,
	PersistenceID:  evaluatePersistence,
	HostID:         evaluateHost,
	SecurityID:     evaluateSecurity,
	ResourceID:     evaluateResource,
	TaskQualityID:  evaluateTaskQuality,
}

var requiredEvidence = map[string]string{
	RuntimeID:      "runtime_events",
	SideEffectID:   "effect_ledger",
	WorkspaceID:    "workspace_digest",
	VerificationID: "verification_result",
	PersistenceID:  "persistence_state",
	HostID:         "host_projection",
	SecurityID:     "security_receipts",
	ResourceID:     "resource_snapshot",
	TaskQualityID:  "task_assertions",
}

func Evaluate(input Input, required []string, risk spec.Risk) Report {
	report := Report{
		SchemaVersion: SchemaVersion,
		ScenarioID:    input.ScenarioID,
		Status:        spec.StatusPassed,
	}
	if !risk.Valid() {
		return invalidReport(input.ScenarioID, "oracle_risk_invalid", "oracle risk is invalid")
	}
	if err := input.Validate(); err != nil {
		return invalidReport(input.ScenarioID, "oracle_input_invalid", err.Error())
	}
	if err := validateRequired(required); err != nil {
		return invalidReport(input.ScenarioID, "oracle_set_invalid", err.Error())
	}
	for _, id := range required {
		var result Result
		if !hasEvidence(input, requiredEvidence[id]) {
			result = invalidEvidence(
				id,
				fmt.Sprintf("required evidence %s is missing", requiredEvidence[id]),
			)
		} else {
			result = safeEvaluate(id, evaluators[id], input)
		}
		for index := range result.Findings {
			result.Findings[index].Severity = risk
		}
		report.Results = append(report.Results, result)
		report.Status = mergeStatus(report.Status, result.Status)
	}
	for _, result := range report.Results {
		if len(result.Findings) == 0 {
			continue
		}
		finding := result.Findings[0]
		report.Primary = &finding
		report.FailureSignature = finding.OracleID + ":" + finding.Code
		break
	}
	return report
}

func safeEvaluate(id string, evaluate evaluator, input Input) (result Result) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = Result{
				OracleID: id,
				Status:   spec.StatusInvalid,
				Summary:  "oracle panicked",
				Findings: []Finding{{
					OracleID: id,
					Code:     "oracle_panic",
					Domain:   DomainEvaluationHarness,
					Summary:  fmt.Sprintf("oracle panic: %v", recovered),
				}},
			}
		}
	}()
	return evaluate(input)
}

func evaluateRuntime(input Input) Result {
	const id = RuntimeID
	facts := input.Runtime
	if !facts.EvidenceAvailable {
		return invalidEvidence(id, "runtime evidence is unavailable")
	}
	var findings []Finding
	terminalKinds := map[string]bool{
		"turn.completed": true,
		"turn.failed":    true,
		"turn.cancelled": true,
	}
	started := 0
	terminals := 0
	var runtimeTerminal string
	var turn, operation string
	var previous uint64
	for _, event := range facts.Events {
		if event.Sequence != previous+1 {
			findings = append(findings, finding(
				id, "event_sequence_drift", DomainRuntimeState,
				"runtime event sequence is not contiguous",
			))
		}
		previous = event.Sequence
		if turn == "" {
			turn, operation = event.Turn, event.Operation
		} else if event.Turn != turn || event.Operation != operation {
			findings = append(findings, finding(
				id, "identity_drift", DomainRuntimeState,
				"runtime events changed Turn or Operation identity",
			))
		}
		if event.Kind == "turn.started" {
			started++
		}
		if terminalKinds[event.Kind] {
			terminals++
			runtimeTerminal = event.Kind
		}
	}
	if started != 1 {
		findings = append(findings, finding(
			id, "turn_start_cardinality", DomainRuntimeState,
			fmt.Sprintf("runtime observed %d turn.started events", started),
		))
	}
	switch {
	case terminals == 0 && facts.Phase == "parked":
		if strings.TrimSpace(facts.ParkOwner) == "" ||
			strings.TrimSpace(facts.ParkDeadline) == "" {
			findings = append(findings, finding(
				id, "park_owner_missing", DomainRuntimeState,
				"parked Turn has no owner or deadline",
			))
		}
	case terminals == 0:
		findings = append(findings, finding(
			id, "terminal_missing", DomainRuntimeState,
			"started Turn has no terminal event",
		))
	case terminals > 1:
		findings = append(findings, finding(
			id, "terminal_multiple", DomainRuntimeState,
			fmt.Sprintf("runtime observed %d terminal events", terminals),
		))
	}
	if terminals == 1 && facts.Phase != "terminal" {
		findings = append(findings, finding(
			id, "terminal_phase_drift", DomainRuntimeState,
			"terminal Event and Runtime phase disagree",
		))
	}
	if terminals == 0 && facts.Phase == "terminal" {
		findings = append(findings, finding(
			id, "terminal_event_missing", DomainRuntimeState,
			"Runtime phase is terminal without a terminal Event",
		))
	}
	if facts.MailboxCapacity == 0 || facts.MailboxCount > facts.MailboxCapacity {
		findings = append(findings, finding(
			id, "mailbox_capacity_exceeded", DomainRuntimeState,
			"Runtime mailbox exceeds its declared capacity",
		))
	}
	if facts.BudgetLimit == 0 || facts.BudgetUsed > facts.BudgetLimit {
		findings = append(findings, finding(
			id, "budget_exceeded", DomainRuntimeState,
			"Runtime usage exceeds its declared budget",
		))
	}
	if facts.Projection.EvidenceAvailable {
		projection := facts.Projection
		if projection.RuntimeTerminal != runtimeTerminal ||
			projection.HostTerminal != runtimeTerminal {
			findings = append(findings, finding(
				id, "projection_terminal_drift", DomainHostProjection,
				"Runtime and Host terminal projections disagree",
			))
		}
		if projection.DuplicateItems != 0 {
			findings = append(findings, finding(
				id, "projection_duplicate_item", DomainHostProjection,
				"Host projection contains duplicate items",
			))
		}
		if projection.MissingItems != 0 {
			findings = append(findings, finding(
				id, "projection_missing_item", DomainHostProjection,
				"Host projection is missing Runtime items",
			))
		}
	}
	return complete(id, findings)
}

func evaluateSideEffect(input Input) Result {
	const id = SideEffectID
	if !input.EffectsAvailable {
		return invalidEvidence(id, "side-effect evidence is unavailable")
	}
	if len(input.Effects) != input.ExpectedEffectCount {
		return complete(id, []Finding{finding(
			id, "effect_expected_cardinality", DomainToolOrGuard,
			fmt.Sprintf(
				"observed %d effects, expected %d",
				len(input.Effects),
				input.ExpectedEffectCount,
			),
		)})
	}
	var findings []Finding
	for _, effect := range input.Effects {
		switch {
		case effect.Claims != 1:
			findings = append(findings, finding(
				id, "effect_claim_cardinality", DomainToolOrGuard,
				fmt.Sprintf("effect %s has %d claims", effect.ID, effect.Claims),
			))
		case effect.Executions > 1:
			findings = append(findings, finding(
				id, "effect_duplicate_execution", DomainToolOrGuard,
				fmt.Sprintf("effect %s executed %d times", effect.ID, effect.Executions),
			))
		case effect.Results > 1:
			findings = append(findings, finding(
				id, "effect_duplicate_result", DomainToolOrGuard,
				fmt.Sprintf("effect %s produced %d results", effect.ID, effect.Results),
			))
		case effect.Executions != effect.Results:
			findings = append(findings, finding(
				id, "effect_result_mismatch", DomainToolOrGuard,
				fmt.Sprintf("effect %s execution/result counts differ", effect.ID),
			))
		}
		if effect.Consequential && effect.Executions > 0 && !effect.Guarded {
			findings = append(findings, finding(
				id, "effect_guard_bypass", DomainToolOrGuard,
				fmt.Sprintf("consequential effect %s bypassed Guard", effect.ID),
			))
		}
		if effect.ApprovalRequired && !effect.ApprovalBound {
			findings = append(findings, finding(
				id, "effect_approval_unbound", DomainToolOrGuard,
				fmt.Sprintf("effect %s is not bound to its approval", effect.ID),
			))
		}
	}
	return complete(id, findings)
}

func evaluateWorkspace(input Input) Result {
	const id = WorkspaceID
	facts := input.Workspace
	if !facts.EvidenceAvailable {
		return invalidEvidence(id, "workspace evidence is unavailable")
	}
	var findings []Finding
	if facts.EscapedRoot {
		findings = append(findings, finding(
			id, "workspace_root_escape", DomainToolOrGuard,
			"workspace mutation escaped the declared root",
		))
	}
	for _, changed := range facts.ChangedPaths {
		if !matchesAny(changed, facts.AllowedPaths) {
			findings = append(findings, finding(
				id, "workspace_scope_violation", DomainToolOrGuard,
				fmt.Sprintf("changed path %s is outside the allowed scope", changed),
			))
		}
	}
	for _, dirty := range facts.PreexistingDirty {
		if !slices.Contains(facts.PreservedDirty, dirty) {
			findings = append(findings, finding(
				id, "workspace_dirty_change_lost", DomainToolOrGuard,
				fmt.Sprintf("pre-existing dirty path %s was not preserved", dirty),
			))
		}
	}
	return complete(id, findings)
}

func evaluateVerification(input Input) Result {
	const id = VerificationID
	facts := input.Verification
	if !facts.EvidenceAvailable || len(facts.Commands) == 0 {
		return invalidEvidence(id, "verification evidence is unavailable")
	}
	var findings []Finding
	unavailable := false
	mandatory := 0
	for _, command := range facts.Commands {
		if !command.Mandatory {
			continue
		}
		mandatory++
		switch command.Status {
		case spec.StatusPassed:
		case spec.StatusUnavailable, spec.StatusNotEvaluated:
			unavailable = true
		default:
			code := "verification_failed"
			if command.TimedOut {
				code = "verification_timeout"
			}
			findings = append(findings, finding(
				id, code, DomainPlatformEnvironment,
				fmt.Sprintf("mandatory verification %s did not pass", command.ID),
			))
		}
	}
	if mandatory == 0 {
		return invalidEvidence(id, "verification has no mandatory command")
	}
	if len(findings) != 0 {
		return complete(id, findings)
	}
	if unavailable {
		return Result{
			OracleID: id,
			Status:   spec.StatusUnavailable,
			Summary:  "mandatory verification is unavailable",
		}
	}
	return passed(id)
}

func evaluatePersistence(input Input) Result {
	const id = PersistenceID
	facts := input.Persistence
	if !facts.EvidenceAvailable {
		return invalidEvidence(id, "persistence evidence is unavailable")
	}
	var findings []Finding
	if facts.EventDigest != facts.ReplayDigest {
		findings = append(findings, finding(
			id, "replay_drift", DomainPersistenceOrRecovery,
			"Event and Replay semantic digests differ",
		))
	}
	if facts.SnapshotDigest != facts.RebuiltDigest {
		findings = append(findings, finding(
			id, "snapshot_rebuild_drift", DomainPersistenceOrRecovery,
			"Snapshot and rebuilt state digests differ",
		))
	}
	if facts.TerminalDigest != facts.ReceiptTerminalDigest {
		findings = append(findings, finding(
			id, "receipt_terminal_drift", DomainPersistenceOrRecovery,
			"Receipt does not bind the durable terminal digest",
		))
	}
	if facts.OutboxPublications != 1 {
		findings = append(findings, finding(
			id, "outbox_publication_cardinality", DomainPersistenceOrRecovery,
			fmt.Sprintf("terminal outbox published %d times", facts.OutboxPublications),
		))
	}
	return complete(id, findings)
}

func evaluateHost(input Input) Result {
	const id = HostID
	facts := input.Host
	if !facts.EvidenceAvailable {
		return invalidEvidence(id, "host evidence is unavailable")
	}
	var findings []Finding
	if facts.RuntimeTerminal == "" ||
		facts.RuntimeTerminal != facts.VisibleTerminal {
		findings = append(findings, finding(
			id, "host_terminal_drift", DomainHostProjection,
			"Host visible terminal does not match Runtime terminal",
		))
	}
	if facts.DuplicateItems != 0 {
		findings = append(findings, finding(
			id, "host_duplicate_item", DomainHostProjection,
			"Host projection contains duplicate items",
		))
	}
	if facts.MissingItems != 0 {
		findings = append(findings, finding(
			id, "host_missing_item", DomainHostProjection,
			"Host projection is missing Runtime items",
		))
	}
	if !facts.CursorCommitted || !facts.Continued {
		findings = append(findings, finding(
			id, "host_not_operational", DomainHostProjection,
			"Host did not commit its cursor or continue operating",
		))
	}
	return complete(id, findings)
}

func evaluateSecurity(input Input) Result {
	const id = SecurityID
	facts := input.Security
	if !facts.EvidenceAvailable {
		return invalidEvidence(id, "security evidence is unavailable")
	}
	var findings []Finding
	if facts.SecretLeaks != 0 {
		findings = append(findings, finding(
			id, "secret_leak", DomainToolOrGuard,
			"secret material reached an unauthorized sink",
		))
	}
	if facts.UnauthorizedEgress != 0 {
		findings = append(findings, finding(
			id, "unauthorized_egress", DomainToolOrGuard,
			"network egress occurred without authority",
		))
	}
	if facts.SandboxRequired && !facts.SandboxAvailable {
		findings = append(findings, finding(
			id, "sandbox_fail_open", DomainToolOrGuard,
			"required sandbox capability was unavailable without fail-closed",
		))
	}
	if facts.UnsafeLinks != 0 {
		findings = append(findings, finding(
			id, "unsafe_link", DomainToolOrGuard,
			"unsafe link escaped validation",
		))
	}
	if facts.PermissionBypass != 0 {
		findings = append(findings, finding(
			id, "permission_bypass", DomainToolOrGuard,
			"permission or constitution control was bypassed",
		))
	}
	return complete(id, findings)
}

func evaluateResource(input Input) Result {
	const id = ResourceID
	facts := input.Resources
	if !facts.EvidenceAvailable {
		return invalidEvidence(id, "resource evidence is unavailable")
	}
	var findings []Finding
	if facts.ProcessesAfter > facts.ProcessesBefore {
		findings = append(findings, finding(
			id, "process_leak", DomainRuntimeState,
			"child process count increased after the scenario",
		))
	}
	before := make(map[string]bool, len(facts.ProcessIDsBefore))
	for _, id := range facts.ProcessIDsBefore {
		before[id] = true
	}
	for _, processID := range facts.ProcessIDsAfter {
		if !before[processID] {
			findings = append(findings, finding(
				id, "process_identity_leak", DomainRuntimeState,
				fmt.Sprintf("owned process %s remains after the scenario", processID),
			))
		}
	}
	if facts.GoroutinesAfter > facts.GoroutinesBefore+facts.GoroutineTolerance {
		findings = append(findings, finding(
			id, "goroutine_leak", DomainRuntimeState,
			"goroutine count exceeded the declared tolerance",
		))
	}
	if facts.FDsAfter > facts.FDsBefore+facts.FDTolerance {
		findings = append(findings, finding(
			id, "fd_leak", DomainRuntimeState,
			"file descriptor count exceeded the declared tolerance",
		))
	}
	if facts.SubscribersAfter > facts.SubscribersBefore {
		findings = append(findings, finding(
			id, "subscriber_leak", DomainRuntimeState,
			"subscriber count increased after the scenario",
		))
	}
	if facts.TemporaryPathsRemaining != 0 {
		findings = append(findings, finding(
			id, "temporary_path_leak", DomainPlatformEnvironment,
			"temporary paths remain after cleanup",
		))
	}
	if facts.QueueCapacity == 0 || facts.QueuePeak > facts.QueueCapacity {
		findings = append(findings, finding(
			id, "queue_capacity_exceeded", DomainRuntimeState,
			"queue peak exceeded its declared capacity",
		))
	}
	if facts.InputBytes == 0 {
		findings = append(findings, finding(
			id, "amplification_denominator_missing", DomainEvaluationHarness,
			"persistence amplification input denominator is missing",
		))
	} else if facts.PersistedBytes*1000 >
		facts.InputBytes*facts.MaxAmplificationMilli {
		findings = append(findings, finding(
			id, "persistence_amplification", DomainPersistenceOrRecovery,
			"persistence amplification exceeded its declared limit",
		))
	}
	return complete(id, findings)
}

func evaluateTaskQuality(input Input) Result {
	const id = TaskQualityID
	facts := input.TaskQuality
	if !facts.EvidenceAvailable {
		return invalidEvidence(id, "task-quality evidence is unavailable")
	}
	if !facts.Deterministic {
		return invalidEvidence(id, "P0 task-quality evidence is not deterministic")
	}
	if facts.Assertions == 0 {
		return invalidEvidence(id, "task-quality assertion denominator is empty")
	}
	if facts.Passed != facts.Assertions {
		return complete(id, []Finding{finding(
			id, "task_assertion_failed", DomainTaskQuality,
			fmt.Sprintf(
				"task quality passed %d of %d assertions",
				facts.Passed,
				facts.Assertions,
			),
		)})
	}
	return passed(id)
}

func validateRequired(required []string) error {
	if len(required) == 0 {
		return errors.New("required oracle set is empty")
	}
	seen := make(map[string]struct{}, len(required))
	for _, id := range required {
		if !IsOracleID(id) {
			return fmt.Errorf("unknown oracle %q", id)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate required oracle %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func invalidReport(scenarioID, code, summary string) Report {
	finding := Finding{
		OracleID: "harness",
		Code:     code,
		Domain:   DomainEvaluationHarness,
		Severity: spec.RiskP0,
		Summary:  summary,
	}
	return Report{
		SchemaVersion: SchemaVersion,
		ScenarioID:    scenarioID,
		Status:        spec.StatusInvalid,
		Results: []Result{{
			OracleID: "harness",
			Status:   spec.StatusInvalid,
			Findings: []Finding{finding},
			Summary:  summary,
		}},
		Primary:          &finding,
		FailureSignature: finding.OracleID + ":" + finding.Code,
	}
}

func complete(id string, findings []Finding) Result {
	if len(findings) == 0 {
		return passed(id)
	}
	status := spec.StatusFailed
	for _, item := range findings {
		if item.Domain == DomainEvaluationHarness {
			status = spec.StatusInvalid
			break
		}
	}
	return Result{
		OracleID: id,
		Status:   status,
		Findings: findings,
		Summary:  fmt.Sprintf("%d invariant violation(s)", len(findings)),
	}
}

func passed(id string) Result {
	return Result{
		OracleID: id,
		Status:   spec.StatusPassed,
		Summary:  "all invariants passed",
	}
}

func notEvaluated(id, summary string) Result {
	return Result{
		OracleID: id,
		Status:   spec.StatusNotEvaluated,
		Summary:  summary,
	}
}

func invalidEvidence(id, summary string) Result {
	return Result{
		OracleID: id,
		Status:   spec.StatusInvalid,
		Summary:  summary,
		Findings: []Finding{finding(
			id,
			"required_evidence_invalid",
			DomainEvaluationHarness,
			summary,
		)},
	}
}

func hasEvidence(input Input, kind string) bool {
	for _, proof := range input.Evidence {
		if proof.Kind == kind && proof.RunPartition == input.RunPartition {
			return true
		}
	}
	return false
}

func finding(id, code string, domain Domain, summary string) Finding {
	return Finding{
		OracleID: id,
		Code:     code,
		Domain:   domain,
		Summary:  summary,
	}
}

func mergeStatus(current, next spec.Status) spec.Status {
	if statusRank(next) > statusRank(current) {
		return next
	}
	return current
}

func statusRank(status spec.Status) int {
	switch status {
	case spec.StatusInvalid:
		return 5
	case spec.StatusFailed:
		return 4
	case spec.StatusUnavailable:
		return 3
	case spec.StatusNotEvaluated:
		return 2
	case spec.StatusPassed:
		return 1
	default:
		return 6
	}
}

func matchesAny(candidate string, patterns []string) bool {
	for _, pattern := range patterns {
		switch {
		case strings.HasSuffix(pattern, "/**"):
			if strings.HasPrefix(candidate, strings.TrimSuffix(pattern, "**")) {
				return true
			}
		default:
			matched, err := path.Match(pattern, candidate)
			if err == nil && matched {
				return true
			}
		}
	}
	return false
}

func Clone(input Input) (Input, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return Input{}, err
	}
	var result Input
	if err := json.Unmarshal(encoded, &result); err != nil {
		return Input{}, err
	}
	return result, nil
}
