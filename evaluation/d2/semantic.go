package d2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

type SemanticCampaignOptions struct {
	Root    string
	ID      string
	Runtime string
	Output  string
	Lock    DiscoveryLock
	Now     func() time.Time
}

type SemanticRound struct {
	SchemaVersion         int              `json:"schema_version"`
	ID                    string           `json:"id"`
	DiscoveryLockIdentity string           `json:"discovery_lock_identity"`
	Status                string           `json:"status"`
	Scheduled             int              `json:"scheduled"`
	Settled               int              `json:"settled"`
	Passed                int              `json:"passed"`
	Failed                int              `json:"failed"`
	Invalid               int              `json:"invalid"`
	StartedAt             time.Time        `json:"started_at"`
	FinishedAt            time.Time        `json:"finished_at"`
	Cases                 []SemanticResult `json:"cases"`
	Observations          []Observation    `json:"observations"`
	ResourceClosure       string           `json:"resource_closure"`
	PrivacyClosure        string           `json:"privacy_closure"`
	EvidenceDigest        string           `json:"evidence_digest"`
}

type SemanticResult struct {
	ID              string `json:"id"`
	FamilyID        string `json:"family_id"`
	Seed            uint64 `json:"seed"`
	Status          string `json:"status"`
	Classification  string `json:"classification"`
	Reproducibility string `json:"reproducibility"`
	Attempts        int    `json:"attempts"`
	SummaryCode     string `json:"summary_code"`
	ExecutionDigest string `json:"execution_digest"`
	EvidenceDigest  string `json:"evidence_digest"`
}

type semanticCase struct {
	id        string
	family    string
	seed      uint64
	timeout   time.Duration
	invariant string
	run       func(context.Context, SemanticCampaignOptions) (string, error)
}

type semanticViolation struct {
	code     string
	evidence string
}

func (v semanticViolation) Error() string {
	return v.code
}

