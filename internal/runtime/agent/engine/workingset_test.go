package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/evidence"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
)

func planFixture() interact.Plan {
	return interact.Plan{
		Steps:         []interact.PlanStep{{Title: "step one", Status: interact.StepPending}},
		CriticalFiles: []string{"design.md"},
	}
}

// stubRepoContext renders one line per working-set entry, which is enough to see
// what the engine handed it and when.
type stubRepoContext struct {
	turns    []uint64
	entries  [][]workingset.Entry
	evidence []evidence.Snapshot
	receipts []promptcontext.Receipt
}

func (e *Engine) turnContextMessages(
	ctx context.Context,
) ([]provider.Message, []promptcontext.Receipt) {
	catalog, err := e.options.Tools.Snapshot()
	if err != nil {
		return nil, nil
	}
	_, advertised, err := e.toolDefinitionsFromSnapshot(catalog)
	if err != nil {
		return nil, nil
	}
	frozen, catalogReceipt := e.toolCatalogContext(catalog, advertised)
	tail, receipts := e.turnContextMessagesForCatalog(ctx)
	if catalogReceipt.OriginalBytes > 0 {
		receipts = append([]promptcontext.Receipt{catalogReceipt}, receipts...)
	}
	return append(frozen, tail...), receipts
}

func (s *stubRepoContext) Build(
	_ context.Context, state promptcontext.TurnState,
) promptcontext.TurnContext {
	s.turns = append(s.turns, state.Turn)
	s.entries = append(s.entries, state.WorkingSet)
	s.evidence = append(s.evidence, state.Evidence)
	paths := make([]string, 0, len(state.WorkingSet))
	selections := make([]promptcontext.Selection, 0, len(state.WorkingSet))
	for _, entry := range state.WorkingSet {
		paths = append(paths, entry.Path)
		selections = append(selections, promptcontext.Selection{
			Path: entry.Path, Kind: "file", Reasons: []string{"read"}, Included: true,
		})
	}
	return promptcontext.TurnContext{
		Messages: []provider.Message{provider.TextMessage(
			provider.RoleSystem, "[working_set] "+strings.Join(paths, " "),
		)},
		Receipts: s.receipts, Selections: selections,
	}
}

func TestObservedPathsAreStoredWorkspaceRelative(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	engine.options.Workspace = t.TempDir()
	engine.turn = 3

	engine.observePath(workingset.SourceEdited, filepath.Join(engine.options.Workspace, "internal", "a.go"))
	engine.observePath(workingset.SourceRead, filepath.Join("internal", "b.go"))
	engine.observePath(workingset.SourceRead, filepath.Join(engine.options.Workspace, "..", "outside.go"))
	engine.observePath(workingset.SourceRead, engine.options.Workspace)
	engine.observePath(workingset.SourceRead, "")

	entries := engine.WorkingSetEntries(3, 10)
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want only the two paths inside the workspace", entries)
	}
	if entries[0].Path != "internal/a.go" || entries[1].Path != "internal/b.go" {
		t.Fatalf("paths = %+v, want workspace-relative slash paths", entries)
	}
}

func TestReadObservationsFollowTheGuardMetadataKey(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	engine.options.Workspace = t.TempDir()
	engine.turn = 1

	// The engine learns about reads from exactly this metadata key. If the guard
	// stops writing it, the working set silently empties, so pin it here.
	path := filepath.Join(engine.options.Workspace, "read.go")
	engine.observePath(workingset.SourceRead, observedFileRead(map[string]any{
		toolguard.MetadataCanonicalPath: path,
	}))
	if paths := engine.ReadPaths(1); len(paths) != 1 || paths[0] != "read.go" {
		t.Fatalf("read paths = %v", paths)
	}
	if observedFileRead(nil) != "" || observedFileRead(map[string]any{"other": 1}) != "" {
		t.Fatal("a result without the key must report no read")
	}
}

