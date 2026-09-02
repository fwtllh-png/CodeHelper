package eventlog

import (
	"encoding/json"
	"fmt"

	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
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
	if err := json.Unmarshal(envelope["kind"], &kind); err != nil {
		return decodeErr
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(envelope["data"], &data); err != nil {
		return decodeErr
	}
	changed := false
	if kind == protocol.EventTurnStarted ||
		kind == protocol.EventExecutionReceipt {
		if _, ok := data["orchestration"]; ok {
			delete(data, "orchestration")
			changed = true
		}
	}
	if kind != protocol.EventToolResult {
		if !changed {
			return decodeErr
		}
		return decodeNormalizedEvent(envelope, data, event)
	}
	var execution map[string]json.RawMessage
	if err := json.Unmarshal(data["execution"], &execution); err != nil {
		return decodeErr
	}
	var attempts []map[string]json.RawMessage
	if err := json.Unmarshal(execution["attempts"], &attempts); err != nil {
		return decodeErr
	}
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
	return decodeNormalizedEvent(envelope, data, event)
}

func decodeNormalizedEvent(
	envelope map[string]json.RawMessage,
	data map[string]json.RawMessage,
	event *protocol.Event,
) error {
	encodedData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("normalize legacy event data: %w", err)
	}
	envelope["data"] = encodedData
	normalized, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("normalize legacy event: %w", err)
	}
	return json.Unmarshal(normalized, event)
}
