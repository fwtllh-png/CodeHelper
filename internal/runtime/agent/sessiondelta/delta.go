// Package sessiondelta defines the durable Agent session snapshot contract.
package sessiondelta

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/compact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextstore"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/evidence"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/durablecodec"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type Compaction struct {
	Count int              `json:"count"`
	State *CompactionState `json:"state,omitempty"`
}

type CompactionState struct {
	ID                  string                          `json:"id"`
	ThreadID            protocol.ThreadID               `json:"thread_id"`
	TurnID              protocol.TurnID                 `json:"turn_id"`
	Phase               string                          `json:"phase"`
	PlanDigest          string                          `json:"plan_digest"`
	Plan                *compact.CompactionPlan         `json:"plan,omitempty"`
	NarrativeInput      *compact.NarrativeInputArtifact `json:"narrative_input,omitempty"`
	Narrative           *compact.NarrativeArtifact      `json:"narrative,omitempty"`
	Truth               compact.TruthCapsule            `json:"truth"`
	SourceWindowID      string                          `json:"source_window_id"`
	TargetWindowID      string                          `json:"target_window_id"`
	SourceContextDigest string                          `json:"source_context_digest"`
	Attempt             uint32                          `json:"attempt,omitempty"`
	FallbackReason      string                          `json:"fallback_reason,omitempty"`
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
		[]compact.TruthEntity(nil),
		value.State.Truth.Entities...,
	)
	state.Truth.Omissions = append(
		[]compact.Omission(nil),
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
		var plan compact.CompactionPlan
		_ = json.Unmarshal(raw, &plan)
		state.Plan = &plan
	}
	if value.State.NarrativeInput != nil {
		input := *value.State.NarrativeInput
		input.Excerpts = append(
			[]compact.NarrativeExcerpt(nil),
			value.State.NarrativeInput.Excerpts...,
		)
		state.NarrativeInput = &input
	}
	if value.State.Narrative != nil {
		narrative := *value.State.Narrative
		narrative.Body.Items = append(
			[]compact.NarrativeItem(nil),
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

type State struct {
	Epoch        uint64                     `json:"epoch,omitempty"`
	Turn         uint64                     `json:"turn,omitempty"`
	HistoryTurns map[string]uint64          `json:"history_turns,omitempty"`
	WorkingSet   workingset.Delta           `json:"working_set"`
	Evidence     evidence.Delta             `json:"evidence"`
	Failures     compact.FailureDelta       `json:"failures"`
	Compaction   Compaction                 `json:"compaction"`
	Plan         *interact.Plan             `json:"plan,omitempty"`
	World        contextstore.WorldBaseline `json:"world,omitempty"`
	Workspace    WorkspaceBinding           `json:"workspace,omitempty"`
	Window       contextstore.WindowLedger  `json:"window"`
	Manifest     ManifestLimits             `json:"manifest_limits,omitempty"`
}

type Delta struct {
	Version        int                        `json:"version,omitempty"`
	Epoch          uint64                     `json:"epoch,omitempty"`
	TurnID         string                     `json:"turn_id"`
	Turn           uint64                     `json:"turn,omitempty"`
	BaseRevision   uint64                     `json:"base_revision"`
	History        []provider.Message         `json:"history"`
	MessageTurns   []uint64                   `json:"message_turns,omitempty"`
	HistoryTurns   map[string]uint64          `json:"history_turns,omitempty"`
	Usage          provider.Usage             `json:"usage"`
	CostMicrounits uint64                     `json:"cost_microunits"`
	WorkingSet     workingset.Delta           `json:"working_set"`
	Evidence       evidence.Delta             `json:"evidence"`
	Failures       compact.FailureDelta       `json:"failures"`
	Compaction     Compaction                 `json:"compaction"`
	Plan           *interact.Plan             `json:"plan,omitempty"`
	World          contextstore.WorldBaseline `json:"world,omitempty"`
	Workspace      WorkspaceBinding           `json:"workspace,omitempty"`
	Window         contextstore.WindowLedger  `json:"window"`
	ManifestLimits ManifestLimits             `json:"manifest_limits,omitempty"`
	Digest         string                     `json:"digest"`
}

type deltaJSON Delta

func (d Delta) MarshalJSON() ([]byte, error) {
	raw, err := json.Marshal(deltaJSON(d))
	if err != nil {
		return nil, err
	}
	return durablecodec.EncodeJSON(raw, d.BaseRevision)
}

func (d *Delta) UnmarshalJSON(raw []byte) error {
	decoded, err := durablecodec.DecodeJSON(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(decoded, (*deltaJSON)(d))
}

func (d Delta) ContextSnapshot() (ContextSnapshot, error) {
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

func (d Delta) AccountingDelta() AccountingDelta {
	value := AccountingDelta{
		TurnID: d.TurnID, Usage: d.Usage,
		CostMicrounits: d.CostMicrounits,
	}
	value.Seal()
	return value
}

func NewDelta(
	snapshot ContextSnapshot,
	accounting AccountingDelta,
) (Delta, error) {
	if err := snapshot.Validate(); err != nil {
		return Delta{}, err
	}
	if err := accounting.Validate(); err != nil {
		return Delta{}, err
	}
	if snapshot.Revision == 0 {
		return Delta{}, errors.New("context snapshot revision is required")
	}
	delta := Delta{
		Version: ContextEnvelopeVersion, Epoch: snapshot.Epoch,
		TurnID: accounting.TurnID, Turn: snapshot.Turn,
		BaseRevision: snapshot.Revision - 1,
		History:      contextstore.CloneMessages(snapshot.History),
		MessageTurns: append([]uint64(nil), snapshot.MessageTurns...),
		HistoryTurns: cloneHistoryTurns(snapshot.HistoryTurns),
		Usage:        accounting.Usage, CostMicrounits: accounting.CostMicrounits,
		WorkingSet: snapshot.WorkingSet, Evidence: snapshot.Evidence,
		Failures: snapshot.Failures, Compaction: snapshot.Compaction,
		Plan: snapshot.Plan, World: contextstore.CloneWorldBaseline(snapshot.World),
		Workspace: snapshot.Workspace,
		Window:    contextstore.CloneWindowLedger(snapshot.Window),
	}
	payload, err := json.Marshal(delta)
	if err != nil {
		return Delta{}, fmt.Errorf("encode session delta: %w", err)
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
	delta, err := NewDelta(snapshot, envelope.Accounting)
	if err != nil {
		return nil, err
	}
	return json.Marshal(delta)
}