var semanticCases = []semanticCase{
	{
		id: "semantic-edit-approval-001", family: "approved_edit_verify",
		seed: 701, invariant: "approved edit creates verified content once",
		run: probeApprovedEdit,
	},
	{
		id:     "semantic-checkpoint-restore-001",
		family: "checkpoint_mutate_restore_continue",
		seed:   709, invariant: "Checkpoint restore never replays external side effects",
		run: probeCheckpointExternalPreservation,
	},
	{
		id: "semantic-workspace-drift-001", family: "concurrent_workspace_conflict",
		seed: 719, invariant: "stale edit plan cannot overwrite external workspace drift",
		run: probeWorkspaceDrift,
	},
	{
		id: "semantic-cancel-late-decision-001", family: "cancel_late_decision",
		seed: 727, invariant: "late approval cannot revive a canceled Turn",
		run: probeCancelLateDecision,
	},
	{
		id: "semantic-readonly-atomicity-001", family: "filesystem_pressure_mid_turn",
		seed: 733, invariant: "denied write leaves no partial workspace mutation",
		run: probeReadOnlyWorkspace,
	},
	{
		id: "semantic-provider-malformed-001", family: "provider_disconnect_mid_turn",
		seed: 739, invariant: "malformed Provider stream produces one failed Terminal",
		run: probeMalformedProvider,
	},
	{
		id:     "semantic-crash-pending-approval-001",
		family: "crash_pending_effect_recovery",
		seed:   743, invariant: "restart recovers or cancels a pending approval without mutation",
		run: probeCrashPendingApproval,
	},
	{
		id:     "semantic-compaction-restart-001",
		family: "checkpoint_compaction_restart",
		seed:   751, invariant: "approved mutation and compacted history survive restart exactly once",
		run: probeCompactionRestart,
	},
	{
		id:     "semantic-shared-state-sessions-001",
		family: "shared_persistence_concurrent_sessions",
		seed:   757, invariant: "independent Sessions sharing durable state both commit exactly once",
		run: probeSharedStateConcurrentSessions,
	},
	{
		id:     "semantic-dual-host-turn-001",
		family: "same_session_dual_host_concurrency",
		seed:   761, invariant: "one Thread cannot commit two concurrent active Turns",
		run: probeSameSessionDualHost,
	},
	{
		id:     "semantic-mcp-cancel-001",
		family: "mcp_disconnect_mid_turn",
		seed:   769, invariant: "canceling an in-flight MCP call closes one Turn and the MCP child",
		run: probeMCPCancel,
	},
	{
		id:     "semantic-multi-host-workspace-001",
		family: "multi_host_workspace_conflict",
		seed:   773, invariant: "concurrent edit plans cannot both commit over the same absent path",
		run: probeConcurrentWorkspaceEdits,
	},
	{
		id:     "semantic-duplicate-approval-001",
		family: "approval_idempotency",
		seed:   787, invariant: "a resolved approval cannot be accepted twice",
		run: probeDuplicateApproval,
	},
	{
		id:     "semantic-wrong-session-approval-001",
		family: "approval_session_isolation",
		seed:   797, invariant: "a Session cannot resolve another Session approval",
		run: probeWrongSessionApproval,
	},
	{
		id:     "semantic-expired-approval-001",
		family: "approval_expiry",
		seed:   809, invariant: "an expired approval receipt cannot authorize a Tool",
		run: probeExpiredApproval,
	},
	{
		id:     "semantic-plan-mismatch-001",
		family: "approval_plan_binding",
		seed:   811, invariant: "an approval with a mismatched Edit Plan cannot authorize a Tool",
		run: probeMismatchedPlanApproval,
	},
	{
		id:     "semantic-restore-active-001",
		family: "checkpoint_active_turn_exclusion",
		seed:   821, invariant: "Checkpoint restore cannot replace a Thread with a parked active Turn",
		run: probeRestoreDuringApproval,
	},
	{
		id:     "semantic-compact-active-001",
		family: "compaction_active_turn_exclusion",
		seed:   823, invariant: "Compaction cannot rewrite a Thread with a parked active Turn",
		run: probeCompactDuringApproval,
	},
	{
		id:     "semantic-fork-active-001",
		family: "fork_active_turn_exclusion",
		seed:   827, invariant: "Fork cannot block cancellation of a parked active Turn",
		run: probeForkDuringApproval,
	},
	{
		id:     "semantic-revert-active-001",
		family: "revert_active_turn_exclusion",
		seed:   829, invariant: "Revert cannot block cancellation of a parked active Turn",
		run: probeRevertDuringApproval,
	},
	{
		id:     "semantic-input-reply-001",
		family: "input_reply_resume",
		seed:   839, invariant: "a valid input reply resumes and completes its parked Turn exactly once",
		run: probeInputReply,
	},
	{
		id:     "semantic-duplicate-input-001",
		family: "input_reply_idempotency",
		seed:   853, invariant: "a resolved input request cannot be answered twice",
		run: probeDuplicateInputReply,
	},
	{
		id:     "semantic-wrong-session-input-001",
		family: "input_session_isolation",
		seed:   857, invariant: "a Session cannot answer another Session input request",
		run: probeWrongSessionInputReply,
	},
	{
		id:     "semantic-cancel-late-input-001",
		family: "cancel_late_input",
		seed:   859, invariant: "a late input reply cannot revive a canceled Turn",
		run: probeCancelLateInputReply,
	},
	{
		id:     "semantic-crash-pending-input-001",
		family: "crash_pending_input_recovery",
		seed:   863, invariant: "restart preserves a pending input request and resumes it exactly once",
		timeout: 45 * time.Second,
		run:     probeCrashPendingInput,
	},
}

func validateSemanticCatalog() error {
	if len(semanticCases) != 25 {
		return errors.New("D2 Semantic catalog must contain twenty-five Cases")
	}
	seenIDs := make(map[string]struct{}, len(semanticCases))
	seenFamilies := make(map[string]struct{}, len(semanticCases))
	for _, item := range semanticCases {
		if !validID(item.id) || !validID(item.family) ||
			item.seed == 0 || strings.TrimSpace(item.invariant) == "" ||
			item.run == nil {
			return fmt.Errorf("D2 Semantic Case %q is invalid", item.id)
		}
		if _, duplicate := seenIDs[item.id]; duplicate {
			return fmt.Errorf("duplicate D2 Semantic Case %q", item.id)
		}
		seenIDs[item.id] = struct{}{}
		seenFamilies[item.family] = struct{}{}
	}
	if len(seenFamilies) != len(semanticCases) {
		return errors.New("D2 Semantic families are not independently represented")
	}
	return nil
}

