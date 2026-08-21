package engine

import (
	"errors"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/compact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextstore"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/evidence"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/sessiondelta"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
)

func (e *Engine) ExportContextSnapshot() (sessiondelta.ContextSnapshot, error) {
	if e == nil {
		return sessiondelta.ContextSnapshot{}, errors.New("engine is nil")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.buildContextSnapshot(
		e.history,
		sessiondelta.CloneCompaction(sessiondelta.Compaction{
			Count: e.compactions, State: e.contextCompaction,
		}),
		max(uint64(1), e.sessionRevision),
		max(uint64(1), e.stateEpoch),
	)
}

func (e *Engine) buildContextSnapshot(
	history []provider.Message,
	compaction sessiondelta.Compaction,
	revision uint64,
	epoch uint64,
) (sessiondelta.ContextSnapshot, error) {
	e.planMu.Lock()
	plan := e.plan.Clone()
	e.planMu.Unlock()
	scope := e.runningScope()
	world := contextstore.CloneWorldBaseline(e.world)
	window := contextstore.CloneWindowLedger(e.window)
	working := e.working
	evidenceSet := e.evidence
	failures := e.failures
	if scope != nil {
		scope.mu.Lock()
		world = contextstore.CloneWorldBaseline(scope.state.world)
		window = contextstore.CloneWindowLedger(scope.state.window)
		working = scope.state.working
		evidenceSet = scope.state.evidence
		failures = scope.state.failures
		scope.mu.Unlock()
	}
	retainedWorking := working.RetainedDelta(
		e.turn,
		e.options.WorkingSetLimit,
		e.options.Context.TruthRetention.TruthMaxEntities,
	)
	retainedEvidence := evidenceSet.RetainedDelta(
		e.options.Context.TruthRetention.FactMaxEntities,
		e.options.Context.TruthRetention.VerifiedChangeRetentionTurns,
		e.options.Context.TruthRetention.HandleMaxEntities,
	)
	workspace, err := e.captureWorkspaceBindingFor(retainedEvidence)
	if err != nil {
		return sessiondelta.ContextSnapshot{}, err
	}
	snapshot := sessiondelta.ContextSnapshot{
		Version: sessiondelta.ContextSnapshotVersion,
		Epoch:   epoch, Revision: revision, Turn: e.turn,
		History:      cloneMessages(history),
		HistoryTurns: cloneHistoryTurns(e.historyTurns),
		WorkingSet:   retainedWorking,
		Evidence:     retainedEvidence,
		Failures:     failures.Delta(),
		Compaction:   sessiondelta.CloneCompaction(compaction),
		Plan:         &plan, World: world,
		Workspace: workspace,
		Window:    window,
	}
	if err := snapshot.Seal(); err != nil {
		return sessiondelta.ContextSnapshot{}, err
	}
	return snapshot, nil
}

func (e *Engine) RestoreContextSnapshot(
	snapshot sessiondelta.ContextSnapshot,
) (sessiondelta.ReconciliationReceipt, error) {
	if err := snapshot.Validate(); err != nil {
		return sessiondelta.ReconciliationReceipt{}, err
	}
	current, err := e.currentWorkspaceBinding(snapshot.Workspace)
	if err != nil {
		return sessiondelta.ReconciliationReceipt{}, err
	}
	reconciled, receipt, err := sessiondelta.ReconcileWorkspace(
		snapshot,
		current,
	)
	if err != nil {
		return sessiondelta.ReconciliationReceipt{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	reconciled.Revision = max(e.sessionRevision, snapshot.Revision) + 1
	reconciled.Epoch = max(e.stateEpoch, snapshot.Epoch) + 1
	reconciled.Window, err = contextstore.NewWindowLedger(
		fmt.Sprintf("checkpoint:%d:%d", reconciled.Epoch, reconciled.Revision),
		e.window.Number+1,
	)
	if err != nil {
		return sessiondelta.ReconciliationReceipt{}, err
	}
	if err := reconciled.Seal(); err != nil {
		return sessiondelta.ReconciliationReceipt{}, err
	}
	e.applyContextSnapshot(reconciled)
	return receipt, nil
}

func (e *Engine) ForkFromContextSnapshot(
	snapshot sessiondelta.ContextSnapshot,
) (*Engine, sessiondelta.ReconciliationReceipt, error) {
	if err := snapshot.Validate(); err != nil {
		return nil, sessiondelta.ReconciliationReceipt{}, err
	}
	current, err := e.currentWorkspaceBinding(snapshot.Workspace)
	if err != nil {
		return nil, sessiondelta.ReconciliationReceipt{}, err
	}
	reconciled, receipt, err := sessiondelta.ReconcileWorkspace(snapshot, current)
	if err != nil {
		return nil, sessiondelta.ReconciliationReceipt{}, err
	}
	options := e.options
	options.Guard = nil
	forked, err := New(options)
	if err != nil {
		return nil, sessiondelta.ReconciliationReceipt{}, err
	}
	reconciled.Epoch = 1
	reconciled.Revision = 1
	// A fork has a new Thread identity. An in-flight or completed compaction
	// fence belongs to the parent and must not be resumed in the child.
	reconciled.Compaction.State = nil
	reconciled.Window, err = contextstore.NewWindowLedger(
		fmt.Sprintf("checkpoint-fork:%s", snapshot.Digest),
		1,
	)
	if err != nil {
		return nil, sessiondelta.ReconciliationReceipt{}, err
	}
	if err := reconciled.Seal(); err != nil {
		return nil, sessiondelta.ReconciliationReceipt{}, err
	}
	forked.mu.Lock()
	forked.applyContextSnapshot(reconciled)
	forked.mu.Unlock()
	return forked, receipt, nil
}

func (e *Engine) currentWorkspaceBinding(
	checkpoint sessiondelta.WorkspaceBinding,
) (sessiondelta.WorkspaceBinding, error) {
	paths := make([]string, len(checkpoint.BoundPaths))
	for index, path := range checkpoint.BoundPaths {
		paths[index] = path.Path
	}
	return sessiondelta.CaptureWorkspaceBinding(
		e.options.Workspace,
		e.options.WorkspaceIdentity,
		e.sessionRevision,
		paths,
	)
}

// applyContextSnapshot changes only live Context state. Usage and cost remain
// monotonic accounting owned by the receiving Engine.
func (e *Engine) applyContextSnapshot(snapshot sessiondelta.ContextSnapshot) {
	e.history = cloneMessages(snapshot.History)
	for index, turn := range snapshot.MessageTurns {
		if index < len(e.history) {
			e.history[index].Turn = turn
		}
	}
	e.historyTurns = cloneHistoryTurns(snapshot.HistoryTurns)
	e.turn = max(e.turn, snapshot.Turn)
	e.working = workingset.ApplyDelta(snapshot.WorkingSet)
	e.evidence = evidence.ApplyDelta(snapshot.Evidence)
	e.failures = compact.ApplyFailureDelta(snapshot.Failures)
	e.compactions = snapshot.Compaction.Count
	e.contextCompaction = sessiondelta.CloneCompaction(snapshot.Compaction).State
	if snapshot.Plan != nil {
		e.setPlan(snapshot.Plan.Clone())
	} else {
		e.setPlan(interact.Plan{})
	}
	if contextstore.WorldBaselineValid(snapshot.History, snapshot.World) {
		e.world = contextstore.CloneWorldBaseline(snapshot.World)
	} else {
		e.world = contextstore.WorldBaseline{}
	}
	e.window = contextstore.CloneWindowLedger(snapshot.Window)
	e.stateEpoch = snapshot.Epoch
	e.sessionRevision = snapshot.Revision
	e.appliedDeltas = make(map[string]string)
	e.scopeMu.Lock()
	e.lastScope = &Scope{engine: e, state: newScopeState(e)}
	e.scopeMu.Unlock()
}
