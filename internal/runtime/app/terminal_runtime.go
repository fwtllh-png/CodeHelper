package app

import (
	"context"

	agentcontext "github.com/fwtllh-png/QCode/internal/runtime/agent/context"
	"github.com/fwtllh-png/QCode/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/QCode/internal/runtime/app/eventhub"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func (r *Runtime) TerminalContent() eventhub.TerminalContentStore {
	return r.content
}

func (r *Runtime) TerminalOperationReceipt(
	operationID protocol.OperationID,
) any {
	return r.OperationService.operationCommitReceipt(operationID)
}

func (r *Runtime) TerminalProjectionIdentity(
	turnID protocol.TurnID,
	operationID protocol.OperationID,
	itemID protocol.ItemID,
) (protocol.OperationID, protocol.ItemID) {
	if stored, ok := r.active.LookupTurn(turnID); ok {
		if stored.OperationID != "" {
			operationID = stored.OperationID
		}
		if stored.ItemID != "" {
			itemID = stored.ItemID
		}
	}
	return operationID, itemID
}

func (r *Runtime) TerminalStore() turnkernel.TerminalEnvelopeStore {
	return r.terminalStore
}

func (r *Runtime) DurableTerminal() bool { return r.lifecycle != nil }

func (r *Runtime) LoadContextManifest(
	threadID protocol.ThreadID,
) (agentcontext.ContextManifest, bool) {
	value, ok := r.contextManifests.Load(threadID)
	if !ok {
		return agentcontext.ContextManifest{}, false
	}
	manifest, ok := value.(agentcontext.ContextManifest)
	return manifest, ok
}

func (r *Runtime) StoreContextManifest(
	threadID protocol.ThreadID,
	manifest agentcontext.ContextManifest,
) {
	r.contextManifests.Store(threadID, manifest)
}

func (r *Runtime) PublishTerminalProjection(
	_ context.Context,
	entry turnkernel.ProjectionOutboxEntry,
	data protocol.EventData,
) error {
	r.EventService.mu.Lock()
	defer r.EventService.mu.Unlock()
	return r.hub.PublishStable(protocol.EventMeta{
		OperationID: entry.OperationID,
		ThreadID:    entry.ThreadID,
		TurnID:      entry.TurnID,
		ItemID:      entry.ItemID,
	}, entry.EventID, data, func(event protocol.Event) error {
		if event.OperationID != entry.OperationID ||
			event.ThreadID != entry.ThreadID ||
			event.TurnID != entry.TurnID ||
			event.ItemID != entry.ItemID ||
			string(event.Kind) != entry.Kind {
			return runtimeProblem(
				protocol.CodeConflict,
				"terminal outbox event identity conflict",
				nil,
			)
		}
		if protocol.IsTerminalEvent(event.Kind) {
			r.terminals[event.TurnID] = event.Kind
			r.clearPendingTurn(event.TurnID)
		}
		if r.lifecycle != nil {
			return r.lifecycle.Project(context.Background(), event)
		}
		return nil
	})
}
