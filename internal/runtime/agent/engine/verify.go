package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const (
	VerifyModeOff  = "off"
	VerifyModeSoft = "soft"
	VerifyModeHard = "hard"

	VerifyOnFailureFail   = "fail"
	VerifyOnFailureRevert = "revert"
)

// verifyFeedbackLimit bounds the failure text handed back to the model so a
// noisy suite cannot swallow the context window.
const verifyFeedbackLimit = 4 << 10

// VerifyOptions configures the gate that runs before a turn commits its edits.
type VerifyOptions struct {
	Mode           string
	Scope          verify.Scope
	OnFailure      string
	MaxRepairSteps int
	Timeout        time.Duration
	Runner         verify.Runner
}

func (o VerifyOptions) enabled() bool {
	return o.Runner != nil && (o.Mode == VerifyModeSoft || o.Mode == VerifyModeHard)
}

// VerificationReceipt is one gate evaluation as the hosts see it.
type VerificationReceipt struct {
	verify.Receipt
	Mode           string                 `json:"mode"`
	Action         string                 `json:"action"`
	RepairSteps    int                    `json:"repair_steps"`
	Paths          []string               `json:"paths,omitempty"`
	UncoveredPaths []string               `json:"uncovered_paths,omitempty"`
	Attempts       []verify.Receipt       `json:"attempts,omitempty"`
	Workspace      *VerificationWorkspace `json:"workspace,omitempty"`
}

// VerificationWorkspace is the final observable workspace state after the
// verification policy and any automatic rollback have settled.
type VerificationWorkspace struct {
	Status                     string   `json:"status"`
	Restored                   []string `json:"restored,omitempty"`
	Conflicts                  []string `json:"conflicts,omitempty"`
	NonFileSideEffectsReverted bool     `json:"non_file_side_effects_reverted"`
	Note                       string   `json:"note,omitempty"`
}

type verifyAction string

const (
	// verifyActionSkipped means the gate had nothing to judge: it is off, has no
	// runner, or the turn changed no files.
	verifyActionSkipped verifyAction = "skipped"
	verifyActionPassed  verifyAction = "passed"
	verifyActionRepair  verifyAction = "repair"
	// verifyActionReported is a soft-mode failure: recorded, turn unaffected.
	verifyActionReported verifyAction = "reported"
	verifyActionBlocked  verifyAction = "blocked"
	verifyActionFailed   verifyAction = "failed"
	verifyActionReverted verifyAction = "reverted"
)

type verifyOutcome struct {
	action  verifyAction
	receipt *VerificationReceipt
}

// verifyGate holds the per-turn gate state: how much of the repair budget the
// turn has spent, which is also the extra step allowance it has earned.
type verifyGate struct {
	engine   *Engine
	kernel   *engineTurnKernel
	attempts []verify.Receipt
}

// extraSteps keeps repair rounds out of the model's normal step budget, so a
// gate failure never costs the model a step it would otherwise have used.
func (g *verifyGate) extraSteps() int {
	if g == nil {
		return 0
	}
	return int(g.kernel.repairSteps(turnkernel.RepairVerification))
}

// evaluate runs one verification pass over the files the turn changed.
func (g *verifyGate) evaluate(
	ctx context.Context, send func(State, Event) error,
) (verifyOutcome, error) {
	options := g.engine.options.Verify
	if !options.enabled() {
		return verifyOutcome{action: verifyActionSkipped}, nil
	}
	paths := changedPaths(g.engine.TurnDiff())
	if len(paths) == 0 {
		return verifyOutcome{action: verifyActionSkipped}, nil
	}
	if g.kernel == nil {
		return verifyOutcome{}, protocol.NewProblem(
			protocol.CodeInternal,
			"turn kernel is required for verification",
			false,
			nil,
		)
	}
	if err := g.kernel.beginVerification(); err != nil {
		return verifyOutcome{}, err
	}
	scope := options.Scope
	if scope == "" {
		scope = verify.ScopeDiagnostics
	}
	verifyCtx := ctx
	if options.Timeout > 0 {
		var cancel context.CancelFunc
		verifyCtx, cancel = context.WithTimeout(ctx, options.Timeout)
		defer cancel()
	}
	span := g.engine.tracer().Start(trace.NameVerify, 0, map[string]any{
		"scope": string(scope), "repair_step": g.extraSteps(),
	})
	receipt, err := options.Runner.Verify(verifyCtx, verify.Request{
		Scope: scope, Paths: paths, Diagnostics: g.engine.turnDiagnostics(),
	})
	if err != nil {
		span.Set("error", errorText(err))
		span.End(trace.StatusError)
		// A soft gate must never change the turn outcome, so a runner that could
		// not run is reported as unavailable. A hard gate cannot be honoured
		// without a working runner, so the error stands.
		if options.Mode != VerifyModeSoft {
			_, _ = g.kernel.finishVerification(
				g.verificationCommand(
					turnkernel.VerificationUnavailable,
					nil,
					err.Error(),
				),
			)
			return verifyOutcome{}, fmt.Errorf("verification (%s): %w", scope, err)
		}
		receipt = verify.Receipt{
			Scope: scope, Status: verify.StatusUnavailable, Message: err.Error(),
		}
	} else {
		span.Set("status", receipt.Status)
		span.End(trace.StatusOK)
	}
	var uncovered []string
	mutationRevision := g.kernel.mutationRevision()
	if g.kernel.verificationMustPass() &&
		receipt.Status == verify.StatusUnavailable {
		qualityReceipt, missing := g.engine.qualityVerificationReceipt(
			paths,
			mutationRevision,
		)
		g.attempts = append(g.attempts, receipt)
		if qualityReceipt.Status == verify.StatusPassed {
			receipt = qualityReceipt
		} else {
			uncovered = missing
			if len(uncovered) != 0 {
				qualityReceipt.Message += "; uncovered_paths=" + strings.Join(uncovered, ",")
			}
			receipt = qualityReceipt
		}
	}
	actionValue, err := g.kernel.finishVerification(
		g.verificationCommand(
			kernelVerificationStatus(receipt.Status),
			currentVerificationCallIDs(g.engine, mutationRevision),
			receipt.Message,
		),
	)
	if err != nil {
		return verifyOutcome{}, err
	}
	action := verifyAction(actionValue)
	g.attempts = append(g.attempts, receipt)
	// A verified path outranks one that was merely edited: it is the path the
	// turn now owes an explanation for.
	g.engine.observePaths(workingset.SourceVerified, paths)
	// Only a pass clears the evidence gap. A failed or unavailable run leaves the
	// change exactly as unproved as it was before the gate ran.
	if receipt.Status == verify.StatusPassed {
		g.engine.observeVerifiedEvidence(paths)
	} else {
		// An unavailable run is recorded too, and says so: "nobody could check
		// this" is a different instruction to the next turn than "the check ran
		// and it is broken", and neither is silence.
		g.engine.observeVerifyFailure(string(scope), string(receipt.Status), failureDetail(receipt))
	}
	observed := &VerificationReceipt{
		Receipt: receipt, Mode: options.Mode, Action: string(action),
		RepairSteps: g.extraSteps(), Paths: paths,
		UncoveredPaths: append([]string(nil), uncovered...),
		Attempts:       append([]verify.Receipt(nil), g.attempts...),
	}
	if err := send(Verifying, Event{Verification: observed}); err != nil {
		return verifyOutcome{}, err
	}
	return verifyOutcome{action: action, receipt: observed}, nil
}

