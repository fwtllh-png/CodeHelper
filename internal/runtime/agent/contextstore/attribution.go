package contextstore

import (
	"encoding/json"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type Estimator interface {
	Estimate([]provider.Message) (uint64, error)
}

type EstimatorFunc func([]provider.Message) (uint64, error)

func (f EstimatorFunc) Estimate(messages []provider.Message) (uint64, error) {
	return f(messages)
}

type ImageEstimator interface {
	EstimateImage(provider.Attachment) (uint64, error)
}

func ApplyTransport(
	context *protocol.SampleContextData,
	value provider.TransportMetadata,
) {
	if context == nil {
		return
	}
	context.RequestBytes = value.RequestBytes
	context.LogicalRequestDigest = value.LogicalRequestDigest
	context.TransportPayloadDigest = value.TransportPayloadDigest
	context.IncrementalTransport = value.Incremental
}

// Measure attributes the complete immutable Snapshot used for one sample.
func (s Snapshot) Measure(
	reason string,
	reasoningEffort string,
	estimate Estimator,
) (protocol.SampleContextData, error) {
	stable := s.partitions[KindStable]
	history := s.partitions[KindHistory]
	dynamic := s.partitions[KindDynamic]
	continuation := s.partitions[KindContinuation]
	result := protocol.SampleContextData{
		Reason: reason, ReasoningEffort: reasoningEffort,
		ContextRevision:     s.revision,
		MessageCount:        len(stable) + len(history) + len(dynamic) + len(continuation),
		ToolDefinitionCount: len(s.definitions),
	}
	digest, err := s.Digest()
	if err != nil {
		return protocol.SampleContextData{}, fmt.Errorf("digest context snapshot: %w", err)
	}
	result.ContextDigest = digest
	if result.StableTokens, err = countMessages(stable, estimate); err != nil {
		return protocol.SampleContextData{}, err
	}
	groups := map[provider.Role]*uint64{
		provider.RoleUser:      &result.HistoryUserTokens,
		provider.RoleAssistant: &result.HistoryAssistantTokens,
		provider.RoleTool:      &result.HistoryToolTokens,
	}
	for _, message := range history {
		target := groups[message.Role]
		if target == nil {
			target = &result.HistoryOtherTokens
		}
		tokens, estimateErr := countMessages([]provider.Message{message}, estimate)
		if estimateErr != nil {
			return protocol.SampleContextData{}, estimateErr
		}
		*target += tokens
	}
	if result.DynamicTokens, err = countMessages(dynamic, estimate); err != nil {
		return protocol.SampleContextData{}, err
	}
	if result.ContinuationTokens, err = countMessages(continuation, estimate); err != nil {
		return protocol.SampleContextData{}, err
	}
	for _, message := range s.Messages() {
		for _, block := range message.Blocks {
			if block.Type != provider.ContentImage || block.Attachment == nil {
				continue
			}
			imageEstimator, ok := estimate.(ImageEstimator)
			if !ok {
				continue
			}
			tokens, estimateErr := imageEstimator.EstimateImage(*block.Attachment)
			if estimateErr != nil {
				return protocol.SampleContextData{}, estimateErr
			}
			result.ImageTokens += tokens
		}
	}
	if len(s.definitions) != 0 {
		encoded, encodeErr := json.Marshal(s.definitions)
		if encodeErr != nil {
			return protocol.SampleContextData{}, fmt.Errorf(
				"encode tool definitions: %w",
				encodeErr,
			)
		}
		result.ToolDefinitionTokens = uint64((len(encoded) + 3) / 4)
	}
	result.EstimatedTokens = result.StableTokens + result.HistoryUserTokens +
		result.HistoryAssistantTokens + result.HistoryToolTokens +
		result.HistoryOtherTokens + result.DynamicTokens +
		result.ContinuationTokens + result.ToolDefinitionTokens
	result.ProviderFramingTokens = (result.EstimatedTokens*12 + 99) / 100
	result.EstimatedTokens += result.ProviderFramingTokens
	attributedNonText := result.ImageTokens + result.ToolDefinitionTokens +
		result.ProviderFramingTokens
	result.TextTokens = result.EstimatedTokens -
		min(result.EstimatedTokens, attributedNonText)
	return result, nil
}

func countMessages(messages []provider.Message, estimate Estimator) (uint64, error) {
	if len(messages) == 0 {
		return 0, nil
	}
	return estimate.Estimate(messages)
}
