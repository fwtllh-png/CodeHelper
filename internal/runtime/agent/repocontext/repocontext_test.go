package repocontext

import (
	"context"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/persist/repoindex"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/evidence"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
)

type countingIndex struct {
	files int
}

func (c *countingIndex) Files(context.Context) (map[string]repoindex.File, repoindex.Snapshot, error) {
	c.files++
	return map[string]repoindex.File{
		"internal/store/store.go": {Path: "internal/store/store.go", Language: "go", SymbolCount: 3},
	}, repoindex.Snapshot{Status: repoindex.StatusReady}, nil
}

func (c *countingIndex) Symbols(context.Context, repoindex.Query) ([]repoindex.Symbol, repoindex.Snapshot, error) {
	return nil, repoindex.Snapshot{Status: repoindex.StatusReady}, nil
}

func entries(paths ...string) []workingset.Entry {
	result := make([]workingset.Entry, 0, len(paths))
	for _, path := range paths {
		result = append(result, workingset.Entry{
			Path: path, Sources: []workingset.Source{workingset.SourceRead}, LastTurn: 1,
		})
	}
	return result
}

func state(turn uint64, paths ...string) promptcontext.TurnState {
	return promptcontext.TurnState{Turn: turn, WorkingSet: entries(paths...)}
}

func text(assembled promptcontext.TurnContext) string {
	var b strings.Builder
	for _, message := range assembled.Messages {
		b.WriteString(message.Text())
	}
	return b.String()
}

func TestBuildWalksTheIndexOncePerTurnAndRerendersPerSample(t *testing.T) {
	index := &countingIndex{}
	provider := New(index, Options{RepoMap: true, WorkingSet: true})

	first := provider.Build(context.Background(), state(1, "a.go"))
	second := provider.Build(context.Background(), state(1, "a.go", "b.go"))
	if index.files != 1 {
		t.Fatalf("index reads = %d, want one per turn", index.files)
	}
	if strings.Contains(text(first), "b.go") || !strings.Contains(text(second), "b.go") {
		t.Fatal("the working set must follow the sample, not the cached map")
	}

	provider.Build(context.Background(), state(2, "a.go"))
	if index.files != 2 {
		t.Fatalf("index reads = %d, want a fresh read for the new turn", index.files)
	}
}

func TestBuildHonorsDisabledSections(t *testing.T) {
	index := &countingIndex{}
	mapOnly := New(index, Options{RepoMap: true})
	assembled := mapOnly.Build(context.Background(), state(1, "a.go"))
	if body := text(assembled); !strings.Contains(body, "repo_map") || strings.Contains(body, "working_set") {
		t.Fatalf("map-only tail = %q", body)
	}

	setOnly := New(index, Options{WorkingSet: true})
	assembled = setOnly.Build(context.Background(), state(1, "a.go"))
	if body := text(assembled); strings.Contains(body, "repo_map") || !strings.Contains(body, "working_set") {
		t.Fatalf("set-only tail = %q", body)
	}
	if index.files != 1 {
		t.Fatalf("index reads = %d, want none for a disabled map", index.files-1)
	}

	off := New(index, Options{})
	if assembled = off.Build(context.Background(), state(1, "a.go")); len(assembled.Messages) != 0 {
		t.Fatalf("tail with both sections off = %+v", assembled.Messages)
	}
}

func TestBuildCarriesEvidenceOnlyWhenAskedTo(t *testing.T) {
	snapshot := evidence.Snapshot{
		Turn:  1,
		Risks: []evidence.Risk{{Kind: evidence.RiskUnverifiedChange, Path: "a.go", Turn: 1}},
	}
	withEvidence := New(nil, Options{Evidence: true})
	assembled := withEvidence.Build(context.Background(), promptcontext.TurnState{
		Turn: 1, Evidence: snapshot,
	})
	if !strings.Contains(text(assembled), "[evidence turn=1]") {
		t.Fatalf("tail = %q", text(assembled))
	}

	// The evidence section is independent of the index: it describes the thread,
	// not the repository, so it survives a session with no index at all.
	off := New(nil, Options{WorkingSet: true})
	assembled = off.Build(context.Background(), promptcontext.TurnState{
		Turn: 1, WorkingSet: entries("a.go"), Evidence: snapshot,
	})
	if strings.Contains(text(assembled), "evidence") {
		t.Fatalf("a disabled section was rendered: %q", text(assembled))
	}
}

func TestBuildDegradesWithoutAnIndex(t *testing.T) {
	provider := New(nil, Options{RepoMap: true, WorkingSet: true})
	assembled := provider.Build(context.Background(), state(4, "a.go"))
	body := text(assembled)
	if !strings.Contains(body, "No repository map is available") {
		t.Fatalf("tail = %q", body)
	}
	// The working set does not depend on the index, so it survives.
	if !strings.Contains(body, "a.go") {
		t.Fatalf("tail = %q", body)
	}
}

func TestNilProviderBuildsNothing(t *testing.T) {
	var provider *Provider
	if assembled := provider.Build(context.Background(), state(1)); len(assembled.Messages) != 0 {
		t.Fatalf("assembled = %+v", assembled)
	}
}
