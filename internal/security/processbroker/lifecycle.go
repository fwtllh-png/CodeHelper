package processbroker

import (
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/authority"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type LifecycleRequest struct {
	Lease      authority.ExecutionLease
	Validation authority.LeaseValidation
	Options    process.Options
	Identity   Identity
}

type Lifecycle struct {
	authority  *authority.LeaseAuthority
	lease      authority.ExecutionLease
	handle     authority.ProcessHandleCapability
	process    *process.StreamManaged
	identity   Identity
	processID  string
	generation uint64

	mu       sync.Mutex
	closed   bool
	terminal *LifecycleSnapshot
}

type LifecycleSnapshot struct {
	Lease  authority.LeaseSnapshot
	Handle authority.ProcessHandleSnapshot
}

func (b *Broker) StartLifecycle(
	ctx context.Context,
	request LifecycleRequest,
) (*Lifecycle, error) {
	if b == nil || b.authority == nil {
		return nil, errors.New("process broker is required")
	}
	if err := validateCommand(CommandRequest{
		Validation: request.Validation, Options: request.Options,
	}); err != nil {
		return nil, err
	}
	if err := b.authority.Consume(request.Lease, request.Validation); err != nil {
		return nil, err
	}
	settleFailure := func(reason string, cause error) error {
		return errors.Join(
			cause,
			b.authority.Settle(request.Lease, authority.Settlement{
				Status: "failed", Reason: reason, CompletedAt: time.Now().UTC(),
			}),
		)
	}
	snapshot, err := b.authority.Snapshot(request.Lease)
	if err != nil {
		return nil, settleFailure("lease_snapshot", err)
	}
	runCtx, err := sandbox.WithExecutionAuthority(
		context.WithoutCancel(ctx),
		snapshot.PermissionProfile.ExecutionAuthorityFor(
			request.Validation.Operation,
		),
	)
	if err != nil {
		return nil, settleFailure("authority_context", err)
	}
	running, err := process.StartStreamManaged(runCtx, request.Options)
	if err != nil {
		return nil, settleFailure("runner_failure", err)
	}
	generation := b.generation.Add(1)
	processID := strconv.Itoa(running.PID())
	handle, err := b.authority.IssueProcessHandle(
		request.Lease,
		authority.ProcessHandleRequest{
			SessionID: request.Identity.SessionID,
			ThreadID:  request.Identity.ThreadID, TurnID: request.Identity.TurnID,
			ProcessID: processID, Generation: generation,
			Actions: []authority.ProcessAction{
				authority.ProcessObserve, authority.ProcessStdin,
				authority.ProcessSignal, authority.ProcessWait, authority.ProcessCancel,
			},
		},
	)
	if err != nil {
		_ = running.Cancel()
		_ = running.Wait(context.Background())
		return nil, settleFailure("handle_issue", err)
	}
	return &Lifecycle{
		authority: b.authority, lease: request.Lease, handle: handle,
		process: running, identity: request.Identity,
		processID: processID, generation: generation,
	}, nil
}

func (p *Lifecycle) Stdin() (io.WriteCloser, error) {
	if err := p.validate(authority.ProcessStdin); err != nil {
		return nil, err
	}
	return p.process.Stdin(), nil
}

func (p *Lifecycle) Snapshot() (LifecycleSnapshot, error) {
	if p == nil || p.authority == nil {
		return LifecycleSnapshot{}, errors.New("process lifecycle is required")
	}
	p.mu.Lock()
	if p.terminal != nil {
		snapshot := *p.terminal
		p.mu.Unlock()
		return snapshot, nil
	}
	p.mu.Unlock()
	lease, err := p.authority.Snapshot(p.lease)
	if err != nil {
		return LifecycleSnapshot{}, err
	}
	handle, err := p.authority.ProcessHandleSnapshot(p.handle)
	if err != nil {
		return LifecycleSnapshot{}, err
	}
	return LifecycleSnapshot{Lease: lease, Handle: handle}, nil
}

func (p *Lifecycle) Stdout() (io.ReadCloser, error) {
	if err := p.validate(authority.ProcessObserve); err != nil {
		return nil, err
	}
	return p.process.Stdout(), nil
}

func (p *Lifecycle) Stderr() (io.ReadCloser, error) {
	if err := p.validate(authority.ProcessObserve); err != nil {
		return nil, err
	}
	return p.process.Stderr(), nil
}

func (p *Lifecycle) Signal(signal os.Signal) error {
	if err := p.validate(authority.ProcessSignal); err != nil {
		return err
	}
	return p.process.Signal(signal)
}

func (p *Lifecycle) Wait(ctx context.Context) error {
	if err := p.validate(authority.ProcessWait); err != nil {
		return err
	}
	err := p.process.Wait(ctx)
	if err == nil || !errors.Is(err, context.Canceled) {
		status, reason := "succeeded", "process_exited"
		if err != nil {
			status, reason = "failed", "command_failed"
		}
		return errors.Join(err, p.settle(status, reason))
	}
	return err
}

func (p *Lifecycle) Close(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()
	if err := p.validate(authority.ProcessCancel); err != nil {
		return err
	}
	_ = p.process.Cancel()
	_ = p.process.Wait(context.WithoutCancel(ctx))
	return p.settle("canceled", "lifecycle_closed")
}

func (p *Lifecycle) validate(action authority.ProcessAction) error {
	if p == nil || p.authority == nil {
		return errors.New("process lifecycle is required")
	}
	return p.authority.ValidateProcessHandle(
		p.handle,
		p.identity.SessionID, p.identity.ThreadID, p.identity.TurnID,
		p.processID, p.generation, action,
	)
}

func (p *Lifecycle) settle(status, reason string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed && status != "canceled" {
		return nil
	}
	p.closed = true
	completeErr := p.authority.CompleteProcessHandle(p.handle)
	settleErr := p.authority.Settle(p.lease, authority.Settlement{
		Status: status, Reason: reason, CompletedAt: time.Now().UTC(),
	})
	leaseSnapshot, leaseErr := p.authority.Snapshot(p.lease)
	handleSnapshot, handleErr := p.authority.ProcessHandleSnapshot(p.handle)
	if leaseErr == nil && handleErr == nil {
		p.terminal = &LifecycleSnapshot{
			Lease: leaseSnapshot, Handle: handleSnapshot,
		}
	}
	releaseErr := p.authority.Release(p.lease)
	return errors.Join(
		completeErr, settleErr, leaseErr, handleErr, releaseErr,
	)
}
