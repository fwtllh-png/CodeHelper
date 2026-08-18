package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

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
type TerminalPublisher struct{ runtime *Runtime }

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
	receiptPayload, err := json.Marshal(material.Receipt)
	if err != nil {
		return CommittedTerminal{}, err
	}
	terminalPayload, err := json.Marshal(material.Terminal)
	if err != nil {
		return CommittedTerminal{}, err
	}
	operationReceipt, err := json.Marshal(
		p.runtime.operationCommitReceipt(request.Operation.ID),
	)
	if err != nil {
		return CommittedTerminal{}, err
	}
	projectionOperationID := request.Operation.ID
	if stored, ok := p.runtime.active.LookupTurn(turnID); ok {
		if stored.OperationID != "" {
			projectionOperationID = stored.OperationID
		}
		if stored.ItemID != "" {
			itemID = stored.ItemID
		}
	}
	entry := func(id string, kind protocol.EventKind, payload json.RawMessage) turnkernel.ProjectionOutboxEntry {
		return turnkernel.ProjectionOutboxEntry{
			ID: id, EventID: terminalOutboxEventID(turnID, id),
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
		entry("terminal", eventKind(material.Terminal), terminalPayload),
	)
	decision := *material.FrozenState.Terminal
	envelope := turnkernel.TerminalEnvelope{
		TurnID: string(turnID), EffectID: "terminal:" + string(turnID),
		FrozenState: material.FrozenState, DomainFacts: material.DomainFacts,
		Measurement:  material.Measurement,
		Receipt:      material.Receipt,
		SessionDelta: append(json.RawMessage(nil), material.SessionDelta...),
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
	observationOutcome := terminalObservationOutcome(decision)
	preparedObservation := p.runtime.opts.Observability.Runtime.ObserveTerminal(
		context.Background(),
		trace.TerminalPrepared,
		threadID,
		turnID,
		request.Operation.ID,
		envelope.EffectID,
		"",
		envelope.Measurement.Digest,
		observationOutcome,
	)
	if p.runtime.lifecycle != nil {
		atomicStore, ok := p.runtime.terminalStore.(turnkernel.AtomicTerminalOperationStore)
		if !ok {
			return CommittedTerminal{}, errors.New(
				"durable lifecycle requires atomic terminal operation store",
			)
		}
		_, err = atomicStore.CommitTerminalOperation(ctx, envelope)
		committed.OperationCommitted = err == nil
	} else {
		_, err = p.runtime.terminalStore.CommitTerminal(ctx, envelope)
		committed.OperationCommitted = err == nil
	}
	if err == nil {
		p.runtime.opts.Observability.Runtime.ObserveTerminal(
			context.Background(),
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
	return committed, err
}
func (p *TerminalPublisher) Publish(ctx context.Context, committed CommittedTerminal) error {
	_, turnID, _ := protocol.OperationReferences(committed.Operation)
	entries, err := p.runtime.terminalStore.PendingOutbox(ctx, string(turnID))
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
	store, ok := p.runtime.terminalStore.(turnkernel.TerminalProjectionRecoveryStore)
	if !ok {
		if p.runtime.lifecycle != nil {
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
	data, err := decodeTerminalOutboxEntry(entry)
	if err != nil {
		return err
	}
	if entry.EventID == "" || entry.OperationID == "" || entry.ThreadID == "" ||
		entry.TurnID == "" || entry.ItemID == "" {
		return errors.New("terminal outbox event identity is incomplete")
	}
	p.runtime.mu.Lock()
	err = p.runtime.hub.PublishStable(protocol.EventMeta{
		OperationID: entry.OperationID, ThreadID: entry.ThreadID,
		TurnID: entry.TurnID, ItemID: entry.ItemID,
	}, entry.EventID, data, func(event protocol.Event) error {
		if event.OperationID != entry.OperationID || event.ThreadID != entry.ThreadID || event.TurnID != entry.TurnID || event.ItemID != entry.ItemID || string(event.Kind) != entry.Kind {
			return errors.New("terminal outbox event identity conflict")
		}
		if protocol.IsTerminalEvent(event.Kind) {
			p.runtime.terminals[event.TurnID] = event.Kind
			p.runtime.clearPendingTurn(event.TurnID)
		}
		if p.runtime.lifecycle != nil {
			return p.runtime.lifecycle.Project(context.Background(), event)
		}
		return nil
	})
	p.runtime.mu.Unlock()
	if err != nil {
		return err
	}
	return p.runtime.terminalStore.MarkOutboxPublished(ctx, turnID, []string{entry.ID})
}
func terminalOutboxEventID(turnID protocol.TurnID, entryID string) protocol.EventID {
	sum := sha256.Sum256([]byte("terminal-outbox\x00" + string(turnID) + "\x00" + entryID))
	return protocol.EventID(fmt.Sprintf("evt_%x", sum[:16]))
}
