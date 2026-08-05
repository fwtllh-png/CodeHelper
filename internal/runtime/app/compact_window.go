package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// EncodeCompactedHistory converts provider messages into durable compacted messages.
func EncodeCompactedHistory(messages []provider.Message) ([]protocol.CompactedMessage, error) {
	result := make([]protocol.CompactedMessage, 0, len(messages))
	for _, message := range messages {
		content, err := json.Marshal(message.Blocks)
		if err != nil {
			return nil, fmt.Errorf("encode compacted content: %w", err)
		}
		result = append(result, protocol.CompactedMessage{
			Role: string(message.Role), Content: content, Turn: message.Turn,
		})
	}
	return result, nil
}

// DecodeCompactedHistory restores provider messages from a compacted window.
func DecodeCompactedHistory(messages []protocol.CompactedMessage) ([]provider.Message, error) {
	result := make([]provider.Message, 0, len(messages))
	for _, message := range messages {
		var blocks []provider.ContentBlock
		if len(message.Content) > 0 {
			if err := json.Unmarshal(message.Content, &blocks); err != nil {
				return nil, fmt.Errorf("decode compacted content: %w", err)
			}
		}
		result = append(result, provider.Message{
			Role: provider.Role(message.Role), Blocks: blocks, Turn: message.Turn,
		})
	}
	return result, nil
}

// LatestCompactedWindow returns the newest thread.compacted payload for threadID
// by scanning the durable/memory event log.
func LatestCompactedWindow(
	ctx context.Context, store EventStore, threadID protocol.ThreadID,
) (*protocol.ThreadCompactedData, error) {
	if store == nil {
		return nil, nil
	}
	if threadID == "" {
		return nil, fmt.Errorf("thread id is required")
	}
	events, err := store.Replay(ctx, 0)
	if err != nil {
		return nil, err
	}
	var latest *protocol.ThreadCompactedData
	for _, event := range events {
		if event.Kind != protocol.EventThreadCompacted || event.ThreadID != threadID {
			continue
		}
		data, ok := event.Data.(*protocol.ThreadCompactedData)
		if !ok || data == nil {
			continue
		}
		copy := *data
		latest = &copy
	}
	return latest, nil
}

// LatestThreadHistorySeed returns the best history seed for resume via full
// event reconstruction (compact/fork base + post-checkpoint durable events).
func LatestThreadHistorySeed(
	ctx context.Context, store EventStore, threadID protocol.ThreadID,
) (*protocol.ThreadCompactedData, error) {
	if store == nil || threadID == "" {
		return nil, nil
	}
	events, err := store.Replay(ctx, 0)
	if err != nil {
		return nil, err
	}
	recon, err := ReconstructThread(events, threadID)
	if err != nil {
		return nil, err
	}
	if len(recon.History) == 0 {
		return nil, nil
	}
	encoded, err := EncodeCompactedHistory(recon.History)
	if err != nil {
		return nil, err
	}
	windowID := recon.Window.Current
	if windowID == "" {
		windowID = "reconstructed:" + string(threadID)
	}
	firstID := recon.Window.FirstID
	if firstID == "" {
		firstID = windowID
	}
	number := recon.Window.Number
	if number == 0 {
		number = 1
	}
	return &protocol.ThreadCompactedData{
		Summary:            "reconstructed from event log",
		ReplacementHistory: encoded,
		WindowNumber:       number,
		FirstWindowID:      firstID,
		WindowID:           windowID,
	}, nil
}
