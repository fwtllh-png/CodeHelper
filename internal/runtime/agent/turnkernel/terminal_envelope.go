package turnkernel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

var (
	ErrTerminalEnvelopeConflict = errors.New("terminal envelope conflict")
	ErrTerminalEnvelopeMissing  = errors.New("terminal envelope missing")
)

type DomainFact struct {
	TurnID      string `json:"turn_id"`
	Sequence    uint64 `json:"sequence"`
	Command     string `json:"command"`
	Event       Event  `json:"event"`
	State       State  `json:"state"`
	StateDigest string `json:"state_digest"`
}

type OperationCommitFact struct {
	OperationID protocol.OperationID `json:"operation_id"`
	Status      string               `json:"status"`
	Receipt     json.RawMessage      `json:"receipt,omitempty"`
}

type ProjectionOutboxEntry struct {
	ID          string               `json:"id"`
	EventID     protocol.EventID     `json:"event_id,omitempty"`
	OperationID protocol.OperationID `json:"operation_id,omitempty"`
	ThreadID    protocol.ThreadID    `json:"thread_id,omitempty"`
	TurnID      protocol.TurnID      `json:"turn_id,omitempty"`
	ItemID      protocol.ItemID      `json:"item_id,omitempty"`
	Kind        string               `json:"kind"`
	Payload     json.RawMessage      `json:"payload"`
}

type PendingTerminalProjection struct {
	Envelope TerminalEnvelope        `json:"envelope"`
	Entries  []ProjectionOutboxEntry `json:"entries"`
}

type TerminalEnvelope struct {
	TurnID          string                         `json:"turn_id"`
	EffectID        string                         `json:"effect_id"`
	FrozenState     State                          `json:"frozen_state"`
	DomainFacts     []DomainFact                   `json:"domain_facts"`
	Measurement     TerminalMeasurementSnapshot    `json:"measurement"`
	Receipt         *protocol.ExecutionReceiptData `json:"receipt"`
	SessionDelta    json.RawMessage                `json:"session_delta,omitempty"`
	FinalOutput     []string                       `json:"final_output,omitempty"`
	TerminalEvent   Event                          `json:"terminal_event"`
	OperationCommit OperationCommitFact            `json:"operation_commit"`
	Outbox          []ProjectionOutboxEntry        `json:"outbox"`
}

type TerminalCommitMarker struct {
	TurnID      string    `json:"turn_id"`
	EffectID    string    `json:"effect_id"`
	Digest      string    `json:"digest"`
	CommittedAt time.Time `json:"committed_at"`
}

type TerminalEnvelopeStage string

const (
	StageDomainFacts     TerminalEnvelopeStage = "domain_facts"
	StageMeasurement     TerminalEnvelopeStage = "measurement"
	StageReceipt         TerminalEnvelopeStage = "receipt"
	StageSessionDelta    TerminalEnvelopeStage = "session_delta"
	StageFinalOutput     TerminalEnvelopeStage = "final_output"
	StageTerminalEvent   TerminalEnvelopeStage = "terminal_event"
	StageOperationCommit TerminalEnvelopeStage = "operation_commit"
	StageOutbox          TerminalEnvelopeStage = "outbox"
	StageCommitMarker    TerminalEnvelopeStage = "commit_marker"
)

type TerminalFaultInjector func(TerminalEnvelopeStage) error

type TerminalEnvelopeStore interface {
	AppendDomainFacts(context.Context, string, uint64, []DomainFact) error
	LoadDomainFacts(context.Context, string) ([]DomainFact, error)
	CommitTerminal(context.Context, TerminalEnvelope) (TerminalCommitMarker, error)
	LoadTerminal(context.Context, string) (TerminalEnvelope, TerminalCommitMarker, error)
	PendingOutbox(context.Context, string) ([]ProjectionOutboxEntry, error)
	MarkOutboxPublished(context.Context, string, []string) error
}

// AtomicTerminalOperationStore commits the Terminal Envelope and the real
// Runtime Operation in one storage transaction.
type AtomicTerminalOperationStore interface {
	CommitTerminalOperation(
		context.Context,
		TerminalEnvelope,
	) (TerminalCommitMarker, error)
}

