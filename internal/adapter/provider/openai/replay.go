package openai

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fwtllh-png/QCode/internal/adapter/model"
	"github.com/fwtllh-png/QCode/internal/adapter/provider"
)

type responsesReplay struct {
	Items []json.RawMessage `json:"items"`
}

func replayState(items []json.RawMessage) (*provider.ReplayState, error) {
	if len(items) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(responsesReplay{Items: items})
	if err != nil {
		return nil, fmt.Errorf("encode Responses replay state: %w", err)
	}
	return &provider.ReplayState{
		Version: provider.ReplayVersion,
		Data:    data,
	}, nil
}

func ParseResponsesReplay(
	message provider.Message,
	route model.ReadyRoute,
	adapter model.AdapterID,
) ([]json.RawMessage, error) {
	if err := provider.ValidateReplayForRoute(message, route, adapter); err != nil {
		return nil, err
	}
	if message.Provenance == nil || message.Provenance.Replay == nil {
		return nil, nil
	}
	var replay responsesReplay
	if err := json.Unmarshal(message.Provenance.Replay.Data, &replay); err != nil {
		return nil, fmt.Errorf("decode Responses replay state: %w", err)
	}
	if len(replay.Items) == 0 {
		return nil, errors.New("Responses replay state has no items")
	}
	seen := make(map[string]struct{}, len(replay.Items))
	for index, raw := range replay.Items {
		var item map[string]any
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, fmt.Errorf("decode Responses replay item %d: %w", index, err)
		}
		if stringValue(item["type"]) != "reasoning" {
			return nil, fmt.Errorf(
				"Responses replay item %d has unsupported type %q",
				index,
				stringValue(item["type"]),
			)
		}
		id := stringValue(item["id"])
		if id == "" {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("Responses replay item id %q is duplicated", id)
		}
		seen[id] = struct{}{}
	}
	return replay.Items, nil
}
