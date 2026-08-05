package promptcontext_test

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func TestWorldStateSectionsDigestSkip(t *testing.T) {
	runtime := policy.DefaultRuntime(policy.ModeAct, policy.PermissionAuto)
	runtime.Granular.MCP = policy.SurfaceAsk
	section := promptcontext.NewPolicySection(runtime)
	first, err := promptcontext.Assemble(promptcontext.Options{
		BaseSystem: "base", Workspace: t.TempDir(), ToolPrefix: "tools",
		Sections: []promptcontext.WorldStateSection{section},
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, msg := range first.Messages {
		if strings.Contains(msg.Text(), "granular:") && strings.Contains(msg.Text(), "mcp=ask") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("policy section missing: %+v", first.Messages)
	}
	second, err := promptcontext.Assemble(promptcontext.Options{
		BaseSystem: "base", Workspace: t.TempDir(), ToolPrefix: "tools",
		Sections:         []promptcontext.WorldStateSection{section},
		PreviousReceipts: first.Receipts,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range second.Messages {
		if strings.Contains(msg.Text(), "Policy snapshot") {
			t.Fatal("unchanged policy section should be skipped from messages")
		}
	}
	var policyReceipt *promptcontext.Receipt
	for i := range second.Receipts {
		if second.Receipts[i].Kind == promptcontext.PartitionPolicy {
			policyReceipt = &second.Receipts[i]
			break
		}
	}
	if policyReceipt == nil || policyReceipt.Digest == "" {
		t.Fatal("expected policy receipt with digest on skip")
	}
}

func TestToolCatalogSectionListsDescriptors(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	section := promptcontext.NewToolCatalogSection(registry)
	if section.Digest() == "" {
		t.Fatal("empty digest")
	}
	if len(section.Entries) == 0 {
		t.Fatal("expected catalog entries")
	}
}
