package web

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestWebHostDoesNotDependOnExecutionImplementations(t *testing.T) {
	forbidden := []string{
		"github.com/fwtllh-png/CodeHelper/internal/runtime/agent",
		"github.com/fwtllh-png/CodeHelper/internal/adapter/provider",
		"github.com/fwtllh-png/CodeHelper/internal/security/sandbox",
		"github.com/fwtllh-png/CodeHelper/internal/adapter/tool",
		"github.com/fwtllh-png/CodeHelper/internal/adapter/plugin",
		"github.com/fwtllh-png/CodeHelper/internal/adapter/skill",
		"github.com/fwtllh-png/CodeHelper/internal/runtime/app/extension",
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	files := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		file, err := parser.ParseFile(files, entry.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, importSpec := range file.Imports {
			path, err := strconv.Unquote(importSpec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			for _, prefix := range forbidden {
				if path == prefix || strings.HasPrefix(path, prefix+"/") {
					t.Errorf("%s imports forbidden execution package %s", entry.Name(), path)
				}
			}
		}
	}
}
