package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryHotspotBaseline(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	if err := run(root, "testdata/contracts/hotspot-baseline.json"); err != nil {
		t.Fatal(err)
	}
}

func TestCheckHotspotAcceptsMovedResponsibilitiesWithinPackage(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "internal/example/orchestrator.go", `package example
import "github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
type Model struct{}
func Run() {}
`)
	writeTestFile(t, root, "internal/example/reducer.go", `package example
type worker struct{}
func (worker) Run() {}
func Update() {}
`)
	writeTestFile(t, root, "internal/example/orchestrator_test.go", "package example\n")

	failures := checkHotspot(root, hotspot{
		ID: "example", Package: "./internal/example",
		HotspotFile: "internal/example/orchestrator.go", BaselineLines: 20,
		Responsibilities: map[string][]string{
			"orchestration": {"Model", "Run"},
			"reducer":       {"Update"},
		},
		ResponsibilityFiles: map[string]string{
			"orchestration": "orchestrator.go",
			"reducer":       "reducer.go",
		},
		AllowedInternalImports: []string{"internal/runtime/protocol"},
		RequiredTestAssets:     []string{"internal/example/orchestrator_test.go"},
	})
	if len(failures) != 0 {
		t.Fatalf("checkHotspot() failures = %v", failures)
	}
}

func TestCheckHotspotRejectsBoundaryDrift(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "internal/example/hotspot.go", `package example
import "github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
func Run() {}
`)

	failures := checkHotspot(root, hotspot{
		ID: "example", Package: "./internal/example",
		HotspotFile: "internal/example/hotspot.go", BaselineLines: 1,
		Responsibilities: map[string][]string{
			"orchestration": {"Run", "Missing"},
		},
		ResponsibilityFiles: map[string]string{"orchestration": "model.go"},
		RequiredTestAssets:  []string{"internal/example/missing_test.go"},
	})
	joined := strings.Join(failures, "\n")
	for _, want := range []string{
		"lost symbol Missing",
		"symbol Run found in hotspot.go, owner is model.go",
		"unreviewed internal dependency internal/adapter/provider",
		"hotspot grew",
		"required test asset missing",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("failures %q do not contain %q", joined, want)
		}
	}
}

func writeTestFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
