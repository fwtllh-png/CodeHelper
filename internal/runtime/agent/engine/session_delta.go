package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/compact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextstore"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/evidence"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/sessiondelta"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
)

type CompactionDelta = sessiondelta.Compaction
type SessionStateDelta = sessiondelta.State
type SessionDelta = sessiondelta.Delta

func prepareSessionDelta(
	turnID string,
	baseRevision uint64,
	history []provider.Message,
	usage provider.Usage,
	cost float64,
	state ...SessionStateDelta,
) (SessionDelta, error) {
	if turnID == "" {
		return SessionDelta{}, errors.New("session delta turn id is required")
	}
	if cost < 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
		return SessionDelta{}, errors.New("session delta cost is invalid")
	}
	var sessionState SessionStateDelta
	if len(state) != 0 {
		sessionState = state[0]
	}
	delta := SessionDelta{
		TurnID:         turnID,
		Version:        sessiondelta.ContextEnvelopeVersion,
		Epoch:          sessionState.Epoch,
		BaseRevision:   baseRevision,
		History:        cloneMessages(history),
		MessageTurns:   make([]uint64, len(history)),
		HistoryTurns:   cloneHistoryTurns(sessionState.HistoryTurns),
		Usage:          usage,
		CostMicrounits: uint64(math.Round(cost * 1_000_000)),
		WorkingSet:     sessionState.WorkingSet, Evidence: sessionState.Evidence,
		Failures: sessionState.Failures, Compaction: sessionState.Compaction,
		Plan: sessionState.Plan, World: contextstore.CloneWorldBaseline(sessionState.World),
		Workspace:      sessionState.Workspace,
		Window:         contextstore.CloneWindowLedger(sessionState.Window),
		ManifestLimits: sessionState.Manifest,
		Turn:           sessionState.Turn,
	}
	for _, message := range history {
		delta.Turn = max(delta.Turn, message.Turn)
	}
	for index, message := range history {
		delta.MessageTurns[index] = message.Turn
	}
	if sessionState.Turn != 0 &&
		historyContainsTurn(history, sessionState.Turn) {
		if delta.HistoryTurns == nil {
			delta.HistoryTurns = make(map[string]uint64)
		}
		delta.HistoryTurns[turnID] = sessionState.Turn
	}
	reconcileHistoryTurnBindings(
		delta.HistoryTurns,
		delta.MessageTurns,
	)
	payload, err := json.Marshal(delta)
	if err != nil {
		return SessionDelta{}, fmt.Errorf("encode session delta: %w", err)
	}
	sum := sha256.Sum256(payload)
	delta.Digest = hex.EncodeToString(sum[:])
	return delta, nil
}

func (e *Engine) stageSessionDelta(delta SessionDelta) {
	scope := e.runningScope()
	if scope == nil {
		return
	}
	scope.mu.Lock()
	scope.state.delta = &delta
	scope.mu.Unlock()
}

func (e *Engine) applySessionDelta() error {
	scope := e.runningScope()
	if scope == nil {
		return errors.New("turn scope is not active")
	}
	scope.mu.Lock()
	delta := scope.state.delta
	scope.mu.Unlock()
	if delta == nil {
		return nil
	}
	return e.applyDurableSessionDelta(*delta)
}