func RunSemanticCampaign(
	ctx context.Context,
	options SemanticCampaignOptions,
) (SemanticRound, error) {
	if err := validateSemanticCatalog(); err != nil {
		return SemanticRound{}, err
	}
	if !validID(options.ID) || options.Lock.Status != "qualified" {
		return SemanticRound{}, errors.New("D2 Semantic Campaign identity is invalid")
	}
	if err := options.Lock.Validate(); err != nil {
		return SemanticRound{}, err
	}
	if _, err := VerifyDiscoveryInputs(options.Root, options.Lock); err != nil {
		return SemanticRound{}, err
	}
	runtimeDigest, err := digestArtifact(options.Runtime)
	if err != nil {
		return SemanticRound{}, err
	}
	if runtimeDigest != options.Lock.RuntimeDigest {
		return SemanticRound{}, errors.New("D2 Semantic Runtime identity drifted")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	round := SemanticRound{
		SchemaVersion:         SchemaVersion,
		ID:                    options.ID,
		DiscoveryLockIdentity: options.Lock.LockIdentity,
		Status:                "closed",
		Scheduled:             len(semanticCases),
		StartedAt:             now().UTC(),
		Cases:                 make([]SemanticResult, len(semanticCases)),
		Observations:          []Observation{},
	}
	campaignContext, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	sem := make(chan struct{}, 2)
	var wait sync.WaitGroup
	for index, item := range semanticCases {
		index, item := index, item
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-campaignContext.Done():
				round.Cases[index] = invalidSemanticResult(
					item,
					"campaign_budget_exhausted",
					campaignContext.Err(),
				)
				return
			}
			caseTimeout := item.timeout
			if caseTimeout <= 0 {
				caseTimeout = 30 * time.Second
			}
			caseContext, caseCancel := context.WithTimeout(
				campaignContext,
				caseTimeout,
			)
			defer caseCancel()
			round.Cases[index] = runSemanticCase(
				caseContext,
				options,
				item,
			)
		}()
	}
	wait.Wait()
	round.FinishedAt = now().UTC()
	if round.FinishedAt.Before(round.StartedAt) {
		round.FinishedAt = time.Now().UTC()
	}
	environmentDigest := spec.DigestString(strings.Join([]string{
		options.Lock.RuntimeDigest,
		options.Lock.LockIdentity,
		"semantic-v1",
	}, "\x00"))
	for _, result := range round.Cases {
		round.Settled++
		switch result.Status {
		case "passed":
			round.Passed++
		case "failed":
			round.Failed++
		case "invalid":
			round.Invalid++
		}
		if result.Status == "passed" {
			continue
		}
		severity := "p1"
		if result.Classification == "harness_incident" {
			severity = "p2"
		}
		round.Observations = append(round.Observations, Observation{
			SchemaVersion:         SchemaVersion,
			ID:                    "observation-" + result.ID,
			CampaignID:            "semantic-in-path-v1",
			CaseID:                result.ID,
			DiscoveryLockIdentity: options.Lock.LockIdentity,
			EnvironmentDigest:     environmentDigest,
			Producer:              "d2-semantic-campaign",
			Classification:        result.Classification,
			Severity:              severity,
			Reproducibility:       result.Reproducibility,
			Attempts:              result.Attempts,
			EvidenceDigests:       []string{result.EvidenceDigest},
			FirstObservedAt:       round.FinishedAt,
			SummaryCode:           result.SummaryCode,
		})
	}
	slices.SortFunc(round.Observations, func(left, right Observation) int {
		return strings.Compare(left.ID, right.ID)
	})
	round.ResourceClosure = spec.DigestString(
		fmt.Sprintf("%s\x00semantic-resources\x00%d", round.ID, round.Settled),
	)
	round.PrivacyClosure = spec.DigestString(
		fmt.Sprintf("%s\x00semantic-privacy\x00%d", round.ID, len(round.Cases)),
	)
	round.EvidenceDigest = digestSemanticRound(round)
	return round, round.Validate()
}

func runSemanticCase(
	ctx context.Context,
	options SemanticCampaignOptions,
	item semanticCase,
) SemanticResult {
	evidence, err := item.run(ctx, options)
	result := SemanticResult{
		ID: item.id, FamilyID: item.family, Seed: item.seed,
		Attempts: 1, Status: "passed", Classification: "expected_variance",
		Reproducibility: "controlled_matrix", SummaryCode: "semantic_invariant_passed",
		ExecutionDigest: spec.DigestString(item.id + "\x00" + evidence),
	}
	var violation semanticViolation
	if err == nil {
		result.EvidenceDigest = digestSemanticResult(result)
		return result
	}
	if !errors.As(err, &violation) {
		return invalidSemanticResult(item, "semantic_harness_failed", err)
	}
	secondEvidence, secondErr := item.run(ctx, options)
	result.Attempts = 2
	var secondViolation semanticViolation
	if !errors.As(secondErr, &secondViolation) ||
		secondViolation.code != violation.code {
		result.Status = "invalid"
		result.Classification = "unattributed"
		result.Reproducibility = "unreproduced"
		result.SummaryCode = "semantic_failure_not_reproduced"
	} else {
		result.Status = "failed"
		result.Classification = "product_candidate"
		result.Reproducibility = "exact_seed"
		result.SummaryCode = violation.code
	}
	result.ExecutionDigest = spec.DigestString(strings.Join([]string{
		item.id,
		evidence,
		violation.evidence,
		secondEvidence,
		secondViolation.evidence,
	}, "\x00"))
	result.EvidenceDigest = digestSemanticResult(result)
	return result
}

