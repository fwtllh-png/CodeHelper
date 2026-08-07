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

// RepoContext renders the volatile tail of a request: what the repository looks
// like, which paths are in play, and what the turn has yet to prove. It is an
// interface so the engine stays unaware of the repository index and keeps working
// without one.
type RepoContext interface {
	Build(ctx context.Context, state promptcontext.TurnState) promptcontext.TurnContext
}

// observePath records that source named path. Paths are stored workspace-relative
// with forward slashes, because that is what the repository index is keyed by and
// what a model can hand straight back to a file tool.
func (e *Engine) observePath(source workingset.Source, path string) {
	if e == nil || e.working == nil {
		return
	}
	if relative, ok := e.workspaceRelative(path); ok {
		e.working.Observe(source, e.turn, relative)
	}
}

func (e *Engine) observePaths(source workingset.Source, paths []string) {
	for _, path := range paths {
		e.observePath(source, path)
	}
}

// workspaceRelative normalizes a path the way the index spells it. A path outside
// the workspace is dropped rather than recorded: the working set exists to point
// at code the agent can act on.
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

// WorkingSetEntries returns the working set as of turn, most relevant first. The
// turn is a parameter rather than the engine's own counter so a caller outside
// the turn loop does not have to read state the loop owns.
func (e *Engine) WorkingSetEntries(turn uint64, limit int) []workingset.Entry {
	if e == nil {
		return nil
	}
	return e.working.Select(turn, limit)
}

// ReadPaths returns the paths turn read. The turn receipt reports them, which is
// why the ledger tracks a turn per source rather than only the latest.
func (e *Engine) ReadPaths(turn uint64) []string {
	if e == nil {
		return nil
	}
	return e.working.PathsObservedAt(workingset.SourceRead, turn)
}

// compactionPaths returns the working set and the critical paths a compaction
// summary should carry, sorted for a stable summary.
func (e *Engine) compactionPaths() ([]string, []string) {
	var paths, critical []string
	for _, entry := range e.working.Select(e.turn, e.options.WorkingSetLimit) {
		paths = append(paths, entry.Path)
		if entry.Critical {
			critical = append(critical, entry.Path)
		}
	}
	sort.Strings(paths)
	sort.Strings(critical)
	return paths, critical
}

// recordTurnContextReceipts keeps the receipts of the latest tail render, so the
// turn receipt and the compaction summary can report what the volatile sections
// cost and whether a budget cut them.
func (e *Engine) recordTurnContextReceipts(receipts []promptcontext.Receipt) {
	for _, receipt := range receipts {
		e.options.Metrics.ContextTail(receipt.RetainedBytes, receipt.Truncated)
	}
	e.turnContextMu.Lock()
	defer e.turnContextMu.Unlock()
	e.turnContextSeen = receipts
}

func (e *Engine) turnContextReceipts() []promptcontext.Receipt {
	if e == nil {
		return nil
	}
	e.turnContextMu.Lock()
	defer e.turnContextMu.Unlock()
	return append([]promptcontext.Receipt(nil), e.turnContextSeen...)
}

// TurnContextReceipts reports the volatile partitions of the last sample.
func (e *Engine) TurnContextReceipts() []promptcontext.Receipt {
	return e.turnContextReceipts()
}

// ContextSelections reports the per-path explanation captured from the same
// prompt render as the latest context receipts.
func (e *Engine) ContextSelections() []promptcontext.Selection {
	if e == nil {
		return nil
	}
	e.turnContextMu.Lock()
	defer e.turnContextMu.Unlock()
	return cloneSelections(e.turnSelections)
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
	e.turnContextMu.Lock()
	if e.catalogSeen == nil {
		e.turnContextMu.Unlock()
		return nil
	}
	snapshot := *e.catalogSeen
	e.turnContextMu.Unlock()
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

// seedWorkingSet pins the files the session started with. They came from the user
// naming them, so they outrank anything the agent later stumbles onto and never
// decay out.
func (e *Engine) seedWorkingSet() {
	if e.working == nil {
		return
	}
	for _, path := range e.options.WorkingSet {
		if relative, ok := e.workspaceRelative(path); ok {
			e.working.Observe(workingset.SourcePinned, 0, relative)
		}
	}
}

// turnContextMessages renders the volatile tail for the current sample.
//
// It is rebuilt on every sample rather than once per turn, so a file the agent
// read a step ago is already listed. Nothing follows it in the request, so the
// churn costs no cached prefix.
func (e *Engine) turnContextMessages(ctx context.Context) ([]provider.Message, []promptcontext.Receipt) {
	catalog, err := e.options.Tools.Snapshot()
	if err != nil {
		return nil, nil
	}
	_, advertised, err := e.toolDefinitionsFromSnapshot(catalog)
	if err != nil {
		return nil, nil
	}
	return e.turnContextMessagesForCatalog(ctx, catalog, advertised)
}

func (e *Engine) turnContextMessagesForCatalog(
	ctx context.Context,
	catalog tool.CatalogSnapshot,
	advertised map[string]bool,
) ([]provider.Message, []promptcontext.Receipt) {
	var messages []provider.Message
	var receipts []promptcontext.Receipt
	catalogMessages, catalogReceipt := promptcontext.AssembleToolCatalog(
		promptcontext.NewToolCatalogSectionFromSnapshot(catalog, advertised),
		e.options.ToolCatalogBudget,
	)
	messages = append(messages, catalogMessages...)
	if catalogReceipt.OriginalBytes > 0 {
		receipts = append(receipts, catalogReceipt)
	}
	catalogCopy := catalog
	e.turnContextMu.Lock()
	e.catalogSeen = &catalogCopy
	e.turnSelections = nil
	e.turnContextMu.Unlock()
	if e.options.RepoContext != nil {
		snapshot := e.evidence.Snapshot(e.options.EvidenceLimit)
		built := e.options.RepoContext.Build(ctx, promptcontext.TurnState{
			Turn:       e.turn,
			WorkingSet: e.working.Select(e.turn, e.options.WorkingSetLimit),
			Evidence:   snapshot,
		})
		e.options.Metrics.Evidence(len(snapshot.Risks), len(snapshot.Reminders))
		messages = append(messages, built.Messages...)
		receipts = append(receipts, built.Receipts...)
		e.turnContextMu.Lock()
		e.turnSelections = cloneSelections(built.Selections)
		e.turnContextMu.Unlock()
	}
	// The plan sits at the very end: it is the most task-specific instruction,
	// and keeping it out of the prefix means updating it no longer invalidates
	// the cached history behind it.
	e.planMu.Lock()
	plan := e.planText
	e.planMu.Unlock()
	if plan != "" {
		messages = append(messages, provider.TextMessage(provider.RoleSystem, plan))
	}
	return messages, receipts
}
