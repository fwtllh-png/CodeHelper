package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/typed"
	"github.com/fwtllh-png/CodeHelper/internal/persist/repoindex"
	"github.com/fwtllh-png/CodeHelper/internal/platform/repowalk"
	"github.com/fwtllh-png/CodeHelper/internal/platform/symbols"
)

// Symbol tool names.
const (
	KindSymbol       = "search_symbol"
	KindDefinition   = "search_definition"
	KindReferences   = "search_references"
	KindRelatedTests = "search_related_tests"
)

// symbolTool answers a question about declarations from the repository index.
// Every one of them is read-only over the repository tree and reports its
// results as lexical, because that is what the index holds.
type symbolTool struct {
	typed.Contract[symbolInput, tool.Result]
	kind     string
	index    *repoindex.Index
	walker   *repowalk.Walker
	semantic symbols.Provider
}

type symbolInput struct {
	Query              string   `json:"query"`
	Name               string   `json:"name"`
	Kinds              []string `json:"kinds"`
	ExportedOnly       bool     `json:"exported_only"`
	Path               string   `json:"path"`
	PathPrefix         string   `json:"path_prefix"`
	Paths              []string `json:"paths"`
	IncludeDefinitions bool     `json:"include_definitions"`
	MaxResults         int      `json:"max_results"`
	Line               int      `json:"line"`
	Character          int      `json:"character"`
}

func newSymbolTool(
	kind string,
	index *repoindex.Index,
	walker *repowalk.Walker,
	semantic symbols.Provider,
) (*symbolTool, error) {
	executor := &symbolTool{
		kind: kind, index: index, walker: walker, semantic: semantic,
	}
	contract, err := typed.NewResultContract(typed.ResultSpec[symbolInput]{
		Name: kind, Disposition: tool.DispositionWaitForTeardown,
		Run: executor.run,
	})
	if err != nil {
		return nil, err
	}
	executor.Contract = contract
	return executor, nil
}

func (t *symbolTool) Descriptor() tool.Descriptor {
	descriptor := tool.Descriptor{
		Name: t.kind, Description: symbolDescription(t.kind), Visibility: tool.VisibleModel,
		Capability: tool.CapabilityRead, AccessMode: tool.AccessTree,
		ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
			Kind: "repo", ID: ".", Access: tool.AccessRead, Tree: true,
		}}},
		ParallelPolicy:     tool.ParallelConcurrent,
		SandboxRequirement: tool.SandboxNone,
		Availability:       tool.AvailabilityAvailable,
		InputSchema:        symbolSchema(t.kind),
	}
	// A tool the session cannot serve is declared unavailable rather than left to
	// fail at call time, so the model never plans around it.
	switch snapshot := t.index.Snapshot(); {
	case snapshot.Status == repoindex.StatusDisabled:
		descriptor.Availability = tool.AvailabilityUnavailable
		descriptor.UnavailableReason = "the repository index is disabled for this session"
	case snapshot.Status == repoindex.StatusDegraded:
		descriptor.Availability = tool.AvailabilityUnavailable
		descriptor.UnavailableReason = "the repository index is unavailable: " + snapshot.Detail
	}
	return descriptor
}

func symbolDescription(kind string) string {
	switch kind {
	case KindDefinition:
		return "Find where a symbol is declared, by exact name. " +
			"Lexical index, not a compiler: confirm the result before relying on it."
	case KindReferences:
		return "Find whole-word uses of a symbol across indexed files, excluding its " +
			"declarations. Lexical: matches in comments and strings are included."
	case KindRelatedTests:
		return "List the test files that cover the given source paths, by the naming " +
			"convention of each language. Paths with no known convention are omitted."
	default:
		return "Search declarations (functions, types, classes, constants) by name " +
			"substring. Lexical index, not a compiler."
	}
}

