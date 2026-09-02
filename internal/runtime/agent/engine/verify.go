package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	"github.com/fwtllh-png/QCode/internal/observability/trace"
	"github.com/fwtllh-png/QCode/internal/observability/verify"
	agentcontext "github.com/fwtllh-png/QCode/internal/runtime/agent/context"
	"github.com/fwtllh-png/QCode/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

const (
	VerifyModeOff  = verify.ModeOff
	VerifyModeSoft = verify.ModeSoft
	VerifyModeHard = verify.ModeHard

	VerifyOnFailureFail   = verify.OnFailureFail
	VerifyOnFailureRevert = verify.OnFailureRevert
)

type VerifyOptions = verify.GateOptions
type VerificationReceipt = verify.GateReceipt
type VerificationWorkspace = verify.Workspace

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
	kernel   *turnkernel.RuntimeKernel
	attempts []verify.Receipt
}

// extraSteps keeps repair rounds out of the model's normal step budget, so a
// gate failure never costs the model a step it would otherwise have used.
func (g *verifyGate) extraSteps() int {
	if g == nil {
		return 0
	}
	return int(g.kernel.RepairSteps(turnkernel.RepairVerification))
}

// evaluate runs one verification pass over the files the turn changed.
func (g *verifyGate) evaluate(
	ctx context.Context, send func(State, Event) error,
) (verifyOutcome, error) {
	options := g.engine.options.Verify
	if !options.Enabled() {
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
	if err := g.kernel.BeginVerification(); err != nil {
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
		WorkspaceRevision: g.engine.sessionRevision, MutationRevision: g.kernel.MutationRevision(),
	})
	if err != nil {
		span.Set("error", errorText(err))
		span.End(trace.StatusError)
		// A soft gate must never change the turn outcome, so a runner that could
		// not run is reported as unavailable. A hard gate cannot be honoured
		// without a working runner, so the error stands.
		if options.Mode != VerifyModeSoft {
			_, _ = g.kernel.FinishVerification(
				g.verificationCommand(
					turnkernel.VerificationUnavailable,
					nil,
					err.Error(),
				),
			)
			return verifyOutcome{}, protocol.NewFault(
				protocol.CodeUnavailable,
				fmt.Sprintf("verification (%s) is unavailable", scope),
				true,
				protocol.FaultMetadata{
					Origin:         protocol.FaultOriginVerification,
					Disposition:    protocol.FaultResumeTurn,
					SideEffects:    protocol.SideEffectDraft,
					RecoveryAction: "restore verification and continue the retained draft",
				},
				err,
			)
		}
		receipt = verify.Receipt{
			Scope: scope, Status: verify.StatusUnavailable, Message: err.Error(),
		}
	} else {
		span.Set("status", receipt.Status)
		span.End(trace.StatusOK)
	}
	var uncovered []string
	mutationRevision := g.kernel.MutationRevision()
	if g.kernel.VerificationMustPass() &&
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
	actionValue, err := g.kernel.FinishVerification(
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
	for _, path := range paths {
		g.engine.contextAuthority().ObservePath(
			g.engine.options.Workspace,
			agentcontext.SourceVerified,
			g.engine.turn,
			path,
		)
	}
	// Only a pass clears the evidence gap. A failed or unavailable run leaves the
	// change exactly as unproved as it was before the gate ran.
	if receipt.Status == verify.StatusPassed {
		g.engine.contextAuthority().ObserveVerified(
			g.engine.options.Workspace,
			paths,
		)
	} else {
		// An unavailable run is recorded too, and says so: "nobody could check
		// this" is a different instruction to the next turn than "the check ran
		// and it is broken", and neither is silence.
		g.engine.contextAuthority().Failures().NoteVerify(
			g.engine.turn,
			string(scope),
			string(receipt.Status),
			verify.FailureDetail(receipt),
		)
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
		g.kernel.MutationRevision(),
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
	return verify.FeedbackMessage(receipt, turn)
}

func changedPaths(entries []turnkernel.TurnDiffEntry) []string {
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
