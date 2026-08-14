package anthropic

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

type signatureReplay struct {
	Signatures []replaySignature `json:"signatures"`
}

type replaySignature struct {
	Block int    `json:"block"`
	Value string `json:"value"`
}

func anthropicReplayState(
	signatures map[int]string,
) (*provider.ReplayState, error) {
	if len(signatures) == 0 {
		return nil, nil
	}
	replay := signatureReplay{
		Signatures: make([]replaySignature, 0, len(signatures)),
	}
	blocks := make([]int, 0, len(signatures))
	for block := range signatures {
		blocks = append(blocks, block)
	}
	sort.Ints(blocks)
	for _, block := range blocks {
		replay.Signatures = append(replay.Signatures, replaySignature{
			Block: block, Value: signatures[block],
		})
	}
	data, err := json.Marshal(replay)
	if err != nil {
		return nil, fmt.Errorf("encode Anthropic replay state: %w", err)
	}
	return &provider.ReplayState{
		Version: provider.ReplayVersion,
		Data:    data,
	}, nil
}

func replaySignatures(
	message provider.Message,
	route model.ReadyRoute,
) (map[int]string, error) {
	if err := provider.ValidateReplayForRoute(
		message, route, model.AdapterAnthropic,
	); err != nil {
		return nil, err
	}
	if message.Provenance == nil || message.Provenance.Replay == nil {
		return nil, nil
	}
	var replay signatureReplay
	if err := json.Unmarshal(message.Provenance.Replay.Data, &replay); err != nil {
		return nil, fmt.Errorf("decode Anthropic replay state: %w", err)
	}
	if len(replay.Signatures) == 0 {
		return nil, errors.New("Anthropic replay state has no signatures")
	}
	result := make(map[int]string, len(replay.Signatures))
	for _, signature := range replay.Signatures {
		if signature.Block < 0 || signature.Value == "" {
			return nil, errors.New("Anthropic replay signature is invalid")
		}
		if _, duplicate := result[signature.Block]; duplicate {
			return nil, fmt.Errorf(
				"Anthropic replay signature block %d is duplicated",
				signature.Block,
			)
		}
		result[signature.Block] = signature.Value
	}
	return result, nil
}
