package admission

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestRepositoryH1CatalogHasCompleteUniqueInventory(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	catalog, err := LoadH1(root, "evaluation/spec/h1-execution.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Cases) != 18 {
		t.Fatalf("H1 cases = %d, want 18", len(catalog.Cases))
	}
}

func TestH1CatalogSchemaValidatesRepositoryAsset(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	schemaRaw, err := os.ReadFile(filepath.Join(
		root,
		"evaluation/schema/h1-execution.schema.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var schemaValue any
	if err := json.Unmarshal(schemaRaw, &schemaValue); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("h1-execution.schema.json", schemaValue); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("h1-execution.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	catalogRaw, err := os.ReadFile(filepath.Join(
		root,
		"evaluation/spec/h1-execution.json",
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

func TestH1CatalogRejectsReusedVerification(t *testing.T) {
	catalog := completeCatalog()
	catalog.Cases[1].Command = append([]string(nil), catalog.Cases[0].Command...)
	if err := catalog.Validate(); err == nil {
		t.Fatal("H1 catalog accepted a reused verification")
	}
}

func TestH1CatalogRejectsMissingLane(t *testing.T) {
	catalog := completeCatalog()
	for index := range catalog.Cases {
		if catalog.Cases[index].Lane == "filesystem" {
			catalog.Cases[index].Lane = "process"
		}
	}
	if err := catalog.Validate(); err == nil {
		t.Fatal("H1 catalog accepted a missing lane")
	}
}

func completeCatalog() H1Catalog {
	lanes := []string{
		"extension_host",
		"process",
		"provider",
		"persistence",
		"filesystem",
	}
	catalog := H1Catalog{SchemaVersion: H1SchemaVersion}
	for index := 0; index < 18; index++ {
		id := fmt.Sprintf("case-%02d", index)
		catalog.Cases = append(catalog.Cases, H1Case{
			ID:      id,
			Lane:    lanes[index%len(lanes)],
			Kind:    "go_test",
			Command: []string{"go", "test", "./" + id},
		})
	}
	return catalog
}
