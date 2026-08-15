package guard

import (
	"context"
	"encoding/json"
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
			attachExecutionReceipt(&attempt.result, receipt)
			if softFailEgressApproval(approvalErr) {
				g.afterAttempt(ctx, prepared.invocation, attempt.result, nil)
				if attempt.err != nil {
					return attempt.result, attempt.err
				}
				return attempt.result, nil
			}
			g.afterAttempt(ctx, prepared.invocation, attempt.result, approvalErr)
			return attempt.result, approvalErr
		}
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
	run.result, run.outcome, run.err = g.registry.ExecutePreparedOutcome(
		runContext,
		invocation.Tool,
		invocation.Arguments,
		prepared.executor,
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
	if run.err != nil {
		status = tool.OutcomeFailed
	}
	run.receipt = attemptReceipt(sequence, mode, started, g.now(), status, reason)
	return run
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
		Sequence: sequence, Sandbox: string(mode), Status: status, Reason: reason,
		StartedAt: started, CompletedAt: completed,
		DurationMS: completed.Sub(started).Milliseconds(),
	}
}

func attachExecutionReceipt(result *tool.Result, receipt tool.ExecutionReceipt) {
	if result == nil {
		return
	}
	result.Execution = tool.CloneExecutionReceipt(&receipt)
}
