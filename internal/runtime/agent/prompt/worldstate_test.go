package prompt_test

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	promptcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/prompt"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func TestWorldTextAssemblyLeavesDiffingToContextStore(t *testing.T) {
	runtime := policy.DefaultRuntime(policy.ModeAct, policy.PermissionAuto)
	runtime.Granular.MCP = policy.SurfaceAsk
	runtime.ConfigurePlanning(policy.PlanningRequired, policy.PlanApprovalManual)
	section := promptcontext.NewPolicySection(runtime)
	messages, receipt := promptcontext.AssembleWorldText(
		section.ID(),
		"worldstate://policy",
		section.Render(),
		promptcontext.Budget{MaxBytes: 2 << 10, MaxTokens: 512},
	)
	if len(messages) != 1 ||
		!strings.Contains(messages[0].Text(), "granular:") ||
		!strings.Contains(messages[0].Text(), "mcp=ask") ||
		!strings.Contains(messages[0].Text(), "submit_plan is required") ||
		!strings.Contains(messages[0].Text(), "wait for approval") ||
		receipt.Kind != promptcontext.PartitionPolicy ||
		receipt.RetainedBytes == 0 {
		t.Fatalf("messages=%+v receipt=%+v", messages, receipt)
	}
}

func TestToolCatalogSectionDoesNotRepeatProviderDefinitions(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	section := promptcontext.NewToolCatalogSection(registry)
	if section.Digest() == "" {
		t.Fatal("empty digest")
	}
	if len(section.Entries) == 0 {
		t.Fatal("expected catalog entries")
	}
	rendered := section.Render()
	if !strings.Contains(rendered, "advertised=") ||
		strings.Contains(rendered, section.Entries[0].Description) ||
		strings.Contains(rendered, "- "+section.Entries[0].Name) {
		t.Fatalf("catalog duplicated provider definition: %q", rendered)
	}
	changedDefinition := section
	changedDefinition.Entries = append(
		[]promptcontext.ToolCatalogEntry(nil),
		section.Entries...,
	)
	changedDefinition.Entries[0].Description = "changed provider definition"
	if changedDefinition.Digest() != section.Digest() {
		t.Fatal("World digest followed a Provider definition it does not render")
	}
	changedCount := section
	changedCount.Entries = append(
		append(
			[]promptcontext.ToolCatalogEntry(nil),
			section.Entries...,
		),
		promptcontext.ToolCatalogEntry{Name: "new"},
	)
	if changedCount.Digest() == section.Digest() {
		t.Fatal("World digest ignored a model-visible count change")
	}
}
