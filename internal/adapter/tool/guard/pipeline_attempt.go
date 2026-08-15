package guard

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

type retryKind string

const (
	retryNone    retryKind = ""
	retrySandbox retryKind = "sandbox_escalation"
	retryEgress  retryKind = "egress_approval"
)

type attemptRun struct {
	result       tool.Result
	outcome      tool.Outcome
	err          error
	retry        retryKind
	host         string
	protocol     string
	receipt      tool.AttemptReceipt
	dispatchWait time.Duration
	claimWait    time.Duration
	teardown     tool.TeardownReport
	aborted      bool
}

func (g *Guard) executePipeline(
	ctx context.Context,
	callID, name string,
	raw json.RawMessage,
	binding tool.CatalogBinding,
) (tool.Result, error) {
	mode := SandboxModeStrong
	egressRetried := false
	hooksStarted := false
	var receipt tool.ExecutionReceipt
	for {
		prepared, err := g.authorize(ctx, callID, name, raw, binding)
		receipt.ApprovalWait += prepared.waited
		if err != nil {
			return tool.Result{}, err
		}
		raw = append(json.RawMessage(nil), prepared.arguments...)
		if !hooksStarted && g.hooks != nil {
			if err := g.hooks.Before(ctx, prepared.invocation); err != nil {
				return tool.Result{}, err
			}
			hooksStarted = true
		}
		if prepared.invocation.Descriptor.SandboxRequirement == tool.SandboxNone {
			mode = SandboxModeNone
		}
		if receipt.Tool.Name == "" {
			receipt.Tool = prepared.invocation.Ref
			receipt.Source = prepared.invocation.Source
			receipt.Disposition = prepared.invocation.Disposition
		}
		attempt := g.runAttempt(
			ctx,
			prepared,
			mode,
			uint32(len(receipt.Attempts)+1),
			egressRetried,
		)
		receipt.Attempts = append(receipt.Attempts, attempt.receipt)
		receipt.DispatchWait += attempt.dispatchWait
		receipt.ClaimWait += attempt.claimWait
		switch attempt.retry {
		case retrySandbox:
			waited, approvalErr := g.approveSandboxEscalation(
				ctx,
				prepared.invocation,
				callID,
			)
			receipt.ApprovalWait += waited
			if approvalErr != nil {
				setExecutionTerminal(
					&receipt,
					terminalStatus(approvalErr, attempt.result),
					tool.TerminalOwnerGuard,
					attempt.teardown,
				)
				attachExecutionReceipt(&attempt.result, receipt)
				g.afterAttempt(ctx, prepared.invocation, attempt.result, approvalErr)
				return attempt.result, approvalErr
			}
			mode = SandboxModeNone
			continue
		case retryEgress:
			egressRetried = true
			started := g.now()
			approvalErr := g.approveEgressHost(
				ctx,
				prepared.invocation,
				callID,
				attempt.host,
				attempt.protocol,
			)
			receipt.ApprovalWait += g.now().Sub(started)
			if approvalErr == nil {
				continue
			}
			if softFailEgressApproval(approvalErr) {
				setExecutionTerminal(
					&receipt,
					attempt.receipt.Status,
					attempt.receipt.TerminalOwner,
					attempt.teardown,
				)
				attachExecutionReceipt(&attempt.result, receipt)
				g.afterAttempt(ctx, prepared.invocation, attempt.result, nil)
				if attempt.err != nil {
					return attempt.result, attempt.err
				}
				return attempt.result, nil
			}
			setExecutionTerminal(
				&receipt,
				terminalStatus(approvalErr, attempt.result),
				tool.TerminalOwnerGuard,
				attempt.teardown,
			)
			attachExecutionReceipt(&attempt.result, receipt)
			g.afterAttempt(ctx, prepared.invocation, attempt.result, approvalErr)
			return attempt.result, approvalErr
		}
		setExecutionTerminal(
			&receipt,
			attempt.receipt.Status,
			attempt.receipt.TerminalOwner,
			attempt.teardown,
		)
		attachExecutionReceipt(&attempt.result, receipt)
		g.afterAttempt(ctx, prepared.invocation, attempt.result, attempt.err)
		if attempt.err != nil {
			return attempt.result, attempt.err
		}
		return attempt.result, nil
	}
}

