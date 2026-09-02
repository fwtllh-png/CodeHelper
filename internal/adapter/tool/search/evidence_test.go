package search

import (
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/QCode/internal/adapter/tool"
)

func evidenceHits(t *testing.T, result tool.Result) []tool.EvidenceHit {
	t.Helper()
	hits, found := result.Metadata[tool.MetadataEvidence].([]tool.EvidenceHit)
	if !found {
		t.Fatalf("result carried no evidence: %#v", result.Metadata)
	}
	return hits
}

func TestSearchTextClassifiesHitsByPath(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "api.go"), "package api\n\nfunc Serve() {}\n")
	write(t, filepath.Join(root, "api_test.go"), "package api\n\n// Serve is tested here\n")
	write(t, filepath.Join(root, "config.yaml"), "serve: true\n# Serve\n")
	registry := tool.NewRegistry(nil, nil)
	if err := RegisterWithBackend(registry, root, searchTestBackend{}); err != nil {
		t.Fatal(err)
	}

	hits := evidenceHits(t, execute(t, registry, "search_text", map[string]any{"query": "Serve"}))
	kinds := map[string]string{}
	for _, hit := range hits {
		kinds[hit.Path] = hit.Kind
		if hit.Line == 0 {
			t.Fatalf("content hit %q carries no line", hit.Path)
		}
	}
	want := map[string]string{
		"api.go":      tool.EvidenceTextMatch,
		"api_test.go": tool.EvidenceTest,
		"config.yaml": tool.EvidenceConfig,
	}
	for path, kind := range want {
		if kinds[path] != kind {
			t.Fatalf("%s classified as %q, want %q (hits %#v)", path, kinds[path], kind, hits)
		}
	}
}

func TestSearchTextReportsOneHitPerFile(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "api.go"), "package api\n\n// Serve\n// Serve\n// Serve\n")
	registry := tool.NewRegistry(nil, nil)
	if err := RegisterWithBackend(registry, root, searchTestBackend{}); err != nil {
		t.Fatal(err)
	}
	result := execute(t, registry, "search_text", map[string]any{"query": "Serve"})
	if matches := decodeMatches(t, result.Content); len(matches) != 3 {
		t.Fatalf("expected three matches in the body, got %#v", matches)
	}
	hits := evidenceHits(t, result)
	if len(hits) != 1 || hits[0].Line != 3 {
		t.Fatalf("expected one hit on the first matching line, got %#v", hits)
	}
}

func TestSearchFilesClassifiesWholeFileHits(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example\n")
	write(t, filepath.Join(root, "api.go"), "package api\n")
	registry := tool.NewRegistry(nil, nil)
	if err := RegisterWithBackend(registry, root, searchTestBackend{}); err != nil {
		t.Fatal(err)
	}
	hits := evidenceHits(t, execute(t, registry, "search_files", map[string]any{"query": "go"}))
	for _, hit := range hits {
		if hit.Line != 0 {
			t.Fatalf("a path hit carries a line: %#v", hit)
		}
		if hit.Path == "go.mod" && hit.Kind != tool.EvidenceConfig {
			t.Fatalf("go.mod classified as %q", hit.Kind)
		}
	}
}

func TestSymbolToolsClassifyDefinitionsReferencesAndTests(t *testing.T) {
	registry := indexedRegistry(t, map[string]string{
		"api.go":      "package api\n\nfunc Serve() {}\n",
		"call.go":     "package api\n\nfunc run() {\n\tServe()\n}\n",
		"api_test.go": "package api\n\nfunc TestServe(t *testing.T) {}\n",
	})

	definitions := evidenceHits(t, execute(t, registry, "search_definition", map[string]any{
		"name": "Serve",
	}))
	if len(definitions) != 1 {
		t.Fatalf("definitions = %#v", definitions)
	}
	if definitions[0].Kind != tool.EvidenceDefinition || definitions[0].Path != "api.go" ||
		definitions[0].Line != 3 || definitions[0].Symbol != "Serve" {
		t.Fatalf("definition hit = %#v", definitions[0])
	}

	references := evidenceHits(t, execute(t, registry, "search_references", map[string]any{
		"name": "Serve",
	}))
	if len(references) != 1 {
		t.Fatalf("references = %#v", references)
	}
	if references[0].Kind != tool.EvidenceReference || references[0].Path != "call.go" ||
		references[0].Symbol != "Serve" {
		t.Fatalf("reference hit = %#v", references[0])
	}

	tests := evidenceHits(t, execute(t, registry, "search_related_tests", map[string]any{
		"paths": []string{"api.go"},
	}))
	if len(tests) != 1 || tests[0].Kind != tool.EvidenceTest || tests[0].Path != "api_test.go" {
		t.Fatalf("test hits = %#v", tests)
	}
}

func TestUnavailableIndexReportsNoEvidence(t *testing.T) {
	registry := indexedRegistry(t, map[string]string{"api.go": "package api\n"})
	result := execute(t, registry, "search_definition", map[string]any{"name": "Nothing"})
	if _, found := result.Metadata[tool.MetadataEvidence]; found {
		t.Fatalf("a search with no hits reported evidence: %#v", result.Metadata)
	}
}
