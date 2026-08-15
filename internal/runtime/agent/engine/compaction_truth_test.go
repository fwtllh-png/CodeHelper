package engine

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/compact"
)

func TestCompactionReceiptReportsModelDownshiftAndDropsNarrative(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.SummaryMaxBytes = 4 << 10
	previous := compact.TruthCapsule{
		SchemaVersion: compact.TruthSchemaVersion, Generation: 1,
		CompatibilityHash: "sha256:larger-model",
		ModelID:           "larger-model", ContextTokens: 8192,
		DownshiftPolicy: compact.DownshiftRuntimeTruthOnly,
		Entities: []compact.TruthEntity{
			compact.NewTruthEntity(
				compact.EntityFact,
				"legacy.go\x00definition\x0010",
				"legacy.go:10 definition",
				"runtime.evidence",
			),
		},
	}
	previous.Seal()
	rendered, err := compact.RenderStructured(
		compact.Summary{Window: 2},
		previous,
		compact.Narrative{Lines: []string{"assistant: old discussion"}},
		4<<10,
	)
	if err != nil {
		t.Fatal(err)
	}
	engine.history = []provider.Message{
		provider.TextMessage(provider.RoleSystem, rendered.Text),
		messageWithText(provider.RoleUser, strings.Repeat("old ", 500), 1),
		messageWithText(provider.RoleAssistant, strings.Repeat("answer ", 500), 1),
		messageWithText(provider.RoleUser, "current", 2),
	}
	receipt := engine.CompactForced()
	if receipt == nil || !receipt.ModelDownshifted ||
		receipt.NarrativeIncluded ||
		receipt.DownshiftPolicy != compact.DownshiftRuntimeTruthOnly ||
		receipt.TruthGeneration != 2 ||
		receipt.CriticalFacts != 1 {
		t.Fatalf("downshift receipt=%+v", receipt)
	}
	capsule, found, err := compact.ParseTruthCapsule(engine.history[0].Text())
	if err != nil || !found ||
		!truthEntityContains(capsule, compact.EntityFact, "legacy.go:10") {
		t.Fatalf("downshift capsule=%+v found=%t err=%v", capsule, found, err)
	}
}
