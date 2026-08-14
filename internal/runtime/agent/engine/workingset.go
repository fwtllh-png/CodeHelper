package engine

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// RepoContext renders the repository state visible to a sample.
type RepoContext interface {
	Build(ctx context.Context, state promptcontext.TurnState) promptcontext.TurnContext
}

func (e *Engine) observePath(source workingset.Source, path string) {
	if e == nil || e.workingLedger() == nil {
		return
	}
	if relative, ok := e.workspaceRelative(path); ok {
		e.workingLedger().Observe(source, e.turn, relative)
	}
}

func (e *Engine) observePaths(source workingset.Source, paths []string) {
	for _, path := range paths {
		e.observePath(source, path)
	}
}

func (e *Engine) workspaceRelative(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" || path == "." {
		return "", false
	}
	if !filepath.IsAbs(path) {
		return filepath.ToSlash(filepath.Clean(path)), true
	}
	workspace := e.options.Workspace
	if workspace == "" {
		return "", false
	}
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return "", false
	}
	// A workspace reached through a symlink (/var on macOS) is spelled one way in
	// options and another in the fingerprints the guard reports, so both count.
	roots := []string{filepath.Clean(absolute)}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil && resolved != roots[0] {
		roots = append(roots, resolved)
	}
	clean := filepath.Clean(path)
	for _, root := range roots {
		relative, err := filepath.Rel(root, clean)
		if err != nil || relative == ".." ||
			strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		if relative == "." {
			return "", false
		}
		return filepath.ToSlash(relative), true
	}
	return "", false
}

func (e *Engine) WorkingSetEntries(turn uint64, limit int) []workingset.Entry {
	if e == nil {
		return nil
	}
	return e.workingLedger().Select(turn, limit)
}

func (e *Engine) ReadPaths(turn uint64) []string {
	if e == nil {
		return nil
	}
	return e.workingLedger().PathsObservedAt(workingset.SourceRead, turn)
}

func (e *Engine) compactionPaths() ([]string, []string) {
	var paths, critical []string
	for _, entry := range e.workingLedger().Select(e.turn, e.options.WorkingSetLimit) {
		paths = append(paths, entry.Path)
		if entry.Critical {
			critical = append(critical, entry.Path)
		}
	}
	sort.Strings(paths)
	sort.Strings(critical)
	return paths, critical
}

func (e *Engine) recordTurnContextReceipts(receipts []promptcontext.Receipt) {
	scope := e.executionScope()
	if scope == nil {
		return
	}
	scope.mu.Lock()
	scope.state.contextSeen = receipts
	scope.mu.Unlock()
}

