package eventlog

import (
	"encoding/json"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func decodePersistedEvent(payload []byte, event *protocol.Event) error {
	decodeErr := json.Unmarshal(payload, event)
	if decodeErr == nil {
		return nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return decodeErr
	}
	var kind protocol.EventKind
	if err := json.Unmarshal(envelope["kind"], &kind); err != nil ||
		kind != protocol.EventToolResult {
		return decodeErr
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(envelope["data"], &data); err != nil {
		return decodeErr
	}
	var execution map[string]json.RawMessage
	if err := json.Unmarshal(data["execution"], &execution); err != nil {
		return decodeErr
	}
	var attempts []map[string]json.RawMessage
	if err := json.Unmarshal(execution["attempts"], &attempts); err != nil {
		return decodeErr
	}
	changed := false
	for _, attempt := range attempts {
		for _, field := range []string{
			"sandbox_strength", "filesystem_unrestricted",
		} {
			if _, ok := attempt[field]; ok {
				delete(attempt, field)
				changed = true
			}
		}
	}
	if !changed {
		return decodeErr
	}
	var err error
	if execution["attempts"], err = json.Marshal(attempts); err != nil {
		return fmt.Errorf("normalize legacy tool attempts: %w", err)
	}
	if data["execution"], err = json.Marshal(execution); err != nil {
		return fmt.Errorf("normalize legacy tool execution: %w", err)
	}
	if envelope["data"], err = json.Marshal(data); err != nil {
		return fmt.Errorf("normalize legacy tool result: %w", err)
	}
	normalized, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("normalize legacy event: %w", err)
	}
	return json.Unmarshal(normalized, event)
}
