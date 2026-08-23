package eventhub

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type TerminalContentStore interface {
	agentcontext.BlobStore
	Release(context.Context, string) error
}

type TerminalRuntime interface {
	TerminalContent() TerminalContentStore
	TerminalOperationReceipt(protocol.OperationID) any
	TerminalProjectionIdentity(
		protocol.TurnID,
		protocol.OperationID,
		protocol.ItemID,
	) (protocol.OperationID, protocol.ItemID)
	TerminalStore() turnkernel.TerminalEnvelopeStore
	DurableTerminal() bool
	LoadContextManifest(
		protocol.ThreadID,
	) (agentcontext.ContextManifest, bool)
	StoreContextManifest(protocol.ThreadID, agentcontext.ContextManifest)
	ObserveTerminal(
		trace.TerminalPhase,
		protocol.ThreadID,
		protocol.TurnID,
		protocol.OperationID,
		string,
		string,
		string,
		trace.TerminalOutcome,
	) string
	PublishTerminalProjection(
		context.Context,
		turnkernel.ProjectionOutboxEntry,
		protocol.EventData,
	) error
}

type TerminalMaterial struct {
	FrozenState  turnkernel.State
	DomainFacts  []turnkernel.DomainFact
	Measurement  turnkernel.TerminalMeasurementSnapshot
	Receipt      *protocol.ExecutionReceiptData
	Terminal     protocol.EventData
	SessionDelta json.RawMessage
}

type TerminalRequest struct {
	Operation protocol.Operation
	Material  TerminalMaterial
}
type CommittedTerminal struct {
	Operation          protocol.Operation
	OperationCommitted bool
	OperationID        protocol.OperationID
	ItemID             protocol.ItemID
}
type TerminalPublisher struct{ runtime TerminalRuntime }

func NewTerminalPublisher(runtime TerminalRuntime) *TerminalPublisher {
	return &TerminalPublisher{runtime: runtime}
}

