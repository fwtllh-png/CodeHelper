package corpus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/capture"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/evidence"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestEvidenceAndCorpusSchemasCompileAndValidate(t *testing.T) {
	event, err := evidence.Seal([]evidence.Envelope{{
		OffsetMS: 0,
		Source:   evidence.SourceRuntime,
		Kind:     "turn.failed",
		Identity: evidence.Identity{Turn: "turn-001"},
		Policy: evidence.Policy{
			Class: evidence.DataOperational, Redaction: evidence.RedactionNotRequired,
		},
		Data: []byte(`{"metadata":true}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		SchemaVersion: 1, ID: "schema-trace",
		SourceFormat: "vscode_runtime_capture_v1",
		SourceClass:  SourceSynthetic, SourceDigest: digest("a"),
		Selector:         Selector{Kind: "full", Index: 1},
		SourceEventCount: 1, EventCount: 1,
		TraceDigest: digest("b"), FirstDigest: digest("c"), LastDigest: digest("c"),
		FailureSignature: "turn_failed",
		ContentMode:      "metadata_only", SecretScan: "passed",
	}
	for _, test := range []struct {
		name   string
		schema string
		value  any
	}{
		{name: "evidence", schema: "evidence.schema.json", value: event[0]},
		{name: "corpus", schema: "corpus-manifest.schema.json", value: manifest},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", "schema", test.schema))
			if err != nil {
				t.Fatal(err)
			}
			var schemaValue any
			if err := json.Unmarshal(raw, &schemaValue); err != nil {
				t.Fatal(err)
			}
			compiler := jsonschema.NewCompiler()
			if err := compiler.AddResource(test.schema, schemaValue); err != nil {
				t.Fatal(err)
			}
			compiled, err := compiler.Compile(test.schema)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(test.value)
			if err != nil {
				t.Fatal(err)
			}
			var value any
			if err := json.Unmarshal(encoded, &value); err != nil {
				t.Fatal(err)
			}
			if err := compiled.Validate(value); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestQualifiedCorpusSchemasMatchGoContracts(t *testing.T) {
	review := PromotionReview{
		SchemaVersion: QualifiedSchemaVersion,
		ID:            "review-01", BatchID: "batch-01", Reviewer: "reviewer-01",
		Decision: "approved", SourceDigest: digest("a"), ReviewedOn: "2026-08-19",
	}
	batch := BatchManifest{
		SchemaVersion: QualifiedSchemaVersion,
		ID:            "batch-01", SourceDigest: digest("a"), ReviewDigest: digest("b"),
		Entries: []string{"trace-01"},
	}
	manifest := QualifiedManifest{
		SchemaVersion: QualifiedSchemaVersion,
		ID:            "trace-01", BatchID: "batch-01",
		SourceFormat: capture.FormatProvider, SourceClass: SourceSynthetic,
		SourceDigest: digest("a"), ReviewDigest: digest("b"),
		Selector:         Selector{Kind: "full", Index: 1},
		SourceEventCount: 1, EventCount: 1,
		TraceDigest: digest("c"), FirstDigest: digest("d"), LastDigest: digest("d"),
		FailureSignature: "partial_trace", ReplayLevel: "structural",
		ContentMode: "metadata_only", SecretScan: "passed",
	}
	tests := []struct {
		schema string
		value  any
	}{
		{"promotion-review.schema.json", review},
		{"corpus-batch.schema.json", batch},
		{"qualified-corpus-manifest.schema.json", manifest},
	}
	for _, test := range tests {
		t.Run(test.schema, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", "schema", test.schema))
			if err != nil {
				t.Fatal(err)
			}
			var schemaValue any
			if err := json.Unmarshal(raw, &schemaValue); err != nil {
				t.Fatal(err)
			}
			compiler := jsonschema.NewCompiler()
			if err := compiler.AddResource(test.schema, schemaValue); err != nil {
				t.Fatal(err)
			}
			compiled, err := compiler.Compile(test.schema)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(test.value)
			if err != nil {
				t.Fatal(err)
			}
			var value any
			if err := json.Unmarshal(encoded, &value); err != nil {
				t.Fatal(err)
			}
			if err := compiled.Validate(value); err != nil {
				t.Fatal(err)
			}
		})
	}
}