// TurnContextReceipts reports the partitions visible to the last sample.
func (e *Engine) TurnContextReceipts() []promptcontext.Receipt {
	if e == nil {
		return nil
	}
	scope := e.currentScope()
	if scope == nil {
		return nil
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	return append([]promptcontext.Receipt(nil), scope.state.contextSeen...)
}

func (e *Engine) ContextSelections() []promptcontext.Selection {
	if e == nil {
		return nil
	}
	scope := e.currentScope()
	if scope == nil {
		return nil
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	return cloneSelections(scope.state.selections)
}

func cloneSelections(input []promptcontext.Selection) []promptcontext.Selection {
	cloned := make([]promptcontext.Selection, len(input))
	for index, selection := range input {
		cloned[index] = selection
		cloned[index].Reasons = append([]string(nil), selection.Reasons...)
		cloned[index].Evidence = append(
			[]promptcontext.SelectionEvidence(nil), selection.Evidence...,
		)
	}
	return cloned
}

func (e *Engine) CatalogReceipt() *protocol.ReceiptCatalog {
	if e == nil {
		return nil
	}
	scope := e.currentScope()
	if scope == nil {
		return nil
	}
	snapshot := scope.spec.Catalog
	_, advertised, err := e.toolDefinitionsFromSnapshot(snapshot)
	if err != nil {
		return nil
	}
	receipt := &protocol.ReceiptCatalog{
		CatalogID: snapshot.CatalogID, Generation: snapshot.Generation,
		Digest: snapshot.Digest,
	}
	for _, entry := range snapshot.Entries() {
		if advertised[entry.Name] {
			receipt.Advertised = append(receipt.Advertised, entry.Name)
		} else if entry.Descriptor.Visibility == tool.VisibleModel &&
			entry.Descriptor.Availability != tool.AvailabilityUnavailable {
			receipt.OmittedCount++
		}
		if entry.State == tool.CatalogEntryMaterialized {
			receipt.Materialized = append(receipt.Materialized, entry.Name)
		}
		if entry.State == tool.CatalogEntryDeferred {
			receipt.DeferredCount++
		}
	}
	return receipt
}

func (e *Engine) seedWorkingSet() {
	if e.workingLedger() == nil {
		return
	}
	for _, path := range e.options.WorkingSet {
		if relative, ok := e.workspaceRelative(path); ok {
			e.workingLedger().Observe(workingset.SourcePinned, 0, relative)
		}
	}
}

func (e *Engine) toolCatalogContext(
	catalog tool.CatalogSnapshot,
	advertised map[string]bool,
) ([]provider.Message, promptcontext.Receipt) {
	scope := e.executionScope()
	if scope == nil {
		return promptcontext.AssembleToolCatalog(
			promptcontext.NewToolCatalogSectionFromSnapshot(catalog, advertised),
			e.options.ToolCatalogBudget,
		)
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.state.catalogReceipt.Kind == "" {
		messages, receipt := promptcontext.AssembleToolCatalog(
			promptcontext.NewToolCatalogSectionFromSnapshot(catalog, advertised),
			e.options.ToolCatalogBudget,
		)
		scope.state.catalogContext = cloneMessages(messages)
		scope.state.catalogReceipt = receipt
	}
	return cloneMessages(scope.state.catalogContext), scope.state.catalogReceipt
}

func (e *Engine) worldStateContext(
	ctx context.Context,
) ([]provider.Message, []provider.Message, []promptcontext.Receipt) {
	scope := e.executionScope()
	var previous []promptcontext.Receipt
	ready := false
	if scope != nil {
		scope.mu.Lock()
		previous = append(previous, scope.state.contextSeen...)
		ready = scope.state.worldContext != nil
		scope.mu.Unlock()
	}
	var built promptcontext.TurnContext
	if e.options.RepoContext != nil {
		snapshot := e.evidenceSet().Snapshot(e.options.EvidenceLimit)
		built = e.options.RepoContext.Build(ctx, promptcontext.TurnState{
			Turn:             e.turn,
			WorkingSet:       e.workingLedger().Select(e.turn, e.options.WorkingSetLimit),
			Evidence:         snapshot,
			PreviousReceipts: previous,
		})
		e.options.Metrics.Evidence(len(snapshot.Risks), len(snapshot.Reminders))
	}
	e.planMu.Lock()
	plan := e.planText
	var planReceipt *promptcontext.Receipt
	if e.planReceipt != nil {
		copy := *e.planReceipt
		planReceipt = &copy
	}
	e.planMu.Unlock()
	if planReceipt != nil {
		if promptcontext.SectionDigestMap(previous)[promptcontext.PartitionPlan] ==
			planReceipt.Digest {
			planReceipt.RetainedBytes, planReceipt.RetainedTokens = 0, 0
		} else {
			built.Messages = append(
				built.Messages,
				provider.TextMessage(provider.RoleSystem, plan),
			)
		}
		built.Receipts = append(built.Receipts, *planReceipt)
	}
	for index := range built.Messages {
		built.Messages[index].Turn = e.turn
	}
	if scope == nil {
		return built.Messages, nil, built.Receipts
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	scope.state.selections = cloneSelections(built.Selections)
	scope.state.contextSeen = append([]promptcontext.Receipt(nil), built.Receipts...)
	if !ready {
		scope.state.worldContext = cloneMessages(built.Messages)
		return cloneMessages(scope.state.worldContext), nil, built.Receipts
	}
	return cloneMessages(scope.state.worldContext), built.Messages, built.Receipts
}
