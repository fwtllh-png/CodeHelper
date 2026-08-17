package semantic

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestSemanticReducerHasNoBusinessAuthorityDependencies(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	const module = "github.com/fwtllh-png/CodeHelper/"
	allowed := map[string]bool{
		module + "internal/observability/journal":     true,
		module + "internal/observability/observation": true,
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(
			token.NewFileSet(),
			entry.Name(),
			nil,
			parser.ImportsOnly,
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range file.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(path, module) && !allowed[path] {
				t.Fatalf(
					"%s imports forbidden authority dependency %s",
					entry.Name(),
					path,
				)
			}
		}
	}
}
