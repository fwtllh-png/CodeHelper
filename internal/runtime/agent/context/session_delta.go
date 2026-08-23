// Package agentcontext defines the durable Agent session snapshot contract.
package agentcontext

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/durablecodec"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type Compaction struct {
	Count int              `json:"count"`
	State *CompactionState `json:"state,omitempty"`
}

type CompactionState struct {
	ID                  string                  `json:"id"`
	ThreadID            protocol.ThreadID       `json:"thread_id"`
	TurnID              protocol.TurnID         `json:"turn_id"`
	Phase               string                  `json:"phase"`
	PlanDigest          string                  `json:"plan_digest"`
	Plan                *CompactionPlan         `json:"plan,omitempty"`
	NarrativeInput      *NarrativeInputArtifact `json:"narrative_input,omitempty"`
	Narrative           *NarrativeArtifact      `json:"narrative,omitempty"`
	Truth               TruthCapsule            `json:"truth"`
	SourceWindowID      string                  `json:"source_window_id"`
	TargetWindowID      string                  `json:"target_window_id"`
	SourceContextDigest string                  `json:"source_context_digest"`
	Attempt             uint32                  `json:"attempt,omitempty"`
	FallbackReason      string                  `json:"fallback_reason,omitempty"`
}

func (c Compaction) Validate() error {
	if c.Count < 0 {
		return errors.New("compaction count is invalid")
	}
	if c.State == nil {
		return nil
	}
	state := c.State
	switch state.Phase {
	case "prepared", "generating_narrative", "rebasing", "fallback", "completed":
	default:
		return errors.New("compaction state phase is invalid")
	}
	if state.ID == "" || state.ThreadID == "" || state.TurnID == "" ||
		state.PlanDigest == "" || state.SourceWindowID == "" ||
		state.TargetWindowID == "" || state.SourceContextDigest == "" {
		return errors.New("compaction state identity is incomplete")
	}
	if err := state.Truth.Validate(); err != nil {
		return err
	}
	authorityDigest, err := state.Truth.AuthorityDigest()
	if err != nil {
		return err
	}
	if state.Plan != nil {
		if err := state.Plan.Validate(); err != nil {
			return err
		}
		if state.Plan.Digest != state.PlanDigest ||
			state.Plan.ID != state.ID ||
			state.Plan.SourceWindowID != state.SourceWindowID ||
			state.Plan.TargetWindowID != state.TargetWindowID ||
			state.Plan.SourceContextDigest != state.SourceContextDigest {
			return errors.New("compaction plan fence is inconsistent")
		}
	}
	if state.NarrativeInput != nil {
		if err := state.NarrativeInput.Validate(time.Time{}); err != nil {
			return err
		}
		if state.NarrativeInput.ThreadID != state.ThreadID ||
			state.NarrativeInput.SourceWindowID != state.SourceWindowID ||
			state.NarrativeInput.AuthorityDigest != authorityDigest {
			return errors.New("narrative input fence is inconsistent")
		}
	}
	if state.Narrative != nil {
		if err := state.Narrative.Validate(time.Time{}); err != nil {
			return err
		}
		if state.NarrativeInput == nil ||
			state.Narrative.InputDigest != state.NarrativeInput.Digest ||
			state.Narrative.RouteDigest != state.NarrativeInput.RouteDigest {
			return errors.New("narrative artifact input fence is inconsistent")
		}
		if state.Narrative.ThreadID != state.ThreadID ||
			state.Narrative.WindowID != state.SourceWindowID ||
			state.Narrative.AuthorityDigest != authorityDigest {
			return errors.New("narrative artifact fence is inconsistent")
		}
		sources := make(map[string]string, len(state.NarrativeInput.Excerpts))
		for _, excerpt := range state.NarrativeInput.Excerpts {
			sources[excerpt.MessageID] = excerpt.Digest
		}
		for _, item := range state.Narrative.Body.Items {
			digests := make([]string, len(item.SourceMessageIDs))
			for index, id := range item.SourceMessageIDs {
				digest, ok := sources[id]
				if !ok {
					return errors.New("narrative artifact source is unavailable")
				}
				digests[index] = digest
			}
			if digestText(strings.Join(digests, "\x00")) != item.SourceDigest {
				return errors.New("narrative artifact source digest is inconsistent")
			}
		}
	}
	if state.Plan == nil && state.NarrativeInput == nil &&
		state.FallbackReason == "" {
		return errors.New("compaction state has no durable input or fallback")
	}
	return nil
}

