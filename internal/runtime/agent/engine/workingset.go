package engine

import (
	"context"
	"maps"
	"sort"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
	promptcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/prompt"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// RepoContext renders the repository state visible to a sample.
type RepoContext interface {
	Build(ctx context.Context, state promptcontext.TurnState) promptcontext.TurnContext
}

func (e *Engine) WorkingSetEntries(turn uint64, limit int) []agentcontext.WorkingSetEntry {
	if e == nil {
		return nil
	}
	return e.workingLedger().Select(turn, limit)
}

func (e *Engine) ReadPaths(turn uint64) []string {
	if e == nil {
		return nil
	}
	return e.workingLedger().PathsObservedAt(agentcontext.SourceRead, turn)
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
	scope.mu.Lock()
	snapshot := scope.state.sampledCatalog
	advertised := make(map[string]bool, len(scope.state.sampledTools))
	maps.Copy(advertised, scope.state.sampledTools)
	scope.mu.Unlock()
	if snapshot.CatalogID == "" {
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
		e.contextAuthority().ObservePath(
			e.options.Workspace,
			agentcontext.SourcePinned,
			0,
			path,
		)
	}
}