func (g *verifyGate) verificationCommand(
	status turnkernel.VerificationStatus,
	evidenceCalls []string,
	message string,
) turnkernel.VerificationFinished {
	key := fmt.Sprintf(
		"mutation=%d;status=%s;evidence=%s",
		g.kernel.mutationRevision(),
		status,
		strings.Join(evidenceCalls, ","),
	)
	return turnkernel.VerificationFinished{
		Status:        status,
		EvidenceCalls: evidenceCalls,
		Message:       message,
		RepairKey:     key,
	}
}

func kernelVerificationStatus(
	status string,
) turnkernel.VerificationStatus {
	switch status {
	case verify.StatusPassed:
		return turnkernel.VerificationPassed
	case verify.StatusFailed:
		return turnkernel.VerificationFailed
	default:
		return turnkernel.VerificationUnavailable
	}
}

func currentVerificationCallIDs(
	engine *Engine,
	mutationRevision uint64,
) []string {
	var callIDs []string
	for _, evidence := range engine.verificationEvidence() {
		if evidence.MutationRevision == mutationRevision &&
			evidence.CallID != "" {
			callIDs = append(callIDs, evidence.CallID)
		}
	}
	sort.Strings(callIDs)
	return callIDs
}

// feedback is the repair prompt for the model. It uses a user message with the
// [verify] prefix (the convention mailbox already follows) rather than a faked
// tool result: the model never issued this call, and inventing a tool_call /
// tool_result pair pollutes the history and can trip provider-side pairing
// checks.
func verifyFeedback(receipt *VerificationReceipt, turn uint64) provider.Message {
	if receipt != nil && receipt.Status == verify.StatusUnavailable {
		paths, _ := json.Marshal(receipt.UncoveredPaths)
		message := provider.TextMessage(
			provider.RoleUser,
			"[verify] structured verification is required before workspace_change completion.\n"+
				"required_action=quality_verify\n"+
				"retry_original=false\n"+
				"uncovered_paths="+string(paths)+"\n"+
				"Call quality_verify or quality_test after the last mutation with covered_paths "+
				"set to these exact uncovered_paths. Then call turn_complete again. "+
				"Do not enumerate the whole worktree and do not retry the original edit.",
		)
		message.Turn = turn
		return message
	}
	message := provider.TextMessage(
		provider.RoleUser,
		"[verify] "+receipt.Feedback(verifyFeedbackLimit)+
			"\nFix the cause and do not report success until verification passes.",
	)
	message.Turn = turn
	return message
}

func (r *VerificationReceipt) problemMessage() string {
	if r == nil {
		return "verification failed"
	}
	message := fmt.Sprintf("verification (%s) failed", r.Scope)
	if r.Message != "" {
		return message + ": " + r.Message
	}
	for _, check := range r.Checks {
		if check.Status == verify.StatusFailed {
			return message + ": " + check.Name
		}
	}
	return message
}

// failureDetail names what failed in one phrase: the runner's message when there
// is one, otherwise the first failing check. The whole feedback text is already
// in the history; the ledger only needs enough to recognise a repeat.
func failureDetail(receipt verify.Receipt) string {
	if receipt.Message != "" {
		return receipt.Message
	}
	for _, check := range receipt.Checks {
		if check.Status == verify.StatusFailed {
			return check.Name
		}
	}
	return ""
}

func changedPaths(entries []TurnDiffEntry) []string {
	unique := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.Path == "" {
			continue
		}
		unique[entry.Path] = struct{}{}
	}
	paths := make([]string, 0, len(unique))
	for path := range unique {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}
