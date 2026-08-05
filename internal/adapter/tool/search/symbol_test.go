package search

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/persist/repoindex"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/platform/repowalk"
)

func TestSearchSymbolFindsDeclarationsBySubstring(t *testing.T) {
	registry := indexedRegistry(t, map[string]string{
		"api/server.go": "package api\n\ntype Handler struct{}\n\n" +
			"func (h *Handler) ServeHTTP() {}\n\nfunc newHandler() *Handler { return nil }\n",
		"web/app.ts": "export class HandlerView {\n\trender() {}\n}\n",
	})

	result := execute(t, registry, "search_symbol", map[string]any{"query": "handler"})
	matches := decodeSymbols(t, result.Content)
	// The query matches declaration names, not the types that hold them: ServeHTTP
	// is a method of Handler and is not a match for "handler".
	if len(matches) != 3 {
		t.Fatalf("matches = %#v", matches)
	}
	if matches[0]["name"] != "Handler" || matches[0]["file"] != "api/server.go" ||
		matches[0]["kind"] != "type" || matches[0]["line"].(float64) != 3 {
		t.Fatalf("first match = %#v", matches[0])
	}
	if result.Metadata["resolution"] != repoindex.Resolution {
		t.Fatalf("resolution = %#v", result.Metadata["resolution"])
	}

	scoped := execute(t, registry, "search_symbol", map[string]any{
		"query": "handler", "path_prefix": "web/", "exported_only": true,
	})
	if matches := decodeSymbols(t, scoped.Content); len(matches) != 1 ||
		matches[0]["name"] != "HandlerView" {
		t.Fatalf("scoped matches = %#v", matches)
	}

	kinds := execute(t, registry, "search_symbol", map[string]any{
		"query": "serve", "kinds": []string{"method"},
	})
	if matches := decodeSymbols(t, kinds.Content); len(matches) != 1 ||
		matches[0]["name"] != "ServeHTTP" || matches[0]["container"] != "Handler" {
		t.Fatalf("kind filtered matches = %#v", matches)
	}
}

func TestSearchSymbolTruncatesAndReportsTheTotal(t *testing.T) {
	registry := indexedRegistry(t, map[string]string{
		"api.go": "package api\n\nfunc HandleA() {}\n\nfunc HandleB() {}\n\nfunc HandleC() {}\n",
	})
	result := execute(t, registry, "search_symbol", map[string]any{
		"query": "Handle", "max_results": 2,
	})
	matches := decodeSymbols(t, result.Content)
	if !result.Truncated || len(matches) != 2 {
		t.Fatalf("result = %+v, matches = %#v", result, matches)
	}
	if result.Metadata["matches"] != 3 || result.Metadata["returned"] != 2 {
		t.Fatalf("metadata = %#v", result.Metadata)
	}
}

func TestSearchDefinitionMatchesTheWholeName(t *testing.T) {
	registry := indexedRegistry(t, map[string]string{
		"api.go":  "package api\n\nfunc Serve() {}\n\nfunc ServeAll() {}\n",
		"main.py": "def serve():\n    pass\n",
	})
	result := execute(t, registry, "search_definition", map[string]any{"name": "Serve"})
	matches := decodeSymbols(t, result.Content)
	// Exact and case-insensitive: ServeAll is a different declaration, the Python
	// serve is the same name in another language.
	if len(matches) != 2 {
		t.Fatalf("matches = %#v", matches)
	}
	for _, match := range matches {
		if match["name"] != "Serve" && match["name"] != "serve" {
			t.Fatalf("match = %#v", match)
		}
	}
}

func TestSearchReferencesExcludesDeclarationsAndPartialWords(t *testing.T) {
	registry := indexedRegistry(t, map[string]string{
		"api.go":  "package api\n\nfunc Serve() {}\n",
		"call.go": "package api\n\nfunc run() {\n\tServe()\n\tServeAll()\n\t// Serve again\n}\n",
	})
	result := execute(t, registry, "search_references", map[string]any{"name": "Serve"})
	matches := decodeMatches(t, result.Content)
	if len(matches) != 2 {
		t.Fatalf("matches = %#v", matches)
	}
	// The call and the comment are both uses; ServeAll is not, and the declaration
	// itself is left out.
	if matches[0]["file"] != "call.go" || matches[0]["line"].(float64) != 4 ||
		matches[1]["line"].(float64) != 6 {
		t.Fatalf("matches = %#v", matches)
	}

	withDeclarations := execute(t, registry, "search_references", map[string]any{
		"name": "Serve", "include_definitions": true,
	})
	if matches := decodeMatches(t, withDeclarations.Content); len(matches) != 3 ||
		matches[0]["file"] != "api.go" {
		t.Fatalf("matches with declarations = %#v", matches)
	}

	truncated := execute(t, registry, "search_references", map[string]any{
		"name": "Serve", "max_results": 1,
	})
	if !truncated.Truncated || truncated.Metadata["matches"] != 2 {
		t.Fatalf("truncated = %+v", truncated)
	}
}