func (p *TerminalPublisher) Commit(ctx context.Context, request TerminalRequest) (CommittedTerminal, error) {
	material := request.Material
	if !material.FrozenState.Phase.Terminal() ||
		material.FrozenState.Terminal == nil ||
		material.Receipt == nil ||
		material.Terminal == nil ||
		len(material.DomainFacts) == 0 {
		return CommittedTerminal{}, errors.New("terminal material is incomplete")
	}
	threadID, turnID, itemID := protocol.OperationReferences(request.Operation)
	if string(turnID) != material.DomainFacts[0].TurnID {
		return CommittedTerminal{}, errors.New("terminal material turn identity mismatch")
	}
	sessionDelta, manifest, staged, err := p.prepareContextManifest(
		ctx,
		threadID,
		turnID,
		material.SessionDelta,
	)
	if err != nil {
		return CommittedTerminal{}, err
	}
	releaseStaged := func() {
		for _, ref := range staged {
			_ = p.runtime.TerminalContent().Release(context.Background(), ref.Handle)
		}
	}
	receiptPayload, err := json.Marshal(material.Receipt)
	if err != nil {
		releaseStaged()
		return CommittedTerminal{}, err
	}
	terminalPayload, err := json.Marshal(material.Terminal)
	if err != nil {
		releaseStaged()
		return CommittedTerminal{}, err
	}
	operationReceipt, err := json.Marshal(
		p.runtime.TerminalOperationReceipt(request.Operation.ID),
	)
	if err != nil {
		releaseStaged()
		return CommittedTerminal{}, err
	}
	projectionOperationID := request.Operation.ID
	projectionOperationID, itemID = p.runtime.TerminalProjectionIdentity(
		turnID,
		projectionOperationID,
		itemID,
	)
	entry := func(id string, kind protocol.EventKind, payload json.RawMessage) turnkernel.ProjectionOutboxEntry {
		return turnkernel.ProjectionOutboxEntry{
			ID: id, EventID: TerminalOutboxEventID(turnID, id),
			OperationID: projectionOperationID,
			ThreadID:    threadID, TurnID: turnID, ItemID: itemID,
			Kind: string(kind), Payload: payload,
		}
	}
	outbox := make([]turnkernel.ProjectionOutboxEntry, 0, len(material.FrozenState.FinalOutput)+2)
	for index, text := range material.FrozenState.FinalOutput {
		payload, marshalErr := json.Marshal(&protocol.OutputDeltaData{Text: text})
		if marshalErr != nil {
			return CommittedTerminal{}, marshalErr
		}
		outbox = append(outbox, entry(fmt.Sprintf("output:%06d", index+1),
			protocol.EventOutputDelta, payload))
	}
	outbox = append(outbox,
		entry("receipt", protocol.EventExecutionReceipt, receiptPayload),
		entry("terminal", EventKind(material.Terminal), terminalPayload),
	)
	decision := *material.FrozenState.Terminal
	envelope := turnkernel.TerminalEnvelope{
		TurnID: string(turnID), EffectID: "terminal:" + string(turnID),
		FrozenState: material.FrozenState, DomainFacts: material.DomainFacts,
		Measurement:  material.Measurement,
		Receipt:      material.Receipt,
		SessionDelta: append(json.RawMessage(nil), sessionDelta...),
		FinalOutput:  append([]string(nil), material.FrozenState.FinalOutput...),
		TerminalEvent: turnkernel.Event{
			Kind: turnkernel.EventTerminalCommitted, Terminal: &decision,
		},
		OperationCommit: turnkernel.OperationCommitFact{
			OperationID: request.Operation.ID,
			Status:      "committed", Receipt: operationReceipt,
		},
		Outbox: outbox,
	}
	committed := CommittedTerminal{
		Operation: request.Operation, OperationID: projectionOperationID, ItemID: itemID,
	}
	observationOutcome := TerminalObservationOutcome(decision)
	preparedObservation := p.runtime.ObserveTerminal(
		trace.TerminalPrepared,
		threadID,
		turnID,
		request.Operation.ID,
		envelope.EffectID,
		"",
		envelope.Measurement.Digest,
		observationOutcome,
	)
	if p.runtime.DurableTerminal() {
		atomicStore, ok := p.runtime.TerminalStore().(turnkernel.AtomicTerminalOperationStore)
		if !ok {
			return CommittedTerminal{}, errors.New(
				"durable lifecycle requires atomic terminal operation store",
			)
		}
		_, err = atomicStore.CommitTerminalOperation(ctx, envelope)
		committed.OperationCommitted = err == nil
	} else {
		_, err = p.runtime.TerminalStore().CommitTerminal(ctx, envelope)
		committed.OperationCommitted = err == nil
	}
	if err == nil {
		if manifest != nil {
			p.runtime.StoreContextManifest(threadID, *manifest)
		}
		p.runtime.ObserveTerminal(
			trace.TerminalCommitted,
			threadID,
			turnID,
			request.Operation.ID,
			envelope.EffectID,
			preparedObservation,
			envelope.Measurement.Digest,
			observationOutcome,
		)
	}
	if err != nil {
		releaseStaged()
	}
	return committed, err
}

func (p *TerminalPublisher) prepareContextManifest(
	ctx context.Context,
	threadID protocol.ThreadID,
	turnID protocol.TurnID,
	raw json.RawMessage,
) (
	json.RawMessage,
	*agentcontext.ContextManifest,
	[]agentcontext.ContentRef,
	error,
) {
	if len(raw) == 0 {
		return nil, nil, nil, nil
	}
	var delta agentcontext.SessionDelta
	if err := json.Unmarshal(raw, &delta); err != nil {
		return nil, nil, nil, fmt.Errorf("decode prepared session delta: %w", err)
	}
	if delta.Version != agentcontext.ContextEnvelopeVersion {
		return nil, nil, nil, fmt.Errorf(
			"unsupported session delta version %d",
			delta.Version,
		)
	}
	snapshot, err := delta.ContextSnapshot()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("build context snapshot: %w", err)
	}
	previous, hasPrevious := p.runtime.LoadContextManifest(threadID)
	var prior *agentcontext.ContextManifest
	if hasPrevious {
		prior = &previous
	}
	manifest, err := agentcontext.BuildContextManifest(
		ctx,
		p.runtime.TerminalContent(),
		threadID,
		turnID,
		snapshot,
		prior,
		delta.ManifestLimits,
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("stage context manifest: %w", err)
	}
	encoded, err := agentcontext.EncodeContextEnvelope(
		manifest,
		delta.AccountingDelta(),
	)
	if err != nil {
		return nil, nil, nil, err
	}
	staged := newManifestRefs(prior, manifest)
	return encoded, &manifest, staged, nil
}

