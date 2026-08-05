package engine

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
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
	Mode        string   `json:"mode"`
	Action      string   `json:"action"`
	RepairSteps int      `json:"repair_steps"`
	Paths       []string `json:"paths,omitempty"`
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
	engine  *Engine
	repairs int
}

// extraSteps keeps repair rounds out of the model's normal step budget, so a
// gate failure never costs the model a step it would otherwise have used.
func (g *verifyGate) extraSteps() int {
	if g == nil {
		return 0
	}
	return g.repairs
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
		"scope": string(scope), "repair_step": g.repairs,
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
			return verifyOutcome{}, fmt.Errorf("verification (%s): %w", scope, err)
		}
		receipt = verify.Receipt{
			Scope: scope, Status: verify.StatusUnavailable, Message: err.Error(),
		}
	} else {
		span.Set("status", receipt.Status)
		span.End(trace.StatusOK)
	}
	action := g.decide(options, receipt)
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
		RepairSteps: g.repairs, Paths: paths,
	}
	if err := send(Verifying, Event{Verification: observed}); err != nil {
		return verifyOutcome{}, err
	}
	return verifyOutcome{action: action, receipt: observed}, nil
}

// decide maps a receipt to an action and spends the repair budget.
func (g *verifyGate) decide(options VerifyOptions, receipt verify.Receipt) verifyAction {
	if !receipt.Failed() {
		return verifyActionPassed
	}
	if g.repairs < options.MaxRepairSteps {
		g.repairs++
		return verifyActionRepair
	}
	if options.Mode == VerifyModeSoft {
		return verifyActionReported
	}
	if options.OnFailure == VerifyOnFailureRevert {
		return verifyActionReverted
	}
	return verifyActionFailed
}

// feedback is the repair prompt for the model. It uses a user message with the
// [verify] prefix (the convention mailbox already follows) rather than a faked
// tool result: the model never issued this call, and inventing a tool_call /
// tool_result pair pollutes the history and can trip provider-side pairing
// checks.
func verifyFeedback(receipt *VerificationReceipt, turn uint64) provider.Message {
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
