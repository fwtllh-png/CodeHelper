package discovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/corepack"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestRepositoryD1CatalogCoversEveryCoreScenario(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	bundle, err := corepack.Load(
		root,
		"evaluation/scenarios/core/pack.json",
		"evaluation/impact-map.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Load(
		root,
		"evaluation/spec/d1-execution.json",
		bundle.Pack,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Scenarios) != len(bundle.Pack.Scenarios) {
		t.Fatalf(
			"D1 scenarios = %d, want %d",
			len(catalog.Scenarios),
			len(bundle.Pack.Scenarios),
		)
	}
	if len(catalog.Hosts) != 5 {
		t.Fatalf("D1 Host Cases = %d, want 5", len(catalog.Hosts))
	}
}

func TestD1CatalogSchemaValidatesRepositoryAsset(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	schemaRaw, err := os.ReadFile(filepath.Join(
		root,
		"evaluation/schema/d1-execution.schema.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var schemaValue any
	if err := json.Unmarshal(schemaRaw, &schemaValue); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("d1-execution.schema.json", schemaValue); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("d1-execution.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	catalogRaw, err := os.ReadFile(filepath.Join(
		root,
		"evaluation/spec/d1-execution.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var catalogValue any
	if err := json.Unmarshal(catalogRaw, &catalogValue); err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(catalogValue); err != nil {
		t.Fatal(err)
	}
}

func TestD1CatalogRejectsReusedVerification(t *testing.T) {
	pack := corepack.Pack{
		SchemaVersion: 2,
		Scenarios: []corepack.Scenario{
			{ID: "one", ExpectedFacts: []string{"one-proved"}},
			{ID: "two", ExpectedFacts: []string{"two-proved"}},
		},
	}
	catalog := Catalog{
		SchemaVersion: SchemaVersion,
		Scenarios: []Binding{
			{
				ID: "one", Host: "runtime",
				Command: []string{"go", "test", "./one"},
				Proves:  []string{"one-proved"},
			},
			{
				ID: "two", Host: "runtime",
				Command: []string{"go", "test", "./one"},
				Proves:  []string{"two-proved"},
			},
		},
		Hosts: hostFixtures(),
	}
	if err := catalog.Validate(pack); err == nil {
		t.Fatal("D1 catalog accepted a reused verification")
	}
}

func hostFixtures() []HostCase {
	return []HostCase{
		{ID: "acp", Host: "acp", Command: []string{"test", "acp"}},
		{ID: "cli", Host: "cli", Command: []string{"test", "cli"}},
		{ID: "tui", Host: "tui", Command: []string{"test", "tui"}},
		{ID: "vscode", Host: "vscode", Command: []string{"test", "vscode"}},
		{ID: "worker", Host: "worker", Command: []string{"test", "worker"}},
	}
}
