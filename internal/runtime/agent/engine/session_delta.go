package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/compact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/evidence"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
)

type CompactionDelta struct {
	Count int `json:"count"`
}

type SessionStateDelta struct {
	WorkingSet workingset.Delta     `json:"working_set"`
	Evidence   evidence.Delta       `json:"evidence"`
	Failures   compact.FailureDelta `json:"failures"`
	Compaction CompactionDelta      `json:"compaction"`
}

type SessionDelta struct {
	TurnID         string               `json:"turn_id"`
	BaseRevision   uint64               `json:"base_revision"`
	History        []provider.Message   `json:"history"`
	Usage          provider.Usage       `json:"usage"`
	CostMicrounits uint64               `json:"cost_microunits"`
	WorkingSet     workingset.Delta     `json:"working_set"`
	Evidence       evidence.Delta       `json:"evidence"`
	Failures       compact.FailureDelta `json:"failures"`
	Compaction     CompactionDelta      `json:"compaction"`
	Digest         string               `json:"digest"`
}

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
		BaseRevision:   baseRevision,
		History:        cloneMessages(history),
		Usage:          usage,
		CostMicrounits: uint64(math.Round(cost * 1_000_000)),
		WorkingSet:     sessionState.WorkingSet, Evidence: sessionState.Evidence,
		Failures: sessionState.Failures, Compaction: sessionState.Compaction,
	}
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
	e.history = cloneMessages(delta.History)
	e.usage.Add(delta.Usage)
	e.costUSD += float64(delta.CostMicrounits) / 1_000_000
	e.working = workingset.ApplyDelta(delta.WorkingSet)
	e.evidence = evidence.ApplyDelta(delta.Evidence)
	e.failures = compact.ApplyFailureDelta(delta.Failures)
	e.compactions = delta.Compaction.Count
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
