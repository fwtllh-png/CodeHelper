package prompt

import (
	"context"
	"sync"

	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/repository"
)

// RepositoryOptions configures the volatile repository context appended to a
// model request.
type RepositoryOptions struct {
	RepoMap    bool
	WorkingSet bool
	Evidence   bool
	Map        repository.Options
	Budgets    map[string]Budget
	Tokens     TokenCounter
}

// RepositoryProvider renders the repository map, working set, and evidence
// while caching the expensive map build once per turn.
type RepositoryProvider struct {
	index   repository.Index
	options RepositoryOptions

	mu       sync.Mutex
	mapTurn  uint64
	repoMap  repository.Map
	mapKnown bool
}

func NewRepositoryProvider(
	index repository.Index,
	options RepositoryOptions,
) *RepositoryProvider {
	return &RepositoryProvider{index: index, options: options}
}

// Build renders the volatile context for one sample. An unavailable index is
// represented in the context instead of failing the turn.
func (p *RepositoryProvider) Build(
	ctx context.Context,
	state TurnState,
) TurnContext {
	if p == nil {
		return TurnContext{}
	}
	turnOptions := TurnOptions{
		Turn: state.Turn, Budgets: p.options.Budgets, Tokens: p.options.Tokens,
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
	return AssembleTurn(turnOptions)
}

func (p *RepositoryProvider) mapFor(
	ctx context.Context,
	turn uint64,
	entries []agentcontext.WorkingSetEntry,
) repository.Map {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mapKnown && p.mapTurn == turn {
		return p.repoMap
	}
	focus := make([]string, 0, len(entries))
	for _, entry := range entries {
		focus = append(focus, entry.Path)
	}
	p.repoMap = repository.Build(ctx, p.index, focus, p.options.Map)
	p.mapTurn, p.mapKnown = turn, true
	return p.repoMap
}
