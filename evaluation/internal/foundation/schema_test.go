package foundation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestFoundationSchemasCompileAndValidateAssets(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	tests := []struct {
		schema string
		value  string
	}{
		{"foundation.schema.json", "evaluation/spec/foundation.json"},
		{"oracle-contract.schema.json", "evaluation/spec/oracle-contracts.json"},
		{"mutation-contract.schema.json", "evaluation/spec/mutation-contracts.json"},
	}
	for _, test := range tests {
		t.Run(test.schema, func(t *testing.T) {
			compiled := compileSchema(t, filepath.Join(
				root,
				"evaluation",
				"schema",
				test.schema,
			))
			raw, err := os.ReadFile(filepath.Join(root, test.value))
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
	for _, schema := range []string{
		"harness-lock.schema.json",
		"qualification.schema.json",
		"release-evidence.schema.json",
		"promotion-review.schema.json",
		"corpus-batch.schema.json",
		"qualified-corpus-manifest.schema.json",
		"run-evidence.schema.json",
	} {
		t.Run(schema, func(t *testing.T) {
			_ = compileSchema(t, filepath.Join(root, "evaluation", "schema", schema))
		})
	}
}

func compileSchema(t *testing.T, path string) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource(filepath.Base(path), value); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}