func TestDiagnosticsAndPlanFeedTheWorkingSet(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	engine.options.Workspace = t.TempDir()
	engine.turn = 2

	engine.recordTurnDiagnostics([]diagnostics.Receipt{{Path: "broken.go", Status: "failed"}})
	engine.observePaths(workingset.SourcePlan, []string{"design.md"})

	entries := engine.WorkingSetEntries(2, 10)
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Path != "design.md" || !entries[0].Critical {
		t.Fatalf("entries = %+v, want the plan's critical file first", entries)
	}
	if entries[1].Path != "broken.go" || entries[1].Sources[0] != workingset.SourceDiagnostic {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestTheWorkingSetOutlivesTheTurnAndTheTurnDiffDoesNot(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	scope := attachTestScope(t, engine)
	engine.options.Workspace = t.TempDir()

	engine.turn = 1
	scope.state.diff.Record(TurnDiffEntry{Path: "a.go", Kind: "modified"})
	engine.observePath(workingset.SourceEdited, "a.go")

	engine.turn = 2
	scope.state.diff.Reset()
	if len(engine.TurnDiff()) != 0 {
		t.Fatal("the turn diff must not survive its turn")
	}
	entries := engine.WorkingSetEntries(2, 10)
	if len(entries) != 1 || entries[0].Path != "a.go" || entries[0].LastTurn != 1 {
		t.Fatalf("entries = %+v, want the previous turn's edit still known", entries)
	}
}

func TestForkInheritsTheWorkingSetWithoutSharingIt(t *testing.T) {
	parent := newEngine(t, &scriptedProvider{}, nil)
	parent.options.Workspace = t.TempDir()
	parent.turn = 1
	parent.observePath(workingset.SourceEdited, "shared.go")

	child := parent.Fork()
	child.turn = 2
	child.observePath(workingset.SourceRead, "child.go")
	parent.observePath(workingset.SourceRead, "parent.go")

	if got := len(child.WorkingSetEntries(2, 10)); got != 2 {
		t.Fatalf("child entries = %d, want the inherited edit plus its own read", got)
	}
	for _, entry := range child.WorkingSetEntries(2, 10) {
		if entry.Path == "parent.go" {
			t.Fatal("the fork must not see what the parent did afterwards")
		}
	}
}

func TestTurnContextIsAppendedAfterHistoryAndNeverStored(t *testing.T) {
	stub := &stubRepoContext{}
	engine := newEngine(t, &scriptedProvider{}, nil)
	engine.options.Workspace = t.TempDir()
	engine.options.RepoContext = stub
	engine.options.PromptContext = []provider.Message{
		provider.TextMessage(provider.RoleSystem, "stable prefix"),
	}
	engine.turn = 4
	engine.observePath(workingset.SourceEdited, "a.go")
	engine.ApplyPlan(planFixture())

	history := []provider.Message{messageWithText(provider.RoleUser, "do it", 4)}
	messages := append(engine.promptMessages(), cloneMessages(history)...)
	tail, _ := engine.turnContextMessages(t.Context())
	messages = append(messages, tail...)

	if len(messages) != 4 {
		t.Fatalf("messages = %+v", messages)
	}
	if messages[0].Text() != "stable prefix" || messages[1].Role != provider.RoleUser {
		t.Fatalf("prefix = %+v, want the frozen context then the history", messages[:2])
	}
	if !strings.Contains(messages[2].Text(), "a.go") {
		t.Fatalf("turn context = %q", messages[2].Text())
	}
	// The plan is last: it is the most specific instruction, and keeping it out of
	// the prefix means updating it does not invalidate the cached history.
	if !strings.Contains(messages[3].Text(), "step one") {
		t.Fatalf("plan = %q", messages[3].Text())
	}
	for _, message := range messages[2:] {
		if message.Role != provider.RoleSystem {
			t.Fatalf("tail role = %q, want system", message.Role)
		}
	}
	// Nothing the tail added may leak into what the next turn replays.
	if len(engine.history) != 0 {
		t.Fatalf("history = %+v, want the tail to stay request-local", engine.history)
	}
}

func TestFrozenToolCatalogPrecedesChangingHistory(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		toolCallStream("echo-1", "echo", `{"text":"one"}`),
		textStream("done"),
	}}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&echoTool{}, nil); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, runtime, registry)
	if _, err := engine.Run(t.Context(), "inspect", nil); err != nil {
		t.Fatal(err)
	}
	if len(runtime.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(runtime.requests))
	}
	for _, request := range runtime.requests {
		catalog, user := -1, -1
		for index, message := range request.Messages {
			if strings.Contains(message.Text(), "[tool_catalog ") {
				catalog = index
			}
			if message.Role == provider.RoleUser && message.Text() == "inspect" {
				user = index
			}
		}
		if catalog < 0 || user < 0 || catalog >= user {
			t.Fatalf("catalog/user indexes = %d/%d, messages=%+v", catalog, user, request.Messages)
		}
	}
}

