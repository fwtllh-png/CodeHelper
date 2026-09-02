package app

import (
	"time"

	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
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
