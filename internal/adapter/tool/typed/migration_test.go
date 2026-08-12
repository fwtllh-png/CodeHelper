package typed_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTierOneToolsUseTypedBoundary(t *testing.T) {
	root := filepath.Clean("..")
	files := []string{
		"completion/completion.go",
		"lsp/lsp.go",
		"memory/memory.go",
		"revert/revert.go",
		"skill/skill.go",
		"toolsearch/tool_search.go",
	}
	for _, relative := range files {
		t.Run(relative, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, relative))
			if err != nil {
				t.Fatal(err)
			}
			source := string(data)
			if !strings.Contains(source, "typed.Define(") {
				t.Fatal("Tier-1 tool does not construct a typed executor")
			}
			if strings.Contains(source, "json.Unmarshal(") {
				t.Fatal("Tier-1 tool reintroduced local JSON decoding")
			}
		})
	}
}
