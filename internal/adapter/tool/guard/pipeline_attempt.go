package guard

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/security/authority"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type retryKind string

const (
	retryNone       retryKind = ""
	retryPermission retryKind = "additional_permission"
	retryEgress     retryKind = "egress_approval"
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
	profile      authority.EffectivePermissionProfile
	permission   authority.AdditionalPermissionRequest
}

func (g *Guard) executePipeline(
	ctx context.Context,
	callID, name string,
	raw json.RawMessage,
	binding tool.CatalogBinding,
) (tool.Result, error) {
	mode := SandboxModeStrong
	egressRetried := false
	permissionRetried := false
	hooksStarted := false
	var retryPrepared *preparedExecution
	var retryProfile *authority.EffectivePermissionProfile
	var receipt tool.ExecutionReceipt
	for {
		var prepared preparedExecution
		if retryPrepared != nil {
			prepared = *retryPrepared
			retryPrepared = nil
		} else {
			authorized, err := g.authorize(ctx, callID, name, raw, binding)
			receipt.ApprovalWait += authorized.waited
			if err != nil {
				result := tool.Result{IsError: true}
				if authorized.invocation.Ref.Name != "" {
					receipt.Tool = authorized.invocation.Ref
					receipt.Source = authorized.invocation.Source
					receipt.Disposition = authorized.invocation.Disposition
					setExecutionTerminal(
						&receipt,
						terminalStatus(err, result),
						tool.TerminalOwnerGuard,
						tool.TeardownReport{},
					)
					attachExecutionReceipt(&result, receipt)
				}
				return result, err
			}
			prepared = authorized
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
			permissionRetried,
			retryProfile,
		)
		retryProfile = nil
		receipt.Attempts = append(receipt.Attempts, attempt.receipt)
		receipt.DispatchWait += attempt.dispatchWait
		receipt.ClaimWait += attempt.claimWait
		switch attempt.retry {
		case retryPermission:
			waited, approvalErr := g.approveAdditionalPermission(
				ctx,
				prepared.invocation,
				callID,
				attempt.permission,
			)
			receipt.ApprovalWait += waited
			if approvalErr != nil {
				setAmendmentDecision(
					&receipt,
					amendmentDecision(approvalErr),
					"",
				)
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
			amended, amendErr := authority.Amend(
				attempt.profile,
				attempt.permission,
				uint64(len(receipt.Attempts)+1),
			)
			if amendErr != nil {
				setAmendmentDecision(&receipt, "failed", "")
				setExecutionTerminal(
					&receipt,
					tool.OutcomeFailed,
					tool.TerminalOwnerGuard,
					attempt.teardown,
				)
				attachExecutionReceipt(&attempt.result, receipt)
				g.afterAttempt(ctx, prepared.invocation, attempt.result, amendErr)
				return attempt.result, amendErr
			}
			setAmendmentDecision(&receipt, "approved", amended.Digest)
			updated, updateErr := g.applyAdditionalPermission(
				prepared,
				attempt.permission.Permission,
			)
			if updateErr != nil {
				setAmendmentDecision(&receipt, "failed", amended.Digest)
				setExecutionTerminal(
					&receipt,
					tool.OutcomeFailed,
					tool.TerminalOwnerGuard,
					attempt.teardown,
				)
				attachExecutionReceipt(&attempt.result, receipt)
				g.afterAttempt(ctx, prepared.invocation, attempt.result, updateErr)
				return attempt.result, updateErr
			}
			retryPrepared = &updated
			retryProfile = &amended
			permissionRetried = true
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
	permissionRetried bool,
	profileOverride *authority.EffectivePermissionProfile,
) attemptRun {
	invocation := prepared.invocation
	started := g.now()
	run := attemptRun{}
	var profile authority.EffectivePermissionProfile
	var err error
	if profileOverride == nil {
		profile, err = g.compileAuthority(prepared, mode, uint64(sequence))
	} else {
		profile = *profileOverride
		err = profile.Validate()
	}
	if err != nil || profile.Revision != uint64(sequence) {
		if err == nil {
			err = errors.New("additional permission profile revision is invalid")
		}
		run.err = err
		run.receipt = attemptReceipt(
			sequence, mode, started, g.now(), tool.OutcomeRejected, "authority_compile",
			run.profile,
		)
		return run
	}
	run.profile = profile
	dispatchStarted := g.now()
	releaseAdmission, err := tool.AdmitExecution(ctx, invocation.Descriptor.ParallelPolicy)
	run.dispatchWait = g.now().Sub(dispatchStarted)
	if err != nil {
		run.err = err
		run.receipt = attemptReceipt(
			sequence, mode, started, g.now(), tool.OutcomeCanceled, "dispatch", run.profile,
		)
		return run
	}
	claimStarted := g.now()
	releaseClaims, err := g.registry.Claims().AcquireResources(ctx, invocation.Resources)
	run.claimWait = g.now().Sub(claimStarted)
	if err != nil {
		releaseAdmission()
		run.err = err
		run.receipt = attemptReceipt(
			sequence, mode, started, g.now(), tool.OutcomeCanceled, "claim", run.profile,
		)
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
			run.profile,
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
			run.profile,
		)
		return run
	}
	runContext, err := authority.WithProfile(executeContext, profile)
	if err != nil {
		release()
		run.err = err
		run.receipt = attemptReceipt(
			sequence, mode, started, g.now(), tool.OutcomeRejected, "authority_context",
			run.profile,
		)
		return run
	}
	runContext, err = sandbox.WithExecutionAuthority(
		runContext,
		profile.ExecutionAuthority(),
	)
	if err != nil {
		release()
		run.err = err
		run.receipt = attemptReceipt(
			sequence, mode, started, g.now(), tool.OutcomeRejected, "authority_context",
			run.profile,
		)
		return run
	}
	runContext = WithSandboxAttempt(runContext, SandboxAttempt{Mode: mode})
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
	denial, denied := SandboxDenial(run.err, run.outcome)
	if denied &&
		mode == SandboxModeStrong &&
		!permissionRetried &&
		g.canEscalate(invocation) {
		request, requestErr := authority.RequestFromDenial(profile, denial)
		if requestErr == nil &&
			additionalPermissionAllowed(invocation, request.Permission) {
			run.retry, run.permission = retryPermission, request
			reason = string(retryPermission)
		} else {
			reason = "sandbox_denied_fail_closed"
		}
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
	run.receipt = attemptReceipt(
		sequence, mode, started, g.now(), status, reason, run.profile,
	)
	if denied {
		run.receipt.Denial = &denial
	}
	if run.retry == retryPermission {
		run.receipt.Amendment = amendmentReceipt(run.permission)
	}
	run.receipt.TerminalOwner = tool.TerminalOwnerExecutor
	if run.aborted {
		run.receipt.TerminalOwner = tool.TerminalOwnerGuard
	}
	run.receipt.Teardown = run.teardown.Duration
	run.receipt.TeardownMS = run.teardown.Duration.Milliseconds()
	run.receipt.TeardownTimedOut = run.teardown.TimedOut
	return run
}

func additionalPermissionAllowed(
	invocation Invocation,
	permission authority.AdditionalPermission,
) bool {
	switch permission.Kind {
	case authority.AdditionalPathRead:
		return true
	case authority.AdditionalPathWrite:
		return invocation.Descriptor.AccessMode != tool.AccessRead
	case authority.AdditionalNetwork:
		return invocation.Descriptor.Capability == tool.CapabilityNetwork ||
			invocation.Descriptor.Capability == tool.CapabilityProcess ||
			invocation.Descriptor.Capability == tool.CapabilityPlugin
	case authority.AdditionalProcess:
		return invocation.Descriptor.Capability == tool.CapabilityProcess ||
			invocation.Descriptor.Capability == tool.CapabilityPlugin
	default:
		return false
	}
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

func (g *Guard) approveAdditionalPermission(
	ctx context.Context,
	invocation Invocation,
	callID string,
	request authority.AdditionalPermissionRequest,
) (time.Duration, error) {
	escalation := policyInput(callID, invocation)
	escalation.Resources = append(
		append([]tool.Resource(nil), invocation.Resources...),
		authority.PermissionResource(request.Permission),
	)
	now := g.now()
	started := g.now()
	_, err := g.waitForApproval(
		ctx,
		invocation,
		escalation,
		now,
		approvalAsk{
			Code:                 ApprovalReasonAdditionalPermission,
			AllowedScopes:        []policy.ApprovalScope{policy.ApprovalOnce},
			DisableReplace:       true,
			AdditionalPermission: &request,
		},
	)
	waited := g.now().Sub(started)
	if err != nil {
		return waited, err
	}
	return waited, nil
}

func (g *Guard) applyAdditionalPermission(
	prepared preparedExecution,
	permission authority.AdditionalPermission,
) (preparedExecution, error) {
	resource := authority.PermissionResource(permission)
	resources := append(
		append([]tool.Resource(nil), prepared.invocation.Resources...),
		resource,
	)
	if err := g.checkControlPlaneWrites(resources); err != nil {
		return preparedExecution{}, err
	}
	prepared.invocation.Resources = resources
	return prepared, nil
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
	profile authority.EffectivePermissionProfile,
) tool.AttemptReceipt {
	if status == "" {
		status = tool.OutcomeFailed
	}
	receipt := tool.AttemptReceipt{
		Sequence: sequence, Sandbox: string(mode), Status: status,
		TerminalOwner: tool.TerminalOwnerGuard, Reason: reason,
		StartedAt: started, CompletedAt: completed,
		DurationMS: completed.Sub(started).Milliseconds(),
	}
	bindAttemptAuthority(&receipt, profile)
	return receipt
}

func bindAttemptAuthority(
	receipt *tool.AttemptReceipt,
	profile authority.EffectivePermissionProfile,
) {
	if receipt == nil || profile.Validate() != nil {
		return
	}
	receipt.PermissionSchemaVersion = profile.SchemaVersion
	receipt.PermissionRevision = profile.Revision
	receipt.PermissionDigest = profile.Digest
	receipt.PermissionCapability = profile.Capability
	receipt.PermissionAccess = profile.Access
	receipt.Enforcement = profile.Process.Enforcement
	receipt.Backend = profile.Process.Backend
	receipt.SandboxStrength = profile.Process.Strength
	receipt.WorkspaceRoot = profile.Filesystem.WorkspaceRoot
	receipt.ReadRoots = append([]string(nil), profile.Filesystem.ReadRoots...)
	receipt.WritePaths = append([]string(nil), profile.Filesystem.WritePaths...)
	receipt.DeniedWriteRoots = append(
		[]string(nil),
		profile.Filesystem.DeniedWriteRoots...,
	)
	receipt.WorkspaceBaseWrite = profile.Filesystem.WorkspaceBaseWrite
	receipt.FilesystemUnrestricted = profile.Filesystem.Unrestricted
	receipt.NetworkMode = profile.Network.Mode
	receipt.NetworkTargets = append([]string(nil), profile.Network.Targets...)
	receipt.ManagedProxyPort = profile.Network.ProxyPort
	receipt.LoopbackAllowed = profile.Network.Loopback
	receipt.ProcessAllowed = profile.Process.Allowed
	receipt.Provenance = make(
		[]tool.PermissionProvenance,
		len(profile.Provenance),
	)
	for index, source := range profile.Provenance {
		receipt.Provenance[index] = tool.PermissionProvenance{
			Kind: source.Kind, Value: source.Value,
			Digest: source.Digest, Revision: source.Revision,
		}
	}
}

func amendmentReceipt(
	request authority.AdditionalPermissionRequest,
) *tool.PermissionAmendmentReceipt {
	permission := request.Permission
	return &tool.PermissionAmendmentReceipt{
		BasePermissionDigest: request.BaseProfileDigest,
		Kind:                 string(permission.Kind), Resource: permission.Resource,
		Protocol: permission.Protocol, Port: permission.Port,
		Capability: permission.Capability, Decision: "requested",
	}
}

func amendmentDecision(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	return "rejected"
}

func setAmendmentDecision(
	receipt *tool.ExecutionReceipt,
	decision,
	amendedDigest string,
) {
	if receipt == nil || len(receipt.Attempts) == 0 {
		return
	}
	amendment := receipt.Attempts[len(receipt.Attempts)-1].Amendment
	if amendment == nil {
		return
	}
	amendment.Decision = decision
	amendment.AmendedPermissionDigest = amendedDigest
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
