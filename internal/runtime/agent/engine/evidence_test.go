package engine

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/compact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/evidence"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
)

func evidenceEngine(t *testing.T) *Engine {
	t.Helper()
	engine := newEngine(t, &scriptedProvider{}, nil)
	engine.options.Workspace = t.TempDir()
	engine.turn = 1
	engine.evidence.BeginTurn(1)
	return engine
}

func TestSearchHitsBecomeFactsAndTheWeakestWorkingSetSource(t *testing.T) {
	engine := evidenceEngine(t)
	call := provider.ToolCall{Name: "search_definition", Arguments: `{"name":"Verify"}`}
	engine.observeEvidence(call, tool.Result{Metadata: map[string]any{
		tool.MetadataEvidence: []tool.EvidenceHit{
			{Kind: tool.EvidenceDefinition, Path: "auth/token.go", Line: 12, Symbol: "Verify"},
			// A path outside the workspace is dropped: the ledger points at code the
			// agent can act on.
			{Kind: tool.EvidenceReference, Path: filepath.Join(engine.options.Workspace, "..", "x.go")},
		},
	}})

	facts := engine.EvidenceSnapshot().Facts
	if len(facts) != 1 {
		t.Fatalf("facts = %+v", facts)
	}
	if facts[0].Kind != evidence.KindDefinition || facts[0].Path != "auth/token.go" ||
		facts[0].Line != 12 || facts[0].Symbol != "Verify" ||
		facts[0].Tool != "search_definition" || facts[0].Turn != 1 {
		t.Fatalf("fact = %+v", facts[0])
	}
	entries := engine.WorkingSetEntries(1, 10)
	if len(entries) != 1 || entries[0].Path != "auth/token.go" ||
		entries[0].Sources[0] != workingset.SourceSearch {
		t.Fatalf("entries = %+v, want the hit recorded as a search", entries)
	}
}

func TestUnknownEvidenceKindIsIgnored(t *testing.T) {
	engine := evidenceEngine(t)
	engine.observeEvidence(
		provider.ToolCall{Name: "search_text"},
		tool.Result{Metadata: map[string]any{
			tool.MetadataEvidence: []tool.EvidenceHit{{Kind: "guess", Path: "a.go"}},
		}},
	)
	if facts := engine.EvidenceSnapshot().Facts; len(facts) != 0 {
		t.Fatalf("facts = %+v", facts)
	}
}

func TestAnEditAfterAReadIsNotBlind(t *testing.T) {
	engine := evidenceEngine(t)
	read := filepath.Join(engine.options.Workspace, "a.go")
	engine.observePath(workingset.SourceRead, read)
	engine.observeChangeEvidence(toolguard.FileChange{Path: read, Kind: toolguard.FileModified})
	engine.observeChangeEvidence(toolguard.FileChange{Path: "b.go", Kind: toolguard.FileModified})
	engine.observeChangeEvidence(toolguard.FileChange{Path: "new.go", Kind: toolguard.FileCreated})

	blind := map[string]bool{}
	for _, risk := range engine.EvidenceSnapshot().Risks {
		if risk.Kind == evidence.RiskBlindChange {
			blind[risk.Path] = true
		}
	}
	if len(blind) != 1 || !blind["b.go"] {
		t.Fatalf("blind changes = %v, want only the file nobody read", blind)
	}
}

func TestDiagnosticsCloseAndOpenTheEvidenceGap(t *testing.T) {
	engine := evidenceEngine(t)
	engine.observeChangeEvidence(toolguard.FileChange{Path: "a.go", Kind: toolguard.FileModified})
	engine.observeDiagnosticsEvidence([]diagnostics.Receipt{{
		Path: "a.go", Status: "failed",
		Diagnostics: []diagnostics.Diagnostic{{Path: "a.go", Message: "broken"}},
	}})
	if !hasRisk(engine, evidence.RiskOpenDiagnostics) {
		t.Fatal("a failing check left no risk")
	}
	// An unavailable runner checked nothing, so it must not read as clean.
	engine.observeDiagnosticsEvidence([]diagnostics.Receipt{{Path: "a.go", Status: "unavailable"}})
	if !hasRisk(engine, evidence.RiskOpenDiagnostics) {
		t.Fatal("an unavailable runner cleared the risk")
	}
	engine.observeDiagnosticsEvidence([]diagnostics.Receipt{{Path: "a.go", Status: "passed"}})
	if hasRisk(engine, evidence.RiskOpenDiagnostics) {
		t.Fatal("a clean check left the risk standing")
	}
}

func TestVerifiedPathsClearTheRiskWorkspaceRelative(t *testing.T) {
	engine := evidenceEngine(t)
	absolute := filepath.Join(engine.options.Workspace, "a.go")
	engine.observeChangeEvidence(toolguard.FileChange{Path: absolute, Kind: toolguard.FileModified})
	if !hasRisk(engine, evidence.RiskUnverifiedChange) {
		t.Fatal("a fresh change is not unverified")
	}
	// The gate reports the paths the way the guard spelled them, absolute; the
	// evidence set keys on workspace-relative paths, so the two must be lined up.
	engine.observeVerifiedEvidence([]string{absolute})
	if hasRisk(engine, evidence.RiskUnverifiedChange) {
		t.Fatal("verification did not clear the risk")
	}
}

