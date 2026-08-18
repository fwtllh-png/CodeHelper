package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/kernel"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/model"
	orchestrationstore "github.com/fwtllh-png/CodeHelper/internal/orchestration/store"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/projection"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// PublishExternal appends and broadcasts orchestration events that originate
// outside an Engine turn while preserving the Runtime's single event sequence.
func (r *Runtime) PublishExternal(data protocol.EventData) error {
	if r == nil {
		return ErrClosed
	}
	itemID, err := protocol.NewItemID()
	if err != nil {
		return err
	}
	publishedAt := time.Now().UTC()
	if err := r.publish(
		"op_external", "thread_external", "turn_external", itemID, data,
	); err != nil {
		return err
	}
	r.metrics.ObserveAgentEvent(data, publishedAt)
	return nil
}

type orchestrationEffectStore interface {
	OrchestrationController
	Load(context.Context, protocol.RunID) (model.Graph, error)
	PendingTerminalEffects(
		context.Context,
		int,
	) ([]orchestrationstore.OutboxEntry, error)
}

// DrainWorkGraphEffects publishes durable terminal effects without adding a
// polling loop. Stable identities close the publish-before-ack crash window.
func (r *Runtime) DrainWorkGraphEffects(ctx context.Context) error {
	if r == nil {
		return ErrClosed
	}
	store, ok := r.orchestration.(orchestrationEffectStore)
	if !ok {
		return nil
	}
	r.orchestrationEffects.Lock()
	defer r.orchestrationEffects.Unlock()
	const batchSize = 100
	for {
		pending, err := store.PendingTerminalEffects(ctx, batchSize)
		if err != nil {
			return fmt.Errorf("list terminal WorkGraph effects: %w", err)
		}
		if len(pending) == 0 {
			return nil
		}
		for _, entry := range pending {
			if err := r.publishWorkGraphTerminal(ctx, store, entry.Effect); err != nil {
				return err
			}
		}
	}
}

func (r *Runtime) publishWorkGraphTerminal(
	ctx context.Context,
	store orchestrationEffectStore,
	effect model.Effect,
) error {
	graph, err := store.Load(ctx, effect.RunID)
	if err != nil {
		return fmt.Errorf("load terminal WorkGraph %s: %w", effect.RunID, err)
	}
	data, err := projection.RunTerminal(graph)
	if err != nil {
		return err
	}
	identity := stableWorkGraphEffectIdentity(effect.ID)
	if err := r.publishStable(
		protocol.OperationID("op_workgraph_"+identity),
		graph.Run.RootThreadID,
		protocol.TurnID("turn_workgraph_"+identity),
		protocol.ItemID("item_workgraph_"+identity),
		protocol.EventID("evt_"+identity),
		data,
	); err != nil {
		return fmt.Errorf("publish terminal WorkGraph effect %s: %w", effect.ID, err)
	}
	r.metrics.ObserveAgentEvent(data, time.Now().UTC())
	_, err = store.Execute(ctx, kernel.Command{
		ID:               "effect:publish:" + string(effect.ID),
		Kind:             kernel.CommandPublishEffect,
		RunID:            effect.RunID,
		EffectID:         effect.ID,
		ExpectedRevision: graph.Run.Revision,
		At:               time.Now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("acknowledge terminal WorkGraph effect %s: %w", effect.ID, err)
	}
	return nil
}

func stableWorkGraphEffectIdentity(effectID protocol.EffectID) string {
	sum := sha256.Sum256([]byte("work-graph-effect\x00" + string(effectID)))
	return hex.EncodeToString(sum[:16])
}