func (g *Guard) runAttempt(
	ctx context.Context,
	prepared preparedExecution,
	mode SandboxMode,
	sequence uint32,
	egressRetried bool,
) attemptRun {
	invocation := prepared.invocation
	started := g.now()
	run := attemptRun{}
	dispatchStarted := g.now()
	releaseAdmission, err := tool.AdmitExecution(ctx, invocation.Descriptor.ParallelPolicy)
	run.dispatchWait = g.now().Sub(dispatchStarted)
	if err != nil {
		run.err = err
		run.receipt = attemptReceipt(sequence, mode, started, g.now(), tool.OutcomeCanceled, "dispatch")
		return run
	}
	claimStarted := g.now()
	releaseClaims, err := g.registry.Claims().AcquireResources(ctx, invocation.Resources)
	run.claimWait = g.now().Sub(claimStarted)
	if err != nil {
		releaseAdmission()
		run.err = err
		run.receipt = attemptReceipt(sequence, mode, started, g.now(), tool.OutcomeCanceled, "claim")
		return run
	}
	release := func() {
		releaseClaims()
		releaseAdmission()
	}
	writePaths := invocationWritePaths(invocation)
	requireRead := mediatedFileWriter(invocation.Tool)
	expectedWrites, err := g.prepareFileWrites(ctx, writePaths, requireRead)
	if err != nil {
		release()
		run.err = err
		run.receipt = attemptReceipt(
			sequence, mode, started, g.now(), tool.OutcomeRejected, "prepare_writes",
		)
		return run
	}
	executeContext := workspacejournal.WithExpectedWrites(ctx, expectedWrites)
	readBefore, err := g.snapshotReadTarget(invocation)
	if err != nil {
		release()
		run.err = err
		run.receipt = attemptReceipt(
			sequence, mode, started, g.now(), tool.OutcomeFailed, "snapshot_read",
		)
		return run
	}
	runContext := WithSandboxAttempt(executeContext, SandboxAttempt{Mode: mode})
	var teardownMu sync.Mutex
	runContext = tool.WithTeardownObserver(
		runContext,
		func(report tool.TeardownReport) {
			teardownMu.Lock()
			run.teardown.Duration += report.Duration
			run.teardown.TimedOut = run.teardown.TimedOut || report.TimedOut
			teardownMu.Unlock()
		},
	)
	run.result, run.outcome, run.err, run.aborted = g.executePrepared(
		runContext,
		prepared,
	)
	if invocation.Tool == "file_read" && run.err == nil {
		if recordErr := g.recordFileRead(&run.result, invocation, readBefore); recordErr != nil {
			run.err = recordErr
		}
	}
	if len(writePaths) != 0 {
		if finishErr := g.finishFileWrites(
			ctx,
			writePaths,
			expectedWrites,
			&run.result,
			run.err == nil,
			mediatedFileWriter(invocation.Tool),
			mediatedFileWriter(invocation.Tool),
		); finishErr != nil && run.err == nil {
			run.err = finishErr
		}
	}
	release()
	teardownMu.Lock()
	teardown := run.teardown
	teardownMu.Unlock()
	run.teardown = teardown
	status := run.outcome.Status
	if status == "" {
		status = tool.OutcomeFromResult(run.result).Status
	}
	reason := ""
	if IsSandboxDenial(run.err, run.outcome) &&
		mode != SandboxModeNone &&
		g.canEscalate(invocation) {
		run.retry, reason = retrySandbox, string(retrySandbox)
	} else if !egressRetried {
		if host, protocol, ok := egressDeniedTarget(run.outcome, run.err); ok {
			run.retry, reason = retryEgress, string(retryEgress)
			run.host, run.protocol = host, protocol
		} else if run.err != nil {
			reason = "execute_error"
		} else if run.result.IsError {
			reason = "tool_error"
		}
	} else if run.err != nil {
		reason = "execute_error"
	} else if run.result.IsError {
		reason = "tool_error"
	}
	if errors.Is(run.err, context.Canceled) {
		status = tool.OutcomeCanceled
		reason = "context_canceled"
	} else if run.err != nil {
		status = tool.OutcomeFailed
	}
	run.receipt = attemptReceipt(sequence, mode, started, g.now(), status, reason)
	run.receipt.TerminalOwner = tool.TerminalOwnerExecutor
	if run.aborted {
		run.receipt.TerminalOwner = tool.TerminalOwnerGuard
	}
	run.receipt.Teardown = run.teardown.Duration
	run.receipt.TeardownMS = run.teardown.Duration.Milliseconds()
	run.receipt.TeardownTimedOut = run.teardown.TimedOut
	return run
}

type preparedOutcome struct {
	result  tool.Result
	outcome tool.Outcome
	err     error
}

