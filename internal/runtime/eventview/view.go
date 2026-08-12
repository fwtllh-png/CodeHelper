package eventview

import (
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)
type TerminalUpdate struct {
	Status  string
	Code    protocol.ErrorCode
	Message string
}
type Update struct {
	Traits   protocol.EventTraits
	Data     protocol.EventData
	Terminal *TerminalUpdate
}
func Project(event protocol.Event) (Update, error) {
	traits, ok := protocol.Traits(event.Kind)
	if !ok {
		return Update{}, fmt.Errorf("event %q has no protocol traits", event.Kind)
	}
	update := Update{Traits: traits, Data: event.Data}
	switch data := event.Data.(type) {
	case *protocol.TurnCompletedData:
		update.Terminal = &TerminalUpdate{Status: "completed", Message: data.Text}
	case *protocol.TurnFailedData:
		update.Terminal = &TerminalUpdate{Status: "failed", Code: data.Code, Message: data.Message}
	case *protocol.TurnCanceledData:
		update.Terminal = &TerminalUpdate{Status: "canceled", Code: protocol.CodeCanceled, Message: data.Reason}
	case *protocol.OperationRejectedData:
		update.Terminal = &TerminalUpdate{Status: "rejected", Code: data.Code, Message: data.Message}
	}
	return update, nil
}