func TestSearchRelatedTestsReportsWhatItCannotMap(t *testing.T) {
	registry := indexedRegistry(t, map[string]string{
		"api.go":        "package api\n\nfunc Serve() {}\n",
		"api_test.go":   "package api\n\nfunc TestServe(t *testing.T) {}\n",
		"tool.rs":       "pub fn run() {}\n",
		"pkg/util.py":   "def helper():\n    pass\n",
		"pkg/README.md": "# docs\n",
	})
	result := execute(t, registry, "search_related_tests", map[string]any{
		"paths": []string{"api.go", "pkg/util.py", "tool.rs", "pkg/README.md"},
	})
	var payload struct {
		Coverage []struct {
			Source string   `json:"source"`
			Tests  []string `json:"tests"`
		} `json:"coverage"`
		Unmapped []string `json:"unmapped"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Coverage) != 2 {
		t.Fatalf("coverage = %#v", payload.Coverage)
	}
	if payload.Coverage[0].Source != "api.go" ||
		len(payload.Coverage[0].Tests) != 1 || payload.Coverage[0].Tests[0] != "api_test.go" {
		t.Fatalf("go coverage = %#v", payload.Coverage[0])
	}
	// Python has a convention but no test file here, which is different from Rust
	// and Markdown having no convention this index knows.
	if payload.Coverage[1].Source != "pkg/util.py" || len(payload.Coverage[1].Tests) != 0 {
		t.Fatalf("python coverage = %#v", payload.Coverage[1])
	}
	if len(payload.Unmapped) != 2 ||
		payload.Unmapped[0] != "tool.rs" || payload.Unmapped[1] != "pkg/README.md" {
		t.Fatalf("unmapped = %#v", payload.Unmapped)
	}
}

func TestSymbolToolsAreUnavailableWithoutAnIndex(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "api.go"), "package api\n\nfunc Serve() {}\n")
	registry := tool.NewRegistry(nil, nil)
	if err := RegisterWithBackend(registry, root, searchTestBackend{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"search_symbol", "search_definition", "search_references", "search_related_tests",
	} {
		_, descriptor, _, err := registry.Resolve(name)
		if !errors.Is(err, tool.ErrToolUnavailable) {
			t.Fatalf("%s resolve error = %v", name, err)
		}
		if descriptor.UnavailableReason == "" {
			t.Fatalf("%s gave no reason", name)
		}
	}
	// Text search keeps working: a missing index costs recall, not the ability to
	// look at the repository.
	result := execute(t, registry, "search_text", map[string]any{"query": "Serve"})
	if matches := decodeMatches(t, result.Content); len(matches) != 1 {
		t.Fatalf("text matches = %#v", matches)
	}
}

func TestSymbolToolsReportADegradedIndexAsUnavailable(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "api.go"), "package api\n\nfunc Serve() {}\n")
	database := openIndexDatabase(t)
	store, err := repoindex.NewStore(database, root)
	if err != nil {
		t.Fatal(err)
	}
	walker, err := repowalk.New(root, searchTestBackend{})
	if err != nil {
		t.Fatal(err)
	}
	index, err := repoindex.NewIndex(store, walker, repoindex.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(t.Context(), "DROP TABLE repo_index_symbols"); err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry(nil, nil)
	if err := RegisterWithIndex(registry, root, searchTestBackend{}, index); err != nil {
		t.Fatal(err)
	}

	// The first call cannot build the index, so it answers with the reason instead
	// of a wrong empty result.
	result := execute(t, registry, "search_symbol", map[string]any{"query": "Serve"})
	if result.Metadata["status"] != "unavailable" ||
		result.Metadata["index_status"] != repoindex.StatusDegraded {
		t.Fatalf("metadata = %#v", result.Metadata)
	}
	// Once the index is known to be broken the tool declares itself unavailable,
	// so the model stops planning calls that cannot work.
	if _, _, _, err := registry.Resolve("search_symbol"); !errors.Is(err, tool.ErrToolUnavailable) {
		t.Fatalf("resolve error = %v", err)
	}
}

func indexedRegistry(t *testing.T, files map[string]string) *tool.Registry {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		write(t, filepath.Join(root, filepath.FromSlash(name)), content)
	}
	store, err := repoindex.NewStore(openIndexDatabase(t), root)
	if err != nil {
		t.Fatal(err)
	}
	walker, err := repowalk.New(root, searchTestBackend{})
	if err != nil {
		t.Fatal(err)
	}
	index, err := repoindex.NewIndex(store, walker, repoindex.Options{})
	if err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry(nil, nil)
	if err := RegisterWithIndex(registry, root, searchTestBackend{}, index); err != nil {
		t.Fatal(err)
	}
	return registry
}

func openIndexDatabase(t *testing.T) *sql.DB {
	t.Helper()
	state, err := sqlitestate.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	return state.DB()
}

func decodeSymbols(t *testing.T, content string) []map[string]any {
	t.Helper()
	return decodeMatches(t, content)
}