func digestText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func CloneCompaction(value Compaction) Compaction {
	if value.State == nil {
		return value
	}
	state := *value.State
	state.Truth.Entities = append(
		[]TruthEntity(nil),
		value.State.Truth.Entities...,
	)
	state.Truth.Omissions = append(
		[]Omission(nil),
		value.State.Truth.Omissions...,
	)
	for index := range state.Truth.Omissions {
		state.Truth.Omissions[index].SampleIDs = append(
			[]string(nil),
			state.Truth.Omissions[index].SampleIDs...,
		)
	}
	if value.State.Plan != nil {
		raw, _ := json.Marshal(value.State.Plan)
		var plan CompactionPlan
		_ = json.Unmarshal(raw, &plan)
		state.Plan = &plan
	}
	if value.State.NarrativeInput != nil {
		input := *value.State.NarrativeInput
		input.Excerpts = append(
			[]NarrativeExcerpt(nil),
			value.State.NarrativeInput.Excerpts...,
		)
		state.NarrativeInput = &input
	}
	if value.State.Narrative != nil {
		narrative := *value.State.Narrative
		narrative.Body.Items = append(
			[]NarrativeItem(nil),
			value.State.Narrative.Body.Items...,
		)
		for index := range narrative.Body.Items {
			narrative.Body.Items[index].SourceMessageIDs = append(
				[]string(nil),
				narrative.Body.Items[index].SourceMessageIDs...,
			)
		}
		state.Narrative = &narrative
	}
	value.State = &state
	return value
}

type SessionState struct {
	Epoch        uint64            `json:"epoch,omitempty"`
	Turn         uint64            `json:"turn,omitempty"`
	HistoryTurns map[string]uint64 `json:"history_turns,omitempty"`
	WorkingSet   WorkingSetDelta   `json:"working_set"`
	Evidence     EvidenceDelta     `json:"evidence"`
	Failures     FailureDelta      `json:"failures"`
	Compaction   Compaction        `json:"compaction"`
	Plan         *Plan             `json:"plan,omitempty"`
	World        WorldBaseline     `json:"world,omitempty"`
	Workspace    WorkspaceBinding  `json:"workspace,omitempty"`
	Window       WindowLedger      `json:"window"`
	Manifest     ManifestLimits    `json:"manifest_limits,omitempty"`
}

type SessionDelta struct {
	Version        int                `json:"version,omitempty"`
	Epoch          uint64             `json:"epoch,omitempty"`
	TurnID         string             `json:"turn_id"`
	Turn           uint64             `json:"turn,omitempty"`
	BaseRevision   uint64             `json:"base_revision"`
	History        []provider.Message `json:"history"`
	MessageTurns   []uint64           `json:"message_turns,omitempty"`
	HistoryTurns   map[string]uint64  `json:"history_turns,omitempty"`
	Usage          provider.Usage     `json:"usage"`
	CostMicrounits uint64             `json:"cost_microunits"`
	WorkingSet     WorkingSetDelta    `json:"working_set"`
	Evidence       EvidenceDelta      `json:"evidence"`
	Failures       FailureDelta       `json:"failures"`
	Compaction     Compaction         `json:"compaction"`
	Plan           *Plan              `json:"plan,omitempty"`
	World          WorldBaseline      `json:"world,omitempty"`
	Workspace      WorkspaceBinding   `json:"workspace,omitempty"`
	Window         WindowLedger       `json:"window"`
	ManifestLimits ManifestLimits     `json:"manifest_limits,omitempty"`
	Digest         string             `json:"digest"`
}

type deltaJSON SessionDelta

type SessionRestore struct {
	Key        string
	Digest     string
	Replay     bool
	Revision   uint64
	History    []provider.Message
	State      SessionState
	Accounting AccountingDelta
}

func (d SessionDelta) ReplayKey() string {
	return fmt.Sprintf("%s:%d", d.TurnID, d.BaseRevision)
}

func DecodeSessionDelta(raw json.RawMessage) (SessionDelta, error) {
	if len(raw) == 0 {
		return SessionDelta{}, nil
	}
	var delta SessionDelta
	if err := json.Unmarshal(raw, &delta); err != nil {
		return SessionDelta{}, fmt.Errorf("decode session delta: %w", err)
	}
	if delta.Version != ContextEnvelopeVersion {
		return SessionDelta{}, fmt.Errorf(
			"unsupported session delta version %d",
			delta.Version,
		)
	}
	digest := delta.Digest
	delta.Digest = ""
	payload, err := json.Marshal(delta)
	if err != nil {
		return SessionDelta{}, fmt.Errorf("encode session delta: %w", err)
	}
	sum := sha256.Sum256(payload)
	if digest != hex.EncodeToString(sum[:]) {
		return SessionDelta{}, errors.New("session delta digest mismatch")
	}
	delta.Digest = digest
	return delta, nil
}

