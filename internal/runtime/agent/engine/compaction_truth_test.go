package engine

import (
	"path/filepath"
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
		receipt.CriticalFacts != 0 {
		t.Fatalf("downshift receipt=%+v", receipt)
	}
	capsule, found, err := compact.ParseTruthCapsule(engine.history[0].Text())
	if err != nil || !found ||
		truthEntityContains(capsule, compact.EntityFact, "legacy.go:10") {
		t.Fatalf("downshift capsule=%+v found=%t err=%v", capsule, found, err)
	}
}

func TestTruthCapsuleDowngradesVerifiedChangeWhenWorkspaceCannotBind(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	path := filepath.Join(t.TempDir(), "outside.go")
	engine.evidence.MarkChanged(path, 1, true)
	engine.evidence.MarkVerified([]string{path})

	capsule := engine.buildTruthCapsule(engine.buildCompactSummary(nil))
	if err := capsule.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, entity := range capsule.Entities {
		if entity.Kind != compact.EntityChange {
			continue
		}
		if entity.Verified || entity.Retention != compact.RetentionMandatory ||
			entity.WorkspaceClaimStatus != "" {
			t.Fatalf("unbound change retained an invalid claim: %+v", entity)
		}
		return
	}
	t.Fatal("change truth entity was not emitted")
}
