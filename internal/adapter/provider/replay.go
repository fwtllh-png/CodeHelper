package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
)

const ReplayVersion uint32 = 1

type AssistantProvenance struct {
	Adapter  model.AdapterID `json:"adapter"`
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Replay   *ReplayState    `json:"replay,omitempty"`
}

type ReplayState struct {
	Version       uint32          `json:"version"`
	ContentDigest string          `json:"content_digest"`
	Data          json.RawMessage `json:"data"`
}

func ProducedAssistant(
	route model.ReadyRoute,
	blocks []ContentBlock,
	turn uint64,
	replay *ReplayState,
) Message {
	message := Message{
		Role: RoleAssistant, Blocks: blocks, Turn: turn,
		Provenance: &AssistantProvenance{
			Adapter: route.Adapter(), Provider: route.ProviderID(),
			Model: route.Model().ID,
		},
	}
	if replay != nil {
		state := cloneReplay(replay)
		state.ContentDigest = MessageContentDigest(message)
		message.Provenance.Replay = state
	}
	return message
}

func MessageContentDigest(message Message) string {
	blocks := make([]ContentBlock, len(message.Blocks))
	copy(blocks, message.Blocks)
	payload, _ := json.Marshal(struct {
		Role   Role           `json:"role"`
		Blocks []ContentBlock `json:"content"`
	}{Role: message.Role, Blocks: blocks})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func FilterReplayForRoute(
	messages []Message,
	route model.ReadyRoute,
) []Message {
	filtered := make([]Message, len(messages))
	for index, message := range messages {
		filtered[index] = message
		filtered[index].Blocks = append([]ContentBlock(nil), message.Blocks...)
		if message.Provenance == nil {
			continue
		}
		provenance := *message.Provenance
		provenance.Replay = cloneReplay(message.Provenance.Replay)
		if provenance.Adapter != route.Adapter() || provenance.Provider != route.ProviderID() ||
			provenance.Model != route.Model().ID || provenance.Replay != nil &&
			provenance.Replay.ContentDigest != MessageContentDigest(filtered[index]) {
			provenance.Replay = nil
		}
		filtered[index].Provenance = &provenance
	}
	return filtered
}

func ValidateReplayForRoute(
	message Message,
	route model.ReadyRoute,
	adapter model.AdapterID,
) error {
	if err := validateMessageProvenance(message); err != nil {
		return err
	}
	if message.Provenance == nil || message.Provenance.Replay == nil {
		return nil
	}
	provenance := message.Provenance
	if provenance.Adapter != adapter ||
		provenance.Provider != route.ProviderID() ||
		provenance.Model != route.Model().ID {
		return errors.New("replay state provenance does not match target route")
	}
	return nil
}

func validateMessageProvenance(message Message) error {
	if message.Provenance == nil {
		return nil
	}
	if message.Role != RoleAssistant {
		return errors.New("assistant provenance is only valid on assistant messages")
	}
	provenance := message.Provenance
	if provenance.Adapter == "" || provenance.Provider == "" ||
		provenance.Model == "" {
		return errors.New("assistant provenance is incomplete")
	}
	if provenance.Replay == nil {
		return nil
	}
	replay := provenance.Replay
	if replay.Version != ReplayVersion {
		return fmt.Errorf("unsupported replay state version %d", replay.Version)
	}
	if len(replay.Data) == 0 || len(replay.Data) > 1<<20 ||
		!json.Valid(replay.Data) {
		return errors.New("replay state data is invalid")
	}
	if replay.ContentDigest == "" ||
		replay.ContentDigest != MessageContentDigest(message) {
		return errors.New("replay state does not match assistant content")
	}
	return nil
}

func cloneReplay(replay *ReplayState) *ReplayState {
	if replay == nil {
		return nil
	}
	copy := *replay
	copy.Data = append(json.RawMessage(nil), replay.Data...)
	return &copy
}
