package main

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/bench"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestCalculateUsesNearestRankAndMAD(t *testing.T) {
	got := calculate([]uint64{100, 100, 110, 130, 200})
	if got.P50 != 110 || got.P75 != 130 || got.P90 != 200 || got.MAD != 10 {
		t.Fatalf("stats=%+v", got)
	}
}

func TestSummarizeAttributesEverySample(t *testing.T) {
	results := []bench.Result{{
		Passed: true, InputTokens: 100, UncachedInputTokens: 75,
		OutputTokens: 20, ReasoningTokens: 5,
		Samples: []protocol.UsageData{{
			Sample: 1, InputTokens: 100, CachedTokens: 25,
			Context: &protocol.SampleContextData{
				Reason: "normal", StableTokens: 20, DynamicTokens: 10,
				ToolDefinitionTokens: 40, EstimatedTokens: 70,
				WindowProjectedTokens: 80,
				PairingCalls:          2, PairingResults: 2, PairingPairs: 2,
				MaxItemTokens:  55,
				AdmissionItems: 2, AdmissionSpilledItems: 1,
				AdmissionOriginalTokens: 100, AdmissionRetainedTokens: 60,
			},
		}},
	}}
	got := summarize(results)
	if got.Passed != 1 || got.Input.P50 != 100 || got.UncachedInput.P50 != 75 ||
		got.Calls.P50 != 1 || got.ContextP50["tool_definitions"] != 40 ||
		got.Reasons["normal"] != 1 || got.EstimatorErrorP95 != 0.3 ||
		got.TriggerErrorP95 != 0.2 ||
		got.PairingCalls != 2 || got.PairingResults != 2 ||
		got.PairingPairs != 2 || got.PairingVisibleOrphans != 0 ||
		got.MaxItemTokens != 55 ||
		got.AdmissionItems != 2 || got.AdmissionSpilledItems != 1 ||
		got.AdmissionOriginal != 100 || got.AdmissionRetained != 60 ||
		got.AttributionCoverageBP.P50 != 10_000 {
		t.Fatalf("report=%+v", got)
	}
}

func TestSummarizeReportsMissingAttribution(t *testing.T) {
	got := summarize([]bench.Result{{
		Passed: true,
		Samples: []protocol.UsageData{
			{Sample: 1, Context: &protocol.SampleContextData{Reason: "normal"}},
			{Sample: 2},
		},
	}})
	if got.AttributionCoverageBP.P50 != 5_000 {
		t.Fatalf("attribution coverage=%+v", got.AttributionCoverageBP)
	}
}

func TestDeltaForZeroBaselineStaysFinite(t *testing.T) {
	got := deltaFor(0, 10)
	if got.Absolute != 10 || got.Relative != 0 {
		t.Fatalf("delta=%+v", got)
	}
}
