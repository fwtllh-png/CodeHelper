package corepack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/oracle"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestCorePackSchemasCompileAndValidateRepositoryAssets(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	tests := []struct {
		name   string
		schema string
		value  string
	}{
		{
			name: "core-pack", schema: "core-pack.schema.json",
			value: "evaluation/scenarios/core/pack.json",
		},
		{
			name: "impact-map", schema: "impact-map.schema.json",
			value: "evaluation/impact-map.json",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schemaRaw, err := os.ReadFile(filepath.Join(
				root,
				"evaluation",
				"schema",
				test.schema,
			))
			if err != nil {
				t.Fatal(err)
			}
			var schemaValue any
			decodeErr := json.Unmarshal(schemaRaw, &schemaValue)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			compiler := jsonschema.NewCompiler()
			addErr := compiler.AddResource(test.schema, schemaValue)
			if addErr != nil {
				t.Fatal(addErr)
			}
			compiled, err := compiler.Compile(test.schema)
			if err != nil {
				t.Fatal(err)
			}
			valueRaw, err := os.ReadFile(filepath.Join(root, test.value))
			if err != nil {
				t.Fatal(err)
			}
			var value any
			decodeErr = json.Unmarshal(valueRaw, &value)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if err := compiled.Validate(value); err != nil {
				t.Fatal(err)
			}
		})
	}

	t.Run("oracle-input", func(t *testing.T) {
		schemaRaw, err := os.ReadFile(filepath.Join(
			root,
			"evaluation",
			"schema",
			"oracle-input.schema.json",
		))
		if err != nil {
			t.Fatal(err)
		}
		var schemaValue any
		if err := json.Unmarshal(schemaRaw, &schemaValue); err != nil {
			t.Fatal(err)
		}
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource("oracle-input.schema.json", schemaValue); err != nil {
			t.Fatal(err)
		}
		compiled, err := compiler.Compile("oracle-input.schema.json")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(oracle.NewBaseline("schema-test", "schema-fixture"))
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		if err := compiled.Validate(value); err != nil {
			t.Fatal(err)
		}
	})
}
