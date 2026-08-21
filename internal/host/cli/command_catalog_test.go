package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
)

const (
	commandListBegin = "<!-- BEGIN GENERATED COMMAND LIST -->"
	commandListEnd   = "<!-- END GENERATED COMMAND LIST -->"
)

func TestRootHelpContainsEveryRegisteredCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{"help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("help code=%d stderr=%q", code, stderr.String())
	}
	help := stdout.String()
	catalog := cli.CommandCatalog()
	if len(catalog) == 0 {
		t.Fatal("command catalog is empty")
	}
	seen := make(map[string]struct{}, len(catalog))
	for _, command := range catalog {
		if _, exists := seen[command.Path]; exists {
			t.Fatalf("duplicate command path %q", command.Path)
		}
		seen[command.Path] = struct{}{}
		if !strings.Contains(help, command.Usage) {
			t.Errorf("root help is missing registered command %q", command.Usage)
		}
	}
}

func TestGeneratedCommandListsMatchCobraTree(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	targets := []struct {
		path   string
		locale string
	}{
		{path: "docs/zh-CN/usage.md", locale: "zh-CN"},
	}
	for _, target := range targets {
		t.Run(target.locale, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, target.path))
			if err != nil {
				t.Fatal(err)
			}
			got, err := generatedCommandList(string(data))
			if err != nil {
				t.Fatal(err)
			}
			want := cli.RenderCommandReference(target.locale)
			if got != want {
				t.Fatalf(
					"%s command list is stale; run make command-docs",
					target.path,
				)
			}
		})
	}
}

func generatedCommandList(document string) (string, error) {
	start := strings.Index(document, commandListBegin)
	end := strings.Index(document, commandListEnd)
	if start < 0 || end < start {
		return "", os.ErrInvalid
	}
	start += len(commandListBegin)
	return strings.TrimSpace(document[start:end]), nil
}