func PrepareSessionRestore(
	delta SessionDelta,
	currentRevision uint64,
	appliedDigest string,
	bootstrap bool,
) (SessionRestore, error) {
	key := delta.ReplayKey()
	if appliedDigest != "" {
		if appliedDigest != delta.Digest {
			return SessionRestore{}, errors.New(
				"session delta replay digest conflict",
			)
		}
		return SessionRestore{
			Key: key, Digest: delta.Digest, Replay: true,
		}, nil
	}
	if bootstrap && currentRevision == 0 {
		currentRevision = delta.BaseRevision
	}
	if currentRevision != delta.BaseRevision {
		return SessionRestore{}, fmt.Errorf(
			"session delta revision conflict: have %d, base %d",
			currentRevision,
			delta.BaseRevision,
		)
	}
	if len(delta.MessageTurns) != 0 &&
		len(delta.MessageTurns) != len(delta.History) {
		return SessionRestore{}, errors.New(
			"session delta message turn count mismatch",
		)
	}
	history := CloneMessages(delta.History)
	for index, turn := range delta.MessageTurns {
		history[index].Turn = turn
	}
	historyTurns := CloneHistoryTurns(delta.HistoryTurns)
	ReconcileHistoryTurns(
		&historyTurns,
		history,
		delta.TurnID,
		delta.Turn,
	)
	window := CloneWindowLedger(delta.Window)
	if !window.Valid() {
		var err error
		window, err = CreateWindowLedger(1)
		if err != nil {
			return SessionRestore{}, fmt.Errorf(
				"restore token window: %w",
				err,
			)
		}
	}
	epoch := max(uint64(1), delta.Epoch)
	var plan *Plan
	if delta.Plan != nil && len(delta.Plan.Steps) != 0 {
		cloned := delta.Plan.Clone()
		plan = &cloned
	}
	accounting := delta.AccountingDelta()
	return SessionRestore{
		Key: key, Digest: delta.Digest,
		Revision: delta.BaseRevision + 1,
		History:  history,
		State: SessionState{
			Epoch: epoch, Turn: delta.Turn,
			HistoryTurns: historyTurns,
			WorkingSet:   delta.WorkingSet, Evidence: delta.Evidence,
			Failures: delta.Failures, Compaction: CloneCompaction(delta.Compaction),
			Plan: plan, World: CloneWorldBaseline(delta.World),
			Workspace: delta.Workspace, Window: window,
			Manifest: delta.ManifestLimits,
		},
		Accounting: accounting,
	}, nil
}

func PrepareSessionDelta(
	turnID string,
	baseRevision uint64,
	history []provider.Message,
	usage provider.Usage,
	cost float64,
	state ...SessionState,
) (SessionDelta, error) {
	if turnID == "" {
		return SessionDelta{}, errors.New("session delta turn id is required")
	}
	accounting, err := PrepareAccountingDelta(turnID, usage, cost)
	if err != nil {
		return SessionDelta{}, err
	}
	var sessionState SessionState
	if len(state) != 0 {
		sessionState = state[0]
	}
	delta := SessionDelta{
		TurnID: turnID, Version: ContextEnvelopeVersion,
		Epoch: sessionState.Epoch, BaseRevision: baseRevision,
		History:        CloneMessages(history),
		MessageTurns:   make([]uint64, len(history)),
		HistoryTurns:   CloneHistoryTurns(sessionState.HistoryTurns),
		Usage:          accounting.Usage,
		CostMicrounits: accounting.CostMicrounits,
		WorkingSet:     sessionState.WorkingSet, Evidence: sessionState.Evidence,
		Failures: sessionState.Failures, Compaction: sessionState.Compaction,
		Plan: sessionState.Plan, World: CloneWorldBaseline(sessionState.World),
		Workspace:      sessionState.Workspace,
		Window:         CloneWindowLedger(sessionState.Window),
		ManifestLimits: sessionState.Manifest, Turn: sessionState.Turn,
	}
	for index, message := range history {
		delta.Turn = max(delta.Turn, message.Turn)
		delta.MessageTurns[index] = message.Turn
	}
	if sessionState.Turn != 0 && HistoryContainsTurn(history, sessionState.Turn) {
		if delta.HistoryTurns == nil {
			delta.HistoryTurns = make(map[string]uint64)
		}
		delta.HistoryTurns[turnID] = sessionState.Turn
	}
	ReconcileHistoryTurnBindings(delta.HistoryTurns, delta.MessageTurns)
	payload, err := json.Marshal(delta)
	if err != nil {
		return SessionDelta{}, fmt.Errorf("encode session delta: %w", err)
	}
	sum := sha256.Sum256(payload)
	delta.Digest = hex.EncodeToString(sum[:])
	return delta, nil
}