func symbolSchema(kind string) map[string]any {
	switch kind {
	case KindDefinition:
		return map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"name"},
			"properties": map[string]any{
				"name":        map[string]any{"type": "string", "minLength": float64(1)},
				"path":        map[string]any{"type": "string", "minLength": float64(1)},
				"line":        map[string]any{"type": "integer", "minimum": float64(1)},
				"character":   map[string]any{"type": "integer", "minimum": float64(1)},
				"kinds":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"max_results": map[string]any{"type": "integer"},
			},
		}
	case KindReferences:
		return map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"name"},
			"properties": map[string]any{
				"name":                map[string]any{"type": "string", "minLength": float64(1)},
				"path":                map[string]any{"type": "string", "minLength": float64(1)},
				"line":                map[string]any{"type": "integer", "minimum": float64(1)},
				"character":           map[string]any{"type": "integer", "minimum": float64(1)},
				"include_definitions": map[string]any{"type": "boolean"},
				"path_prefix":         map[string]any{"type": "string"},
				"max_results":         map[string]any{"type": "integer"},
			},
		}
	case KindRelatedTests:
		return map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"paths"},
			"properties": map[string]any{
				"paths": map[string]any{
					"type": "array", "minItems": float64(1),
					"items": map[string]any{"type": "string", "minLength": float64(1)},
				},
			},
		}
	default:
		return map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"query"},
			"properties": map[string]any{
				"query":         map[string]any{"type": "string", "minLength": float64(1)},
				"kinds":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"exported_only": map[string]any{"type": "boolean"},
				"path_prefix":   map[string]any{"type": "string"},
				"max_results":   map[string]any{"type": "integer"},
			},
		}
	}
}

func (t *symbolTool) run(ctx context.Context, input symbolInput) (tool.Result, error) {
	limit := input.MaxResults
	if budget := tool.ResultTokenBudget(ctx); budget != 0 &&
		(limit <= 0 || uint64(limit) > budget) {
		limit = int(min(budget, uint64(math.MaxInt)))
	}
	if limit <= 0 && t.kind != KindRelatedTests {
		return tool.Result{}, errors.New(
			"symbol search requires runtime result budget or explicit max_results",
		)
	}
	snapshot, err := t.index.Ensure(ctx)
	if err != nil {
		return tool.Result{}, err
	}
	if !snapshot.Ready() {
		return unavailableResult(snapshot)
	}
	switch t.kind {
	case KindDefinition:
		if query, ok := semanticQuery(input.Path, input.Line, input.Character); ok &&
			t.semantic != nil {
			found, semanticErr := t.semantic.Definition(ctx, query)
			if semanticErr == nil {
				return semanticDefinitions(found, input.Name, limit)
			}
			result, err := t.declarations(ctx, snapshot, repoindex.Query{
				Name: input.Name, Exact: true, Kinds: input.Kinds,
				Limit: limit,
			}, "", false)
			return semanticFallback(result, semanticErr), err
		}
		return t.declarations(ctx, snapshot, repoindex.Query{
			Name: input.Name, Exact: true, Kinds: input.Kinds,
			Limit: limit,
		}, "", false)
	case KindReferences:
		if query, ok := semanticQuery(input.Path, input.Line, input.Character); ok &&
			t.semantic != nil {
			found, semanticErr := t.semantic.References(
				ctx, query, input.IncludeDefinitions,
			)
			if semanticErr == nil {
				return semanticReferences(found, input.Name, limit)
			}
			result, err := t.references(
				ctx, snapshot, input.Name, input.PathPrefix,
				input.IncludeDefinitions, limit,
			)
			return semanticFallback(result, semanticErr), err
		}
		return t.references(ctx, snapshot, input.Name, input.PathPrefix,
			input.IncludeDefinitions, limit)
	case KindRelatedTests:
		return t.relatedTests(ctx, snapshot, input.Paths)
	default:
		return t.declarations(ctx, snapshot, repoindex.Query{
			Name: input.Query, Kinds: input.Kinds,
			Limit: limit,
		}, input.PathPrefix, input.ExportedOnly)
	}
}