func TestRepeatedCallAndConsumedHandleAreObservedFromCalls(t *testing.T) {
	engine := evidenceEngine(t)
	engine.noteToolCall(provider.ToolCall{Name: "search_text", Arguments: `{"query":"a"}`})
	engine.noteToolCall(provider.ToolCall{Name: "search_text", Arguments: `{"query":"a"}`})
	reminders := engine.EvidenceSnapshot().Reminders
	if len(reminders) != 1 || reminders[0].Kind != evidence.ReminderRepeatedCall {
		t.Fatalf("reminders = %+v", reminders)
	}

	engine.observeEvidence(
		provider.ToolCall{Name: "search_project"},
		tool.Result{Metadata: map[string]any{"handle": "h1"}},
	)
	engine.turn = 2
	engine.evidence.BeginTurn(2)
	if !hasReminder(engine, evidence.ReminderUnconsumedResult) {
		t.Fatal("an unread handle from the previous turn is not reported")
	}
	engine.noteToolCall(provider.ToolCall{Name: "result_get", Arguments: `{"handle":"h1"}`})
	if hasReminder(engine, evidence.ReminderUnconsumedResult) {
		t.Fatal("reading the handle did not clear the reminder")
	}
}

func TestRereadingAnUnchangedFileReminds(t *testing.T) {
	engine := evidenceEngine(t)
	path := filepath.Join(engine.options.Workspace, "a.go")
	result := tool.Result{Metadata: map[string]any{
		toolguard.MetadataCanonicalPath: path, "content_sha256": "sha-1",
	}}
	call := provider.ToolCall{Name: "file_read"}
	engine.observeEvidence(call, result)
	if hasReminder(engine, evidence.ReminderRepeatedRead) {
		t.Fatal("a first read reminded")
	}
	engine.observeEvidence(call, result)
	if !hasReminder(engine, evidence.ReminderRepeatedRead) {
		t.Fatal("re-reading unchanged content did not remind")
	}
}

func TestForkInheritsTheEvidenceWithoutSharingIt(t *testing.T) {
	parent := evidenceEngine(t)
	parent.observeChangeEvidence(toolguard.FileChange{Path: "a.go", Kind: toolguard.FileModified})
	child := parent.Fork()
	parent.observeVerifiedEvidence([]string{"a.go"})

	if !hasRisk(child, evidence.RiskUnverifiedChange) {
		t.Fatal("the fork lost the inherited risk or shares the parent's verification")
	}
}

// Compaction is exactly when the model loses the history that said an edit
// happened, so the summary has to say which edits are still unproved.
func TestCompactionSummaryCarriesUnverifiedChanges(t *testing.T) {
	engine := evidenceEngine(t)
	engine.observeChangeEvidence(toolguard.FileChange{Path: "a.go", Kind: toolguard.FileModified})
	engine.observeChangeEvidence(toolguard.FileChange{Path: "b.go", Kind: toolguard.FileModified})
	engine.observeVerifiedEvidence([]string{"b.go"})

	rendered, _, sections := engine.buildCompactSummary(nil).Render(0)
	if !strings.Contains(rendered, "a.go (turn 1) — nothing verified it") {
		t.Fatalf("summary = %q, want the unproved change to survive compaction", rendered)
	}
	if !strings.Contains(rendered, "b.go (turn 1) — verified") {
		t.Fatalf("summary = %q, want the verified change reported as verified", rendered)
	}
	if !slices.Contains(sections, compact.SectionChanges) {
		t.Fatalf("sections = %v", sections)
	}
}

func TestTailRenderCountsRisksAndReminders(t *testing.T) {
	engine := evidenceEngine(t)
	metrics := telemetry.NewMetrics()
	engine.options.Metrics = metrics
	engine.options.RepoContext = &stubRepoContext{}
	engine.observeChangeEvidence(toolguard.FileChange{Path: "a.go", Kind: toolguard.FileModified})
	engine.noteToolCall(provider.ToolCall{Name: "search_text", Arguments: `{"query":"a"}`})
	engine.noteToolCall(provider.ToolCall{Name: "search_text", Arguments: `{"query":"a"}`})

	engine.turnContextMessages(t.Context())
	snapshot := metrics.Snapshot()
	// Two risks: the change is both unverified and unread.
	if snapshot.EvidenceRisks != 2 || snapshot.PolicyReminders != 1 {
		t.Fatalf("metrics = %+v", snapshot)
	}
}

func hasRisk(engine *Engine, kind string) bool {
	for _, risk := range engine.EvidenceSnapshot().Risks {
		if risk.Kind == kind {
			return true
		}
	}
	return false
}

func hasReminder(engine *Engine, kind string) bool {
	for _, reminder := range engine.EvidenceSnapshot().Reminders {
		if reminder.Kind == kind {
			return true
		}
	}
	return false
}