func PrepareAccountingDelta(
	turnID string,
	usage provider.Usage,
	cost float64,
) (AccountingDelta, error) {
	if turnID == "" {
		return AccountingDelta{},
			errors.New("session delta turn id is required")
	}
	if cost < 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
		return AccountingDelta{},
			errors.New("session delta cost is invalid")
	}
	accounting := AccountingDelta{
		TurnID:         turnID,
		Usage:          usage,
		CostMicrounits: uint64(math.Round(cost * 1_000_000)),
	}
	accounting.Seal()
	return accounting, nil
}

func (d SessionDelta) MarshalJSON() ([]byte, error) {
	raw, err := json.Marshal(deltaJSON(d))
	if err != nil {
		return nil, err
	}
	return durablecodec.EncodeJSON(raw, d.BaseRevision)
}

func (d *SessionDelta) UnmarshalJSON(raw []byte) error {
	decoded, err := durablecodec.DecodeJSON(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(decoded, (*deltaJSON)(d))
}

func (d SessionDelta) ContextSnapshot() (ContextSnapshot, error) {
	epoch := d.Epoch
	if epoch == 0 {
		epoch = 1
	}
	snapshot := ContextSnapshot{
		Version: ContextSnapshotVersion,
		Epoch:   epoch, Revision: d.BaseRevision + 1, Turn: d.Turn,
		History: d.History, MessageTurns: d.MessageTurns,
		HistoryTurns: d.HistoryTurns,
		WorkingSet:   d.WorkingSet, Evidence: d.Evidence,
		Failures: d.Failures, Compaction: d.Compaction,
		Plan: d.Plan, World: d.World, Workspace: d.Workspace,
		Window: d.Window,
	}
	if err := snapshot.Seal(); err != nil {
		return ContextSnapshot{}, err
	}
	return snapshot, nil
}

func (d SessionDelta) AccountingDelta() AccountingDelta {
	value := AccountingDelta{
		TurnID: d.TurnID, Usage: d.Usage,
		CostMicrounits: d.CostMicrounits,
	}
	value.Seal()
	return value
}

func NewSessionDelta(
	snapshot ContextSnapshot,
	accounting AccountingDelta,
	limits ...ManifestLimits,
) (SessionDelta, error) {
	if err := snapshot.Validate(); err != nil {
		return SessionDelta{}, err
	}
	if err := accounting.Validate(); err != nil {
		return SessionDelta{}, err
	}
	if snapshot.Revision == 0 {
		return SessionDelta{}, errors.New("context snapshot revision is required")
	}
	delta := SessionDelta{
		Version: ContextEnvelopeVersion, Epoch: snapshot.Epoch,
		TurnID: accounting.TurnID, Turn: snapshot.Turn,
		BaseRevision: snapshot.Revision - 1,
		History:      CloneMessages(snapshot.History),
		MessageTurns: append([]uint64(nil), snapshot.MessageTurns...),
		HistoryTurns: cloneHistoryTurns(snapshot.HistoryTurns),
		Usage:        accounting.Usage, CostMicrounits: accounting.CostMicrounits,
		WorkingSet: snapshot.WorkingSet, Evidence: snapshot.Evidence,
		Failures: snapshot.Failures, Compaction: snapshot.Compaction,
		Plan: snapshot.Plan, World: CloneWorldBaseline(snapshot.World),
		Workspace: snapshot.Workspace,
		Window:    CloneWindowLedger(snapshot.Window),
	}
	if len(limits) != 0 {
		delta.ManifestLimits = limits[0]
	}
	payload, err := json.Marshal(delta)
	if err != nil {
		return SessionDelta{}, fmt.Errorf("encode session delta: %w", err)
	}
	sum := sha256.Sum256(payload)
	delta.Digest = hex.EncodeToString(sum[:])
	return delta, nil
}

func ExpandContextEnvelope(
	ctx context.Context,
	store BlobStore,
	raw []byte,
) ([]byte, error) {
	envelope, err := DecodeContextEnvelope(raw)
	if err != nil {
		return nil, err
	}
	snapshot, err := LoadContextManifest(ctx, store, envelope.Manifest)
	if err != nil {
		return nil, err
	}
	delta, err := NewSessionDelta(snapshot, envelope.Accounting)
	if err != nil {
		return nil, err
	}
	return json.Marshal(delta)
}