type symbolMatch struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Character int    `json:"character,omitempty"`
	Container string `json:"container,omitempty"`
	Exported  bool   `json:"exported"`
}

func (t *symbolTool) declarations(
	ctx context.Context, snapshot repoindex.Snapshot,
	query repoindex.Query, pathPrefix string, exportedOnly bool,
) (tool.Result, error) {
	// Ask for more rows than the reply needs, so filtering by path or visibility
	// does not silently shrink a full page of results.
	limit := query.Limit
	query.Limit = limit * 4
	found, current, err := t.index.Symbols(ctx, query)
	if err != nil {
		return tool.Result{}, err
	}
	if !current.Ready() {
		return unavailableResult(current)
	}
	matches := make([]symbolMatch, 0, min(limit, len(found)))
	total := 0
	for _, symbol := range found {
		if exportedOnly && !symbol.Exported {
			continue
		}
		if pathPrefix != "" && !strings.HasPrefix(symbol.Path, pathPrefix) {
			continue
		}
		total++
		if len(matches) >= limit {
			continue
		}
		matches = append(matches, symbolMatch{
			Name: symbol.Name, Kind: symbol.Kind, File: symbol.Path,
			Line: symbol.Line, Container: symbol.Container, Exported: symbol.Exported,
		})
	}
	truncated := total > len(matches)
	hits := make([]tool.EvidenceHit, 0, len(matches))
	for _, match := range matches {
		hits = append(hits, tool.EvidenceHit{
			Kind: tool.EvidenceDefinition, Path: match.File,
			Line: match.Line, Symbol: match.Name,
		})
	}
	return marshalResult(map[string]any{
		"matches": matches, "total": total, "truncated": truncated,
		"resolution": repoindex.Resolution, "source": "repoindex",
		"version": repoindex.IndexerVersion, "confidence": "low",
	}, truncated, attach(map[string]any{
		"matches": total, "returned": len(matches),
		"resolution": repoindex.Resolution, "index_source": snapshot.Meta.Source,
		"index_files": snapshot.Meta.FileCount, "source": "repoindex",
		"version": repoindex.IndexerVersion, "confidence": "low",
	}, hits))
}

type referenceMatch struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	Character int    `json:"character,omitempty"`
	Text      string `json:"text,omitempty"`
}

// references scans the files the index lists for whole-word uses of a name. It
// rescans rather than keeping a token index: a reverse index of every identifier
// costs more storage than the recall it would add over this scan.
func (t *symbolTool) references(
	ctx context.Context, snapshot repoindex.Snapshot,
	name, pathPrefix string, includeDefinitions bool, limit int,
) (tool.Result, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return tool.Result{}, tool.Precondition(errors.New("a symbol name is required"))
	}
	paths, current, err := t.index.Paths(ctx, "")
	if err != nil {
		return tool.Result{}, err
	}
	if !current.Ready() {
		return unavailableResult(current)
	}
	declarations := map[string]struct{}{}
	if !includeDefinitions {
		declared, _, err := t.index.Symbols(ctx, repoindex.Query{
			Name: name, Exact: true, Limit: 0,
		})
		if err != nil {
			return tool.Result{}, err
		}
		for _, symbol := range declared {
			declarations[fmt.Sprintf("%s:%d", symbol.Path, symbol.Line)] = struct{}{}
		}
	}
	matches := make([]referenceMatch, 0, min(limit, 64))
	total := 0
	scanned := 0
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return tool.Result{}, err
		}
		if pathPrefix != "" && !strings.HasPrefix(path, pathPrefix) {
			continue
		}
		budget := tool.ResultTokenBudget(ctx)
		maxBytes := int64(min(budget, uint64(math.MaxInt64/4)) * 4)
		content, reason, err := t.walker.Read(repowalk.Entry{Path: path}, maxBytes)
		if err != nil {
			return tool.Result{}, err
		}
		if reason != repowalk.SkipNone {
			continue
		}
		scanned++
		for offset, text := range strings.Split(string(content.Data), "\n") {
			if !containsWord(text, name) {
				continue
			}
			line := offset + 1
			if _, declared := declarations[fmt.Sprintf("%s:%d", path, line)]; declared {
				continue
			}
			total++
			if len(matches) >= limit {
				continue
			}
			matches = append(matches, referenceMatch{File: path, Line: line, Text: text})
		}
	}
	truncated := total > len(matches)
	hits := make([]tool.EvidenceHit, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		// One entry per file: a symbol used forty times in one file is one place
		// the caller has to look at.
		if _, found := seen[match.File]; found {
			continue
		}
		seen[match.File] = struct{}{}
		hits = append(hits, tool.EvidenceHit{
			Kind: tool.EvidenceReference, Path: match.File, Line: match.Line, Symbol: name,
		})
	}
	return marshalResult(map[string]any{
		"matches": matches, "total": total, "truncated": truncated,
		"resolution": repoindex.Resolution, "source": "repoindex",
		"version": repoindex.IndexerVersion, "confidence": "low",
	}, truncated, attach(map[string]any{
		"matches": total, "returned": len(matches), "scanned_files": scanned,
		"resolution": repoindex.Resolution, "index_source": snapshot.Meta.Source,
		"source": "repoindex", "version": repoindex.IndexerVersion, "confidence": "low",
	}, hits))
}