func TestFrozenToolCatalogSnapshotIsReused(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&echoTool{}, nil); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, &scriptedProvider{}, registry)
	attachTestScope(t, engine)
	catalog, err := registry.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	_, advertised, err := engine.toolDefinitionsFromSnapshot(catalog)
	if err != nil {
		t.Fatal(err)
	}
	first, receipt := engine.toolCatalogContext(catalog, advertised)
	first[0].Blocks[0].Text = "mutated caller copy"
	second, cached := engine.toolCatalogContext(catalog, advertised)
	if second[0].Text() == "mutated caller copy" ||
		cached.Digest != receipt.Digest {
		t.Fatalf("cached catalog = %+v receipt=%+v", second, cached)
	}
}

func TestTurnContextRebuildsWithinTheSameTurn(t *testing.T) {
	stub := &stubRepoContext{}
	engine := newEngine(t, &scriptedProvider{}, nil)
	engine.options.Workspace = t.TempDir()
	engine.options.RepoContext = stub
	engine.turn = 1

	if _, _ = engine.turnContextMessages(t.Context()); len(stub.entries[0]) != 0 {
		t.Fatalf("first sample entries = %+v, want none yet", stub.entries[0])
	}
	// A file read during the turn is in the working set for the very next sample,
	// which is what makes the tail worth rebuilding instead of caching per turn.
	engine.observePath(workingset.SourceRead, "found.go")
	messages, _ := engine.turnContextMessages(t.Context())
	if len(stub.entries) != 2 || len(stub.entries[1]) != 1 {
		t.Fatalf("second sample entries = %+v", stub.entries)
	}
	if !strings.Contains(messages[0].Text(), "found.go") {
		t.Fatalf("second sample text = %q", messages[0].Text())
	}
	if stub.turns[0] != 1 || stub.turns[1] != 1 {
		t.Fatalf("turns = %v, want both samples in turn 1", stub.turns)
	}
}

func TestTurnContextReceiptsJoinTheContextReceipts(t *testing.T) {
	stub := &stubRepoContext{receipts: []promptcontext.Receipt{{
		Kind: promptcontext.PartitionRepoMap, RetainedBytes: 12,
		Truncated: true, TruncationReason: "byte_budget",
	}}}
	engine := newEngine(t, &scriptedProvider{}, nil)
	engine.options.RepoContext = stub
	engine.options.ContextReceipts = []promptcontext.Receipt{
		{Kind: promptcontext.PartitionBase, RetainedBytes: 3},
	}

	_, receipts := engine.turnContextMessages(t.Context())
	engine.recordTurnContextReceipts(receipts)

	kinds := map[string]bool{}
	for _, receipt := range engine.ContextReceipts() {
		kinds[receipt.Kind] = true
	}
	if !kinds[promptcontext.PartitionBase] || !kinds[promptcontext.PartitionRepoMap] {
		t.Fatalf("receipt kinds = %v, want both the frozen and the volatile ones", kinds)
	}
	// Re-rendering replaces rather than appends, or a long turn would grow a
	// receipt list one entry per sample.
	engine.recordTurnContextReceipts(receipts)
	count := 0
	for _, receipt := range engine.ContextReceipts() {
		if receipt.Kind == promptcontext.PartitionRepoMap {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("repo map receipts = %d, want one", count)
	}
	engine.observePath(workingset.SourceRead, "internal/value.go")
	_, _ = engine.turnContextMessages(t.Context())
	selections := engine.ContextSelections()
	if len(selections) != 1 || selections[0].Path != "internal/value.go" ||
		selections[0].Reasons[0] != "read" {
		t.Fatalf("context selections = %+v", selections)
	}
}

func TestTurnContextIsOptional(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	messages, receipts := engine.turnContextMessages(t.Context())
	if len(messages) != 0 || len(receipts) != 0 {
		t.Fatalf("messages = %+v, receipts = %+v, want nothing without a provider", messages, receipts)
	}
}