func invalidSemanticResult(
	item semanticCase,
	code string,
	err error,
) SemanticResult {
	result := SemanticResult{
		ID: item.id, FamilyID: item.family, Seed: item.seed,
		Status: "invalid", Classification: "harness_incident",
		Reproducibility: "unreproduced", Attempts: 1,
		SummaryCode: code,
		ExecutionDigest: spec.DigestString(
			item.id + "\x00" + sanitizeError(err),
		),
	}
	result.EvidenceDigest = digestSemanticResult(result)
	return result
}

func (r SemanticRound) Validate() error {
	if r.SchemaVersion != SchemaVersion || !validID(r.ID) ||
		!validDigest(r.DiscoveryLockIdentity) || r.Status != "closed" ||
		r.Scheduled != len(semanticCases) || r.Settled != r.Scheduled ||
		len(r.Cases) != r.Scheduled ||
		r.Passed+r.Failed+r.Invalid != r.Settled ||
		r.StartedAt.IsZero() || r.FinishedAt.IsZero() ||
		!validDigest(r.ResourceClosure) ||
		!validDigest(r.PrivacyClosure) ||
		!validDigest(r.EvidenceDigest) {
		return errors.New("D2 Semantic Round inventory is invalid")
	}
	seen := make(map[string]struct{}, len(r.Cases))
	for _, result := range r.Cases {
		if err := result.Validate(); err != nil {
			return err
		}
		if _, duplicate := seen[result.ID]; duplicate {
			return fmt.Errorf("duplicate D2 Semantic Case %q", result.ID)
		}
		seen[result.ID] = struct{}{}
	}
	for _, observation := range r.Observations {
		if err := observation.Validate(); err != nil {
			return err
		}
	}
	if r.EvidenceDigest != digestSemanticRound(r) {
		return errors.New("D2 Semantic Round digest is invalid")
	}
	return nil
}

func (r SemanticResult) Validate() error {
	if !validID(r.ID) || !validID(r.FamilyID) || r.Seed == 0 ||
		!slices.Contains([]string{"passed", "failed", "invalid"}, r.Status) ||
		!slices.Contains([]string{
			"expected_variance", "product_candidate",
			"harness_incident", "unattributed",
		}, r.Classification) ||
		!slices.Contains([]string{
			"controlled_matrix", "exact_seed", "unreproduced",
		}, r.Reproducibility) ||
		r.Attempts < 1 || r.Attempts > 2 ||
		!validID(r.SummaryCode) ||
		!validDigest(r.ExecutionDigest) ||
		!validDigest(r.EvidenceDigest) {
		return fmt.Errorf("D2 Semantic Case %q is invalid", r.ID)
	}
	if r.Status == "failed" &&
		(r.Classification != "product_candidate" ||
			r.Reproducibility != "exact_seed" ||
			r.Attempts != 2) {
		return fmt.Errorf("D2 Semantic Case %q lacks exact reproduction", r.ID)
	}
	if r.EvidenceDigest != digestSemanticResult(r) {
		return fmt.Errorf("D2 Semantic Case %q digest is invalid", r.ID)
	}
	return nil
}

func WriteSemanticBundle(
	output string,
	round SemanticRound,
	lock DiscoveryLock,
) error {
	if err := round.Validate(); err != nil {
		return err
	}
	if round.DiscoveryLockIdentity != lock.LockIdentity {
		return errors.New("D2 Semantic bundle identity is inconsistent")
	}
	return writeAtomicBundle(output, []struct {
		name  string
		value any
	}{
		{"discovery-lock.json", lock},
		{"semantic-round.json", round},
	})
}

func digestSemanticResult(result SemanticResult) string {
	result.EvidenceDigest = ""
	raw, _ := json.Marshal(result)
	return spec.DigestString(string(raw))
}

func digestSemanticRound(round SemanticRound) string {
	round.EvidenceDigest = ""
	raw, _ := json.Marshal(round)
	return spec.DigestString(string(raw))
}
