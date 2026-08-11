package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryArchitectureMetrics(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, runErr := run(root, "docs/architecture-metrics-baseline.json", "", ""); runErr != nil {
		t.Fatal(runErr)
	}
}

func TestMeasurePackageAndFiles(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/example/example.go", `package example
import (
	"sync"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)
type Options struct {
	Name string
	Limit int
}
type service struct {
	mu sync.Mutex
}
func Run() {
	_ = protocol.EventKind("")
}
`)
	writeFixture(t, root, "internal/example/example_test.go", `package example
import "github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
var _ = tool.Result{}
`)
	metrics, err := measurePackage(filepath.Join(root, "internal/example"))
	if err != nil {
		t.Fatal(err)
	}
	if metrics["internal_fanout"] != 1 {
		t.Fatalf("internal_fanout = %d", metrics["internal_fanout"])
	}
	if metrics["options_fields"] != 2 {
		t.Fatalf("options_fields = %d", metrics["options_fields"])
	}
	if metrics["mutex_fields"] != 1 {
		t.Fatalf("mutex_fields = %d", metrics["mutex_fields"])
	}
	if metrics["production_lines"] == 0 {
		t.Fatal("production_lines was not measured")
	}

	fileMetrics, err := measureFile(filepath.Join(root, "internal/example/example.go"))
	if err != nil {
		t.Fatal(err)
	}
	if fileMetrics["lines"] == 0 || fileMetrics["max_function_lines"] != 3 {
		t.Fatalf("file metrics = %+v", fileMetrics)
	}
}

func TestRepositoryEventSwitchSites(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/example/example.go", `package example
import "github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
func project(event protocol.Event) {
	switch event.Kind {
	case protocol.EventTurnCompleted:
	case protocol.EventToolResult:
	}
}
`)
	writeFixture(t, root, "extensions/vscode/src/projector.ts", `
export function project(kind: string): void {
  switch (kind) {
    case "turn.completed":
    case "tool.result":
      return;
  }
}
`)
	writeFixture(t, root, "extensions/vscode/src/projector.test.ts", `
switch (kind) { case "turn.completed": break; }
`)
	metrics, err := measureRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	if metrics["event_switch_sites"] != 2 {
		t.Fatalf("event_switch_sites = %d", metrics["event_switch_sites"])
	}
}

func TestRunRejectsMetricDriftAndWritesReport(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/example/example.go", `package example
type Options struct {
	One string
	Two string
}
`)
	writeBaseline(t, root, baseline{
		SchemaVersion: 1,
		RequirementID: "ARCH-RATCHET-001",
		Targets: []target{{
			ID: "example", Kind: "package", Path: "internal/example",
			Limits: map[string]int{"options_fields": 1},
		}},
	})
	result, err := run(
		root,
		"docs/architecture-metrics-baseline.json",
		".tmp/report.json",
		"",
	)
	if err == nil || !strings.Contains(err.Error(), "options_fields is 2, maximum is 1") {
		t.Fatalf("run() error = %v", err)
	}
	if len(result.Targets) != 1 {
		t.Fatalf("targets = %+v", result.Targets)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".tmp/report.json")); statErr != nil {
		t.Fatal(statErr)
	}
}

func TestRunRequiresStaleLimitsToRatchetDown(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/example/example.go", `package example
type Options struct {
	Only string
}
`)
	writeBaseline(t, root, baseline{
		SchemaVersion: 1,
		RequirementID: "ARCH-RATCHET-001",
		Targets: []target{{
			ID: "example", Kind: "package", Path: "internal/example",
			Limits: map[string]int{"options_fields": 2},
		}},
	})
	_, err := run(root, "docs/architecture-metrics-baseline.json", "", "")
	if err == nil || !strings.Contains(err.Error(), "limit is stale") {
		t.Fatalf("run() error = %v", err)
	}
}

func TestRatchetRequiresAndConsumesExplicitRelaxation(t *testing.T) {
	previous := baseline{
		SchemaVersion: 1,
		RequirementID: "ARCH-RATCHET-001",
		Targets: []target{{
			ID: "engine", Kind: "package", Path: "internal/runtime/agent/engine",
			Limits: map[string]int{"internal_fanout": 20, "options_fields": 40},
		}},
	}
	increased := previous
	increased.Targets = append([]target(nil), previous.Targets...)
	increased.Targets[0].Limits = map[string]int{
		"internal_fanout": 21,
		"options_fields":  40,
	}
	err := validateRatchet(previous, increased)
	if err == nil || !strings.Contains(err.Error(), "without an explicit relaxation") {
		t.Fatalf("validateRatchet() error = %v", err)
	}
	increased.Targets[0].Relaxations = map[string]string{
		"internal_fanout": "Stage C introduces one reviewed module boundary",
	}
	if ratchetErr := validateRatchet(previous, increased); ratchetErr != nil {
		t.Fatal(ratchetErr)
	}
	increased.Targets[0].Limits["internal_fanout"] = 20
	err = validateRatchet(previous, increased)
	if err == nil || !strings.Contains(err.Error(), "stale relaxation") {
		t.Fatalf("stale validateRatchet() error = %v", err)
	}
}

func TestRatchetRequiresExplicitRetirement(t *testing.T) {
	previous := baseline{
		SchemaVersion: 1,
		RequirementID: "ARCH-RATCHET-001",
		Targets: []target{{
			ID: "engine", Kind: "package", Path: "internal/runtime/agent/engine",
			Limits: map[string]int{"internal_fanout": 20, "options_fields": 40},
		}},
	}
	removedMetric := previous
	removedMetric.Targets = append([]target(nil), previous.Targets...)
	removedMetric.Targets[0].Limits = map[string]int{"internal_fanout": 20}
	err := validateRatchet(previous, removedMetric)
	if err == nil || !strings.Contains(err.Error(), "metric removed without an explicit retirement") {
		t.Fatalf("validateRatchet() error = %v", err)
	}
	removedMetric.Retirements = map[string]string{
		"engine.options_fields": "Turn options moved to typed component contracts",
	}
	if ratchetErr := validateRatchet(previous, removedMetric); ratchetErr != nil {
		t.Fatal(ratchetErr)
	}

	removedTarget := previous
	removedTarget.Targets = nil
	err = validateRatchet(previous, removedTarget)
	if err == nil || !strings.Contains(err.Error(), "target removed without an explicit retirement") {
		t.Fatalf("validateRatchet() target error = %v", err)
	}
	removedTarget.Retirements = map[string]string{
		"engine": "The hotspot file and package no longer exist after the split",
	}
	if ratchetErr := validateRatchet(previous, removedTarget); ratchetErr != nil {
		t.Fatal(ratchetErr)
	}
}

func TestValidateBaselineRejectsUnknownShape(t *testing.T) {
	err := validateBaseline(baseline{
		SchemaVersion: 1,
		RequirementID: "ARCH-RATCHET-001",
		Targets: []target{{
			ID: "bad", Kind: "unknown", Path: ".", Limits: map[string]int{"lines": 1},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported kind") {
		t.Fatalf("validateBaseline() error = %v", err)
	}
	err = validateBaseline(baseline{
		SchemaVersion: 1,
		RequirementID: "ARCH-RATCHET-001",
		Targets: []target{{
			ID: "unsafe", Kind: "file", Path: "../outside.go",
			Limits: map[string]int{"lines": 1},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("unsafe validateBaseline() error = %v", err)
	}
	err = validateBaseline(baseline{
		SchemaVersion: 1,
		RequirementID: "ARCH-RATCHET-001",
		Targets: []target{{
			ID: "relaxed", Kind: "file", Path: "inside.go",
			Limits:      map[string]int{"lines": 1},
			Relaxations: map[string]string{"unknown": "not a limit"},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "relaxes unknown metric") {
		t.Fatalf("relaxation validateBaseline() error = %v", err)
	}
}

func writeBaseline(t *testing.T, root string, value baseline) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "docs/architecture-metrics-baseline.json", string(data))
}

func writeFixture(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