func (e *Engine) applyDurableSessionDelta(delta SessionDelta) error {
	key := fmt.Sprintf("%s:%d", delta.TurnID, delta.BaseRevision)
	if digest, ok := e.appliedDeltas[key]; ok {
		if digest == delta.Digest {
			return nil
		}
		return errors.New("session delta replay digest conflict")
	}
	if e.sessionRevision != delta.BaseRevision {
		return fmt.Errorf(
			"session delta revision conflict: have %d, base %d",
			e.sessionRevision,
			delta.BaseRevision,
		)
	}
	window := contextstore.CloneWindowLedger(delta.Window)
	if !window.Valid() {
		var err error
		window, err = createWindowLedger(1)
		if err != nil {
			return fmt.Errorf("restore token window: %w", err)
		}
	}
	if len(delta.MessageTurns) != 0 &&
		len(delta.MessageTurns) != len(delta.History) {
		return errors.New("session delta message turn count mismatch")
	}
	e.history = cloneMessages(delta.History)
	for index, turn := range delta.MessageTurns {
		e.history[index].Turn = turn
	}
	e.historyTurns = cloneHistoryTurns(delta.HistoryTurns)
	e.reconcileHistoryTurns(e.history, delta.TurnID, delta.Turn)
	e.turn = max(e.turn, delta.Turn)
	e.usage.Add(delta.Usage)
	e.costUSD += float64(delta.CostMicrounits) / 1_000_000
	e.working = workingset.ApplyDelta(delta.WorkingSet)
	e.evidence = evidence.ApplyDelta(delta.Evidence)
	e.failures = compact.ApplyFailureDelta(delta.Failures)
	e.compactions = delta.Compaction.Count
	e.contextCompaction =
		sessiondelta.CloneCompaction(delta.Compaction).State
	if delta.Plan != nil && len(delta.Plan.Steps) != 0 {
		e.setPlan(delta.Plan.Clone())
	}
	if contextstore.WorldBaselineValid(delta.History, delta.World) {
		e.world = contextstore.CloneWorldBaseline(delta.World)
	} else {
		e.world = contextstore.WorldBaseline{}
	}
	e.window = window
	e.stateEpoch = max(uint64(1), delta.Epoch)
	e.sessionRevision++
	e.appliedDeltas[key] = delta.Digest
	return nil
}

func (e *Engine) PreparedSessionDelta() (SessionDelta, bool) {
	scope := e.runningScope()
	if scope == nil {
		return SessionDelta{}, false
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.state.delta == nil {
		return SessionDelta{}, false
	}
	delta := *scope.state.delta
	delta.History = cloneMessages(delta.History)
	delta.MessageTurns = append([]uint64(nil), delta.MessageTurns...)
	delta.HistoryTurns = cloneHistoryTurns(delta.HistoryTurns)
	delta.World = contextstore.CloneWorldBaseline(delta.World)
	delta.Window = contextstore.CloneWindowLedger(delta.Window)
	delta.Compaction = sessiondelta.CloneCompaction(delta.Compaction)
	return delta, true
}

func (e *Engine) RestoreSessionDelta(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	var delta SessionDelta
	if err := json.Unmarshal(raw, &delta); err != nil {
		return fmt.Errorf("decode session delta: %w", err)
	}
	if delta.Version != sessiondelta.ContextEnvelopeVersion {
		return fmt.Errorf(
			"unsupported session delta version %d",
			delta.Version,
		)
	}
	digest := delta.Digest
	delta.Digest = ""
	payload, err := json.Marshal(delta)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(payload)
	if digest != hex.EncodeToString(sum[:]) {
		return errors.New("session delta digest mismatch")
	}
	delta.Digest = digest
	if e.sessionRevision == 0 && len(e.appliedDeltas) == 0 {
		e.sessionRevision = delta.BaseRevision
	}
	return e.applyDurableSessionDelta(delta)
}

func (e *Engine) SessionRevision() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sessionRevision
}

func (e *Engine) captureWorkspaceBinding() (sessiondelta.WorkspaceBinding, error) {
	return e.captureWorkspaceBindingFor(e.evidenceSet().RetainedDelta(
		e.options.Context.TruthRetention.FactMaxEntities,
		e.options.Context.TruthRetention.VerifiedChangeRetentionTurns,
		e.options.Context.TruthRetention.HandleMaxEntities,
	))
}

func (e *Engine) captureWorkspaceBindingFor(
	delta evidence.Delta,
) (sessiondelta.WorkspaceBinding, error) {
	paths := make(map[string]struct{})
	for _, fact := range delta.Facts {
		paths[fact.Path] = struct{}{}
	}
	for _, change := range delta.Changes {
		paths[change.Path] = struct{}{}
	}
	for _, read := range delta.Reads {
		paths[read.Path] = struct{}{}
	}
	values := make([]string, 0, len(paths))
	for path := range paths {
		values = append(values, path)
	}
	sort.Strings(values)
	return sessiondelta.CaptureWorkspaceBinding(
		e.options.Workspace,
		e.options.WorkspaceIdentity,
		e.sessionRevision,
		values,
	)
}