func (g *Guard) executePrepared(
	ctx context.Context,
	prepared preparedExecution,
) (tool.Result, tool.Outcome, error, bool) {
	invocation := prepared.invocation
	if invocation.Disposition != tool.DispositionAbortImmediately {
		result, outcome, err := g.registry.ExecutePreparedOutcome(
			ctx,
			invocation.Tool,
			invocation.Arguments,
			prepared.executor,
		)
		return result, outcome, err, false
	}
	if err := ctx.Err(); err != nil {
		return tool.Result{}, tool.Outcome{Status: tool.OutcomeCanceled}, err, true
	}
	done := make(chan preparedOutcome, 1)
	go func() {
		result, outcome, err := g.registry.ExecutePreparedOutcome(
			ctx,
			invocation.Tool,
			invocation.Arguments,
			prepared.executor,
		)
		done <- preparedOutcome{result: result, outcome: outcome, err: err}
	}()
	select {
	case completed := <-done:
		return completed.result, completed.outcome, completed.err, false
	case <-ctx.Done():
		return tool.Result{},
			tool.Outcome{Status: tool.OutcomeCanceled},
			ctx.Err(),
			true
	}
}

func (g *Guard) snapshotReadTarget(
	invocation Invocation,
) (*workspacejournal.Fingerprint, error) {
	if invocation.Tool != "file_read" {
		return nil, nil
	}
	for _, resource := range invocation.Resources {
		if resource.Kind != "file" || resource.Path == "" {
			continue
		}
		value, _, _, err := workspacejournal.Snapshot(resource.Path)
		if err != nil {
			return nil, err
		}
		return &value, nil
	}
	return nil, nil
}

func (g *Guard) approveSandboxEscalation(
	ctx context.Context,
	invocation Invocation,
	callID string,
) (time.Duration, error) {
	escalation := policyInput(callID, invocation)
	escalation.Resources = withSandboxNoneResource(invocation.Resources)
	escalation.Sandbox = tool.SandboxNone
	now := g.now()
	if g.policy.Approvals != nil && g.policy.Approvals.MatchInvocation(escalation, now) {
		return 0, nil
	}
	started := g.now()
	approval, err := g.waitForApproval(
		ctx,
		invocation,
		escalation,
		now,
		approvalAsk{
			Code: ApprovalReasonSandboxEscalate,
			AllowedScopes: []policy.ApprovalScope{
				policy.ApprovalOnce, policy.ApprovalSession,
			},
			DisableReplace: true,
		},
	)
	waited := g.now().Sub(started)
	if err != nil {
		return waited, err
	}
	if err := g.cacheApproval(escalation, approval); err != nil {
		return waited, err
	}
	return waited, nil
}

func (g *Guard) afterAttempt(
	ctx context.Context,
	invocation Invocation,
	result tool.Result,
	err error,
) {
	if g.hooks != nil {
		g.hooks.After(ctx, invocation, result, err)
	}
}

func attemptReceipt(
	sequence uint32,
	mode SandboxMode,
	started, completed time.Time,
	status tool.OutcomeStatus,
	reason string,
) tool.AttemptReceipt {
	if status == "" {
		status = tool.OutcomeFailed
	}
	return tool.AttemptReceipt{
		Sequence: sequence, Sandbox: string(mode), Status: status,
		TerminalOwner: tool.TerminalOwnerGuard, Reason: reason,
		StartedAt: started, CompletedAt: completed,
		DurationMS: completed.Sub(started).Milliseconds(),
	}
}

func setExecutionTerminal(
	receipt *tool.ExecutionReceipt,
	status tool.OutcomeStatus,
	owner tool.TerminalOwner,
	teardown tool.TeardownReport,
) {
	if receipt == nil || receipt.TerminalStatus != "" {
		return
	}
	if status == "" {
		status = tool.OutcomeFailed
	}
	if owner == "" {
		owner = tool.TerminalOwnerGuard
	}
	receipt.TerminalStatus = status
	receipt.TerminalOwner = owner
	receipt.Teardown = teardown.Duration
	receipt.TeardownMS = teardown.Duration.Milliseconds()
	receipt.TeardownTimedOut = teardown.TimedOut
}

func terminalStatus(err error, result tool.Result) tool.OutcomeStatus {
	if errors.Is(err, context.Canceled) {
		return tool.OutcomeCanceled
	}
	if err != nil {
		var decision *policy.DecisionError
		if errors.As(err, &decision) {
			if decision.Code == "approval_canceled" {
				return tool.OutcomeCanceled
			}
			return tool.OutcomeRejected
		}
		return tool.OutcomeFailed
	}
	return tool.OutcomeFromResult(result).Status
}

func attachExecutionReceipt(result *tool.Result, receipt tool.ExecutionReceipt) {
	if result == nil {
		return
	}
	result.Execution = tool.CloneExecutionReceipt(&receipt)
}