func semanticQuery(path string, line, character int) (symbols.SemanticQuery, bool) {
	path = strings.TrimSpace(path)
	if path == "" || line < 1 || character < 1 {
		return symbols.SemanticQuery{}, false
	}
	return symbols.SemanticQuery{
		Path: path, Line: line, Character: character,
	}, true
}

func semanticDefinitions(
	found symbols.SemanticResult, name string, limit int,
) (tool.Result, error) {
	locations := found.Locations
	if len(locations) > limit {
		locations = locations[:limit]
	}
	matches := make([]symbolMatch, 0, len(locations))
	hits := make([]tool.EvidenceHit, 0, len(locations))
	for _, location := range locations {
		matches = append(matches, symbolMatch{
			Name: name, Kind: "semantic", File: location.Path,
			Line: location.Line, Character: location.Character,
		})
		hits = append(hits, tool.EvidenceHit{
			Kind: tool.EvidenceDefinition, Path: location.Path,
			Line: location.Line, Symbol: name,
		})
	}
	provenance := semanticProvenance(found)
	payload := map[string]any{
		"matches": matches, "total": len(found.Locations),
		"truncated": len(locations) != len(found.Locations),
	}
	for key, value := range provenance {
		payload[key] = value
	}
	provenance["matches"] = len(matches)
	provenance["returned"] = len(matches)
	return marshalResult(
		payload, len(locations) != len(found.Locations), attach(provenance, hits),
	)
}

func semanticReferences(
	found symbols.SemanticResult, name string, limit int,
) (tool.Result, error) {
	locations := found.Locations
	if len(locations) > limit {
		locations = locations[:limit]
	}
	matches := make([]referenceMatch, 0, len(locations))
	hits := make([]tool.EvidenceHit, 0, len(locations))
	for _, location := range locations {
		matches = append(matches, referenceMatch{
			File: location.Path, Line: location.Line, Character: location.Character,
		})
		hits = append(hits, tool.EvidenceHit{
			Kind: tool.EvidenceReference, Path: location.Path,
			Line: location.Line, Symbol: name,
		})
	}
	provenance := semanticProvenance(found)
	payload := map[string]any{
		"matches": matches, "total": len(found.Locations),
		"truncated": len(locations) != len(found.Locations),
	}
	for key, value := range provenance {
		payload[key] = value
	}
	provenance["matches"] = len(matches)
	provenance["returned"] = len(matches)
	return marshalResult(
		payload, len(locations) != len(found.Locations), attach(provenance, hits),
	)
}

