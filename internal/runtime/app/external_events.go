package app

import "github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"

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
	return r.publish(
		"op_external", "thread_external", "turn_external", itemID, data,
	)
}
