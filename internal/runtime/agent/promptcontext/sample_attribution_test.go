package promptcontext

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

func TestMeasureSampleAttributesMessagesAndToolDefinitions(t *testing.T) {
	estimate := func(messages []provider.Message) (uint64, error) {
		return uint64(len(messages) * 10), nil
	}
	got, err := MeasureSample(SampleInput{
		Reason: SampleNormal,
		Stable: []provider.Message{provider.TextMessage(provider.RoleSystem, "stable")},
		History: []provider.Message{
			provider.TextMessage(provider.RoleUser, "question"),
			provider.TextMessage(provider.RoleAssistant, "answer"),
			provider.TextMessage(provider.RoleTool, "result"),
		},
		Dynamic: []provider.Message{provider.TextMessage(provider.RoleSystem, "dynamic")},
		Definitions: []provider.ToolDefinition{{
			Name: "read", Description: "read a file",
		}},
		Estimate: estimate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.StableTokens != 10 || got.HistoryUserTokens != 10 ||
		got.HistoryAssistantTokens != 10 || got.HistoryToolTokens != 10 ||
		got.DynamicTokens != 10 || got.ToolDefinitionTokens == 0 ||
		got.ProviderFramingTokens == 0 || got.EstimatedTokens == 0 ||
		got.MessageCount != 5 {
		t.Fatalf("attribution=%+v", got)
	}
}

func TestSampleReasonClassifiesRetryBeforeContinuation(t *testing.T) {
	if got := SampleReason(SampleNormal, 1, true); got != SampleProviderRetry {
		t.Fatalf("retry reason=%q", got)
	}
	if got := SampleReason(SampleNormal, 0, true); got != SampleOutputContinuation {
		t.Fatalf("continuation reason=%q", got)
	}
}
