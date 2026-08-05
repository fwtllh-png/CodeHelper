package promptcontext

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/evidence"
)

func evidenceSnapshot() evidence.Snapshot {
	return evidence.Snapshot{
		Turn: 3,
		Facts: []evidence.Fact{
			{
				Kind: evidence.KindDefinition, Path: "auth/token.go", Line: 12,
				Symbol: "Verify", Tool: "search_definition", Turn: 2,
			},
			{
				Kind: evidence.KindReference, Path: "api/handler.go", Line: 88,
				Symbol: "Verify", Tool: "search_references", Turn: 2,
			},
			{Kind: evidence.KindConfig, Path: "go.mod", Tool: "search_files", Turn: 1},
		},
		Risks: []evidence.Risk{
			{Kind: evidence.RiskUnverifiedChange, Path: "auth/token.go", Turn: 3},
			{Kind: evidence.RiskOpenDiagnostics, Path: "api/handler.go", Turn: 3},
		},
		Reminders: []evidence.Reminder{
			{Kind: evidence.ReminderRepeatedCall, Detail: "search_text ran 3 times this turn"},
		},
		OmittedFacts: 4,
	}
}

func TestEvidenceSectionRendersRemindersRisksThenFacts(t *testing.T) {
	assembled := AssembleTurn(TurnOptions{Turn: 3, Evidence: evidenceSnapshot()})
	if len(assembled.Messages) != 1 {
		t.Fatalf("messages = %+v, want only the evidence section", assembled.Messages)
	}
	if assembled.Messages[0].Role != provider.RoleSystem {
		t.Fatalf("role = %q, want system like the other tail sections", assembled.Messages[0].Role)
	}
	text := assembled.Messages[0].Text()
	for _, want := range []string{
		"[evidence turn=3]",
		"search_text ran 3 times this turn",
		"auth/token.go — changed, nothing verified it (turn 3)",
		"api/handler.go — diagnostics still failing (turn 3)",
		"auth/token.go:12 definition Verify (search_definition, turn 2)",
		"api/handler.go:88 reference Verify (search_references, turn 2)",
		"go.mod config (search_files, turn 1)",
		"(4 more not listed)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("evidence missing %q:\n%s", want, text)
		}
	}
	// The order carries the priority: a byte budget cuts the tail of the section,
	// so advice has to come before the list of paths.
	reminders := strings.Index(text, "wasted effort")
	risks := strings.Index(text, "unproved")
	facts := strings.Index(text, "what lookups established")
	if !(reminders < risks && risks < facts) {
		t.Fatalf("sections out of order (%d, %d, %d):\n%s", reminders, risks, facts, text)
	}
}

func TestEmptyEvidenceProducesNoSectionAtAll(t *testing.T) {
	assembled := AssembleTurn(TurnOptions{Turn: 1, Evidence: evidence.Snapshot{Turn: 1}})
	if len(assembled.Messages) != 0 || len(assembled.Receipts) != 0 {
		t.Fatalf("empty evidence produced %+v / %+v", assembled.Messages, assembled.Receipts)
	}
}

func TestEvidenceSectionKeepsRemindersWhenTheBudgetCutsIt(t *testing.T) {
	const budget = 160
	assembled := AssembleTurn(TurnOptions{
		Turn: 3, Evidence: evidenceSnapshot(),
		Budgets: map[string]Budget{PartitionEvidence: {MaxBytes: budget}},
	})
	receipt := receiptFor(t, assembled.Receipts, PartitionEvidence)
	if !receipt.Truncated || receipt.TruncationReason != "byte_budget" {
		t.Fatalf("receipt = %+v, want a byte-budget truncation", receipt)
	}
	text := assembled.Messages[0].Text()
	if !strings.Contains(text, "search_text ran 3 times this turn") {
		t.Fatalf("the cut dropped the reminder it was ordered to keep:\n%s", text)
	}
	if !strings.HasSuffix(text, truncationNotice) || len(text) > budget {
		t.Fatalf("cut section = %q (%d bytes, budget %d)", text, len(text), budget)
	}
}

func TestCodingPolicySectionIsSmallAndStable(t *testing.T) {
	section := NewCodingPolicySection()
	if section.ID() != PartitionCodingPolicy {
		t.Fatalf("id = %q", section.ID())
	}
	body := section.Render()
	// The method rides in the stable prefix on every request, so its cost is paid
	// once per session and must stay small.
	if len(body) > 700 {
		t.Fatalf("coding policy is %d bytes, want it to stay under 700", len(body))
	}
	for _, want := range []string{
		"search_definition", "Read a file before editing it", "affected scope",
		"Do not repeat a search",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("coding policy missing %q:\n%s", want, body)
		}
	}
	if section.Digest() != NewCodingPolicySection().Digest() {
		t.Fatal("the digest of a constant section moved")
	}
}
