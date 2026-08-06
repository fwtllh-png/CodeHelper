package protocol

import (
	"errors"
	"fmt"
)

func (o Operation) Validate() error {
	if o.Version != Version {
		return fmt.Errorf("unsupported operation version %d", o.Version)
	}
	if o.ID == "" {
		return errors.New("operation id is required")
	}
	if o.CreatedAt.IsZero() {
		return errors.New("operation created_at is required")
	}
	if o.Payload == nil {
		return errors.New("operation payload is required")
	}
	if o.Kind != o.Payload.operationKind() {
		return fmt.Errorf("operation kind %q does not match payload %q", o.Kind, o.Payload.operationKind())
	}
	return o.Payload.validate()
}

func (e Event) Validate() error {
	if e.Version != Version {
		return fmt.Errorf("unsupported event version %d", e.Version)
	}
	if e.ID == "" {
		return errors.New("event id is required")
	}
	if e.Sequence == 0 {
		return errors.New("event sequence must be positive")
	}
	if e.OperationID == "" {
		return errors.New("event operation_id is required")
	}
	if err := validateReferences(e.ThreadID, e.TurnID, e.ItemID); err != nil {
		return err
	}
	if e.CreatedAt.IsZero() {
		return errors.New("event created_at is required")
	}
	if e.Data == nil {
		return errors.New("event data is required")
	}
	if e.Kind != e.Data.eventKind() {
		return fmt.Errorf("event kind %q does not match data %q", e.Kind, e.Data.eventKind())
	}
	return e.Data.validate()
}