func semanticProvenance(found symbols.SemanticResult) map[string]any {
	return map[string]any{
		"resolution": "semantic", "source": found.Source,
		"version": found.Version, "confidence": found.Confidence,
	}
}

func semanticFallback(result tool.Result, semanticErr error) tool.Result {
	if semanticErr == nil {
		return result
	}
	if result.Metadata == nil {
		result.Metadata = make(map[string]any)
	}
	message := semanticErr.Error()
	if len(message) > 512 {
		message = message[:512]
	}
	result.Metadata["semantic_fallback"] = message
	return result
}

func (t *symbolTool) relatedTests(
	ctx context.Context, snapshot repoindex.Snapshot, paths []string,
) (tool.Result, error) {
	if len(paths) == 0 {
		return tool.Result{}, tool.Precondition(errors.New("at least one path is required"))
	}
	related, current, err := t.index.RelatedTests(ctx, normalizePaths(paths))
	if err != nil {
		return tool.Result{}, err
	}
	if !current.Ready() {
		return unavailableResult(current)
	}
	sources := make([]string, 0, len(related))
	for source := range related {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	type coverage struct {
		Source string   `json:"source"`
		Tests  []string `json:"tests"`
	}
	entries := make([]coverage, 0, len(sources))
	unmapped := make([]string, 0)
	tests := 0
	for _, source := range sources {
		entries = append(entries, coverage{Source: source, Tests: related[source]})
		tests += len(related[source])
	}
	for _, source := range normalizePaths(paths) {
		if _, found := related[source]; !found {
			unmapped = append(unmapped, source)
		}
	}
	hits := make([]tool.EvidenceHit, 0, tests)
	for _, entry := range entries {
		for _, test := range entry.Tests {
			hits = append(hits, tool.EvidenceHit{Kind: tool.EvidenceTest, Path: test})
		}
	}
	return marshalResult(map[string]any{
		"coverage": entries, "unmapped": unmapped, "resolution": repoindex.Resolution,
	}, false, attach(map[string]any{
		"sources": len(entries), "tests": tests, "unmapped": len(unmapped),
		"resolution": repoindex.Resolution, "index_source": snapshot.Meta.Source,
	}, hits))
}

// unavailableResult reports an index that cannot answer. It is a result rather
// than an error so the model reads the reason and falls back to text search
// within the same turn.
func unavailableResult(snapshot repoindex.Snapshot) (tool.Result, error) {
	detail := snapshot.Detail
	if detail == "" {
		detail = "the repository index is " + snapshot.Status
	}
	return marshalResult(map[string]any{
		"status": "unavailable", "detail": detail, "matches": []any{},
		"hint": "fall back to search_text or search_project for this question",
	}, false, map[string]any{
		"status": "unavailable", "index_status": snapshot.Status,
	})
}

func marshalResult(payload any, truncated bool, metadata map[string]any) (tool.Result, error) {
	content, err := json.Marshal(payload)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{Content: string(content), Truncated: truncated, Metadata: metadata}, nil
}

// containsWord reports a whole-word occurrence, so searching for Serve does not
// match Server or reserve.
func containsWord(text, name string) bool {
	for offset := 0; ; {
		index := strings.Index(text[offset:], name)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(name)
		if !wordByte(text, start-1) && !wordByte(text, end) {
			return true
		}
		offset = start + 1
		if offset >= len(text) {
			return false
		}
	}
}

func wordByte(text string, index int) bool {
	if index < 0 || index >= len(text) {
		return false
	}
	symbol := text[index]
	return symbol == '_' || symbol == '$' ||
		(symbol >= '0' && symbol <= '9') ||
		(symbol >= 'a' && symbol <= 'z') ||
		(symbol >= 'A' && symbol <= 'Z')
}

func normalizePaths(paths []string) []string {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		trimmed := strings.TrimPrefix(strings.TrimSpace(strings.ReplaceAll(path, "\\", "/")), "./")
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
