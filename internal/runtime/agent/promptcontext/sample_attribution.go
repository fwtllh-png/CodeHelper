package promptcontext

import (
	"encoding/json"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const (
	SampleNormal             = "normal"
	SampleOutputContinuation = "output_continuation"
	SampleCompletionRepair   = "completion_repair"
	SampleWorkspaceRepair    = "workspace_repair"
	SampleDeclarationRepair  = "declaration_repair"
	SampleVerificationRepair = "verification_repair"
	SampleToolFailureRepair  = "tool_failure_repair"
	SampleProviderRetry      = "provider_retry"
)

type SampleInput struct {
	Reason                                 string
	ReasoningEffort                        string
	Stable, History, Dynamic, Continuation []provider.Message
	Definitions                            []provider.ToolDefinition
	Estimate                               func([]provider.Message) (uint64, error)
}

func ApplyTransport(context *protocol.SampleContextData, value provider.TransportMetadata) {
	if context == nil {
		return
	}
	context.RequestBytes, context.LogicalRequestDigest = value.RequestBytes, value.LogicalRequestDigest
	context.TransportPayloadDigest, context.IncrementalTransport = value.TransportPayloadDigest, value.Incremental
}

func SampleReason(initial string, attempt int, continuation bool) string {
	if attempt > 0 {
		return SampleProviderRetry
	}
	if continuation {
		return SampleOutputContinuation
	}
	return initial
}

func MeasureSample(input SampleInput) (protocol.SampleContextData, error) {
	result := protocol.SampleContextData{
		Reason:              input.Reason,
		ReasoningEffort:     input.ReasoningEffort,
		MessageCount:        len(input.Stable) + len(input.History) + len(input.Dynamic) + len(input.Continuation),
		ToolDefinitionCount: len(input.Definitions),
	}
	var err error
	if result.StableTokens, err = countMessages(input.Stable, input.Estimate); err != nil {
		return protocol.SampleContextData{}, err
	}
	groups := map[provider.Role]*uint64{
		provider.RoleUser: &result.HistoryUserTokens, provider.RoleAssistant: &result.HistoryAssistantTokens,
		provider.RoleTool: &result.HistoryToolTokens,
	}
	for _, message := range input.History {
		target := groups[message.Role]
		if target == nil {
			target = &result.HistoryOtherTokens
		}
		tokens, estimateErr := countMessages([]provider.Message{message}, input.Estimate)
		if estimateErr != nil {
			return protocol.SampleContextData{}, estimateErr
		}
		*target += tokens
	}
	if result.DynamicTokens, err = countMessages(input.Dynamic, input.Estimate); err != nil {
		return protocol.SampleContextData{}, err
	}
	if result.ContinuationTokens, err = countMessages(input.Continuation, input.Estimate); err != nil {
		return protocol.SampleContextData{}, err
	}
	if len(input.Definitions) != 0 {
		encoded, encodeErr := json.Marshal(input.Definitions)
		if encodeErr != nil {
			return protocol.SampleContextData{}, fmt.Errorf("encode tool definitions: %w", encodeErr)
		}
		result.ToolDefinitionTokens = uint64((len(encoded) + 3) / 4)
	}
	result.EstimatedTokens = result.StableTokens + result.HistoryUserTokens +
		result.HistoryAssistantTokens + result.HistoryToolTokens +
		result.HistoryOtherTokens + result.DynamicTokens +
		result.ContinuationTokens + result.ToolDefinitionTokens
	result.ProviderFramingTokens = (result.EstimatedTokens*12 + 99) / 100
	result.EstimatedTokens += result.ProviderFramingTokens
	return result, nil
}

func countMessages(
	messages []provider.Message,
	estimate func([]provider.Message) (uint64, error),
) (uint64, error) {
	if len(messages) == 0 {
		return 0, nil
	}
	return estimate(messages)
}