type TerminalProjectionRecoveryStore interface {
	PendingTerminalProjections(
		context.Context,
	) ([]PendingTerminalProjection, error)
}

type SessionDeltaRecoveryStore interface {
	LatestSessionDelta(context.Context, protocol.ThreadID) (json.RawMessage, error)
}

// MemoryTerminalEnvelopeStore is the reference implementation for the atomic
// contract. Production persistence can implement the same interface with one
// database transaction; this implementation is also the fault-injection oracle.
type MemoryTerminalEnvelopeStore struct {
	mu        sync.Mutex
	now       func() time.Time
	inject    TerminalFaultInjector
	facts     map[string][]DomainFact
	envelopes map[string]TerminalEnvelope
	markers   map[string]TerminalCommitMarker
	published map[string]map[string]bool
}

func NewMemoryTerminalEnvelopeStore(
	now func() time.Time,
	inject TerminalFaultInjector,
) *MemoryTerminalEnvelopeStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryTerminalEnvelopeStore{
		now:       now,
		inject:    inject,
		facts:     make(map[string][]DomainFact),
		envelopes: make(map[string]TerminalEnvelope),
		markers:   make(map[string]TerminalCommitMarker),
		published: make(map[string]map[string]bool),
	}
}

func (s *MemoryTerminalEnvelopeStore) AppendDomainFacts(
	_ context.Context,
	turnID string,
	expectedNext uint64,
	facts []DomainFact,
) error {
	if strings.TrimSpace(turnID) == "" || len(facts) == 0 {
		return errors.New("domain fact append is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, terminal := s.markers[turnID]; terminal {
		return errors.New("terminal turn rejects new domain facts")
	}
	current := s.facts[turnID]
	if expectedNext != uint64(len(current)+1) {
		return fmt.Errorf(
			"domain fact sequence conflict: got %d want %d",
			expectedNext,
			len(current)+1,
		)
	}
	next := append(cloneDomainFacts(current), cloneDomainFacts(facts)...)
	for index, fact := range next {
		if fact.TurnID != turnID ||
			fact.Sequence != uint64(index+1) ||
			fact.Command == "" ||
			fact.StateDigest == "" {
			return fmt.Errorf("invalid domain fact at index %d", index)
		}
		if err := Validate(fact.State); err != nil {
			return fmt.Errorf("invalid domain fact state at index %d: %w", index, err)
		}
		digest, err := Digest(fact.State)
		if err != nil || digest != fact.StateDigest {
			return fmt.Errorf("domain fact digest mismatch at index %d", index)
		}
	}
	s.facts[turnID] = next
	return nil
}

func (s *MemoryTerminalEnvelopeStore) LoadDomainFacts(
	_ context.Context,
	turnID string,
) ([]DomainFact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneDomainFacts(s.facts[turnID]), nil
}

func (s *MemoryTerminalEnvelopeStore) CommitTerminal(
	_ context.Context,
	envelope TerminalEnvelope,
) (TerminalCommitMarker, error) {
	digest, err := validateTerminalEnvelope(envelope)
	if err != nil {
		return TerminalCommitMarker{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if marker, ok := s.markers[envelope.TurnID]; ok {
		if marker.Digest != digest || marker.EffectID != envelope.EffectID {
			return TerminalCommitMarker{}, ErrTerminalEnvelopeConflict
		}
		return marker, nil
	}
	existingFacts := s.facts[envelope.TurnID]
	if len(envelope.DomainFacts) < len(existingFacts) {
		return TerminalCommitMarker{}, ErrTerminalEnvelopeConflict
	}
	for index := range existingFacts {
		left, _ := json.Marshal(existingFacts[index])
		right, _ := json.Marshal(envelope.DomainFacts[index])
		if !slices.Equal(left, right) {
			return TerminalCommitMarker{}, ErrTerminalEnvelopeConflict
		}
	}
	for _, stage := range []TerminalEnvelopeStage{
		StageDomainFacts,
		StageMeasurement,
		StageReceipt,
		StageSessionDelta,
		StageFinalOutput,
		StageTerminalEvent,
		StageOperationCommit,
		StageOutbox,
		StageCommitMarker,
	} {
		if s.inject != nil {
			if err := s.inject(stage); err != nil {
				return TerminalCommitMarker{}, fmt.Errorf(
					"terminal envelope %s: %w",
					stage,
					err,
				)
			}
		}
	}
	marker := TerminalCommitMarker{
		TurnID: envelope.TurnID, EffectID: envelope.EffectID,
		Digest: digest, CommittedAt: s.now().UTC(),
	}
	s.envelopes[envelope.TurnID] = cloneTerminalEnvelope(envelope)
	s.facts[envelope.TurnID] = cloneDomainFacts(envelope.DomainFacts)
	s.markers[envelope.TurnID] = marker
	s.published[envelope.TurnID] = make(map[string]bool)
	return marker, nil
}

func (s *MemoryTerminalEnvelopeStore) LoadTerminal(
	_ context.Context,
	turnID string,
) (TerminalEnvelope, TerminalCommitMarker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	envelope, ok := s.envelopes[turnID]
	if !ok {
		return TerminalEnvelope{}, TerminalCommitMarker{}, ErrTerminalEnvelopeMissing
	}
	return cloneTerminalEnvelope(envelope), s.markers[turnID], nil
}

func (s *MemoryTerminalEnvelopeStore) PendingOutbox(
	_ context.Context,
	turnID string,
) ([]ProjectionOutboxEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	envelope, ok := s.envelopes[turnID]
	if !ok {
		return nil, ErrTerminalEnvelopeMissing
	}
	pending := make([]ProjectionOutboxEntry, 0, len(envelope.Outbox))
	for _, entry := range envelope.Outbox {
		if !s.published[turnID][entry.ID] {
			pending = append(pending, cloneOutboxEntry(entry))
		}
	}
	return pending, nil
}

func (s *MemoryTerminalEnvelopeStore) PendingTerminalProjections(
	_ context.Context,
) ([]PendingTerminalProjection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turnIDs := make([]string, 0, len(s.envelopes))
	for turnID := range s.envelopes {
		turnIDs = append(turnIDs, turnID)
	}
	slices.Sort(turnIDs)
	var projections []PendingTerminalProjection
	for _, turnID := range turnIDs {
		envelope := s.envelopes[turnID]
		var pending []ProjectionOutboxEntry
		for _, entry := range envelope.Outbox {
			if !s.published[turnID][entry.ID] {
				pending = append(pending, cloneOutboxEntry(entry))
			}
		}
		if len(pending) != 0 {
			projections = append(projections, PendingTerminalProjection{
				Envelope: cloneTerminalEnvelope(envelope),
				Entries:  pending,
			})
		}
	}
	return projections, nil
}

func (s *MemoryTerminalEnvelopeStore) LatestSessionDelta(
	_ context.Context,
	threadID protocol.ThreadID,
) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var latest TerminalEnvelope
	var latestRevision uint64
	for _, envelope := range s.envelopes {
		if len(envelope.SessionDelta) == 0 {
			continue
		}
		matches := false
		for _, entry := range envelope.Outbox {
			if entry.ThreadID == threadID {
				matches = true
				break
			}
		}
		var header struct {
			BaseRevision uint64 `json:"base_revision"`
		}
		if matches && json.Unmarshal(envelope.SessionDelta, &header) == nil &&
			(latest.TurnID == "" || header.BaseRevision > latestRevision) {
			latest = envelope
			latestRevision = header.BaseRevision
		}
	}
	return append(json.RawMessage(nil), latest.SessionDelta...), nil
}

func (s *MemoryTerminalEnvelopeStore) MarkOutboxPublished(
	_ context.Context,
	turnID string,
	entryIDs []string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	envelope, ok := s.envelopes[turnID]
	if !ok {
		return ErrTerminalEnvelopeMissing
	}
	known := make(map[string]bool, len(envelope.Outbox))
	for _, entry := range envelope.Outbox {
		known[entry.ID] = true
	}
	for _, entryID := range entryIDs {
		if !known[entryID] {
			return fmt.Errorf("unknown outbox entry %q", entryID)
		}
	}
	for _, entryID := range entryIDs {
		s.published[turnID][entryID] = true
	}
	return nil
}

func validateTerminalEnvelope(envelope TerminalEnvelope) (string, error) {
	if strings.TrimSpace(envelope.TurnID) == "" ||
		strings.TrimSpace(envelope.EffectID) == "" {
		return "", errors.New("terminal envelope identity is incomplete")
	}
	if err := Validate(envelope.FrozenState); err != nil {
		return "", fmt.Errorf("terminal envelope state: %w", err)
	}
	if !envelope.FrozenState.Phase.Terminal() ||
		envelope.FrozenState.Terminal == nil {
		return "", errors.New("terminal envelope state is not terminal")
	}
	if envelope.Receipt == nil {
		return "", errors.New("terminal envelope receipt is missing")
	}
	if err := ValidateTerminalMeasurement(envelope.Measurement); err != nil {
		return "", fmt.Errorf("terminal envelope measurement: %w", err)
	}
	if envelope.Receipt.MeasurementDigest != envelope.Measurement.Digest ||
		envelope.Receipt.UsageDigest != envelope.Measurement.UsageDigest ||
		envelope.Receipt.MeasurementRecorded != envelope.Measurement.Recorded() {
		return "", errors.New(
			"terminal envelope receipt disagrees with measurement",
		)
	}
	if envelope.Measurement.UsageRecorded {
		usage := envelope.Measurement.Usage
		receipt := envelope.Receipt
		if receipt.InputTokens != usage.InputTokens ||
			receipt.OutputTokens != usage.OutputTokens ||
			receipt.ReasoningTokens != usage.ReasoningTokens ||
			receipt.CachedTokens != usage.CachedTokens ||
			receipt.CostMicrounits != usage.CostMicrounits ||
			receipt.CostKnown != usage.CostKnown {
			return "", errors.New(
				"terminal envelope receipt usage disagrees with measurement",
			)
		}
	}
	if len(envelope.SessionDelta) != 0 && !json.Valid(envelope.SessionDelta) {
		return "", errors.New("terminal envelope session delta is invalid")
	}
	if envelope.OperationCommit.OperationID == "" ||
		envelope.OperationCommit.Status != "committed" {
		return "", errors.New("terminal envelope operation commit is invalid")
	}
	if envelope.TerminalEvent.Kind != EventTerminalCommitted ||
		envelope.TerminalEvent.Terminal == nil ||
		*envelope.TerminalEvent.Terminal != *envelope.FrozenState.Terminal {
		return "", errors.New("terminal envelope event disagrees with frozen state")
	}
	if !slices.Equal(envelope.FinalOutput, envelope.FrozenState.FinalOutput) {
		return "", errors.New("terminal envelope output disagrees with frozen state")
	}
	if len(envelope.DomainFacts) == 0 {
		return "", errors.New("terminal envelope has no domain facts")
	}
	for index, fact := range envelope.DomainFacts {
		if fact.TurnID != envelope.TurnID ||
			fact.Sequence != uint64(index+1) ||
			fact.Command == "" ||
			fact.StateDigest == "" {
			return "", fmt.Errorf("invalid domain fact at index %d", index)
		}
	}
	seenOutbox := make(map[string]bool, len(envelope.Outbox))
	for _, entry := range envelope.Outbox {
		if entry.ID == "" || entry.Kind == "" || len(entry.Payload) == 0 ||
			entry.EventID == "" ||
			entry.OperationID == "" ||
			entry.ThreadID == "" ||
			entry.TurnID != protocol.TurnID(envelope.TurnID) ||
			entry.ItemID == "" ||
			seenOutbox[entry.ID] {
			return "", errors.New("terminal envelope outbox is invalid")
		}
		seenOutbox[entry.ID] = true
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func ValidateTerminalEnvelope(
	envelope TerminalEnvelope,
) (string, error) {
	return validateTerminalEnvelope(envelope)
}

func cloneTerminalEnvelope(envelope TerminalEnvelope) TerminalEnvelope {
	encoded, _ := json.Marshal(envelope)
	var cloned TerminalEnvelope
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}

func cloneOutboxEntry(entry ProjectionOutboxEntry) ProjectionOutboxEntry {
	entry.Payload = append(json.RawMessage(nil), entry.Payload...)
	return entry
}

func cloneDomainFacts(facts []DomainFact) []DomainFact {
	cloned := make([]DomainFact, len(facts))
	copy(cloned, facts)
	return cloned
}
