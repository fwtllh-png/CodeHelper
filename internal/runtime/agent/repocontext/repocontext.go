// Package repocontext builds the volatile tail of a request: the repository map
// and the working set, rendered fresh for every sample.
//
// It is the seam between the engine, which knows what the agent touched, and the
// repository index, which knows what the repository holds. Neither has to know
// about the other.
package repocontext

import (
	"context"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/repomap"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
)

// Options configures what the tail contains and how large it may grow.
type Options struct {
	// RepoMap turns the repository map section on. With it off the tail carries
	// only the working set.
	RepoMap bool
	// WorkingSet turns the working set section on.
	WorkingSet bool
	// Evidence turns the evidence section on: what lookups established, what the
	// turn has not proved, and which calls were wasted.
	Evidence bool
	// Map bounds how much of the repository the map describes.
	Map repomap.Options
	// Budgets bounds the rendered sections by partition, as Assemble does.
	Budgets map[string]promptcontext.Budget
	// Tokens counts tokens for the receipts (nil → the heuristic counter).
	Tokens promptcontext.TokenCounter
}

// Provider renders the tail and caches the expensive half of it.
type Provider struct {
	index   repomap.Index
	options Options

	// mu guards the per-turn repository map. Refreshing the index stats the whole
	// workspace, which is worth doing once a turn and not once a sample; the
	// working set, being in memory, is re-rendered every time.
	mu       sync.Mutex
	mapTurn  uint64
	repoMap  repomap.Map
	mapKnown bool
}

// New returns a provider over index, which may be nil when no index is available.
func New(index repomap.Index, options Options) *Provider {
	return &Provider{index: index, options: options}
}

// Build renders the tail for one sample. It never fails: an index that cannot
// answer becomes a section that says so.
func (p *Provider) Build(
	ctx context.Context, state promptcontext.TurnState,
) promptcontext.TurnContext {
	if p == nil {
		return promptcontext.TurnContext{}
	}
	turnOptions := promptcontext.TurnOptions{
		Turn: state.Turn, Budgets: p.options.Budgets, Tokens: p.options.Tokens,
		PreviousReceipts: state.PreviousReceipts,
	}
	if p.options.WorkingSet {
		turnOptions.WorkingSet = state.WorkingSet
	}
	if p.options.Evidence {
		turnOptions.Evidence = state.Evidence
	}
	if p.options.RepoMap {
		turnOptions.RepoMap = p.mapFor(ctx, state.Turn, state.WorkingSet)
	}
	return promptcontext.AssembleTurn(turnOptions)
}

// mapFor returns the map for turn, building it at most once per turn.
//
// A file first read this turn therefore reaches the working set section
// immediately but its outline only next turn. That is the price of not walking
// the workspace once per sample.
func (p *Provider) mapFor(ctx context.Context, turn uint64, entries []workingset.Entry) repomap.Map {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mapKnown && p.mapTurn == turn {
		return p.repoMap
	}
	focus := make([]string, 0, len(entries))
	for _, entry := range entries {
		focus = append(focus, entry.Path)
	}
	p.repoMap = repomap.Build(ctx, p.index, focus, p.options.Map)
	p.mapTurn, p.mapKnown = turn, true
	return p.repoMap
}