func newManifestRefs(
	previous *agentcontext.ContextManifest,
	current agentcontext.ContextManifest,
) []agentcontext.ContentRef {
	existing := make(map[string]struct{})
	if previous != nil {
		for _, ref := range contextManifestRefs(*previous) {
			existing[ref.Handle] = struct{}{}
		}
	}
	var result []agentcontext.ContentRef
	for _, ref := range contextManifestRefs(current) {
		if _, reused := existing[ref.Handle]; !reused {
			result = append(result, ref)
		}
	}
	return result
}

func contextManifestRefs(
	manifest agentcontext.ContextManifest,
) []agentcontext.ContentRef {
	result := []agentcontext.ContentRef{manifest.History.BaseRef}
	result = append(result, manifest.History.TailRefs...)
	for _, owner := range []agentcontext.OwnerManifest{
		manifest.Working,
		manifest.Evidence,
		manifest.Failures,
		manifest.Plan,
	} {
		result = append(result, owner.BaseRef)
		result = append(result, owner.DeltaRefs...)
	}
	return result
}
func (p *TerminalPublisher) Publish(ctx context.Context, committed CommittedTerminal) error {
	_, turnID, _ := protocol.OperationReferences(committed.Operation)
	entries, err := p.runtime.TerminalStore().PendingOutbox(ctx, string(turnID))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.OperationID != committed.OperationID || entry.ItemID != committed.ItemID {
			return errors.New("terminal outbox projection identity mismatch")
		}
		if err := p.publishEntry(ctx, string(turnID), entry); err != nil {
			return err
		}
	}
	return nil
}
func (p *TerminalPublisher) Recover(ctx context.Context) error {
	store, ok := p.runtime.TerminalStore().(turnkernel.TerminalProjectionRecoveryStore)
	if !ok {
		if p.runtime.DurableTerminal() {
			return errors.New(
				"durable lifecycle requires terminal projection recovery store",
			)
		}
		return nil
	}
	projections, err := store.PendingTerminalProjections(ctx)
	if err != nil {
		return err
	}
	for _, projection := range projections {
		for _, entry := range projection.Entries {
			if err := p.publishEntry(ctx, projection.Envelope.TurnID, entry); err != nil {
				return fmt.Errorf(
					"project terminal outbox %s/%s: %w",
					projection.Envelope.TurnID,
					entry.ID,
					err,
				)
			}
		}
	}
	return nil
}

func (p *TerminalPublisher) publishEntry(
	ctx context.Context,
	turnID string,
	entry turnkernel.ProjectionOutboxEntry,
) error {
	data, err := DecodeTerminalOutboxEntry(entry)
	if err != nil {
		return err
	}
	if entry.EventID == "" || entry.OperationID == "" || entry.ThreadID == "" ||
		entry.TurnID == "" || entry.ItemID == "" {
		return errors.New("terminal outbox event identity is incomplete")
	}
	err = p.runtime.PublishTerminalProjection(ctx, entry, data)
	if err != nil {
		return err
	}
	return p.runtime.TerminalStore().MarkOutboxPublished(ctx, turnID, []string{entry.ID})
}
func TerminalOutboxEventID(turnID protocol.TurnID, entryID string) protocol.EventID {
	sum := sha256.Sum256([]byte("terminal-outbox\x00" + string(turnID) + "\x00" + entryID))
	return protocol.EventID(fmt.Sprintf("evt_%x", sum[:16]))
}
