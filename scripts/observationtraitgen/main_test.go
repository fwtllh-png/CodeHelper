package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryManifestGeneratesCommittedOutputs(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := readManifest(filepath.Join(
		root,
		"internal/observability/schema/observation_traits.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	output, err := generate(manifest)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string][]byte{
		"internal/observability/observation/traits.gen.go": output.goSource,
		"web/src/protocol/observation.generated.ts":        output.typeScript,
		"docs/protocol/observation.schema.json":            output.schema,
	}
	for path, content := range expected {
		if err := checkFile(filepath.Join(root, path), content); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGenerateIsDeterministic(t *testing.T) {
	manifest := fixtureManifest()
	first, err := generate(manifest)
	if err != nil {
		t.Fatal(err)
	}
	second, err := generate(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.goSource) != string(second.goSource) ||
		string(first.typeScript) != string(second.typeScript) ||
		string(first.schema) != string(second.schema) {
		t.Fatal("observation trait generation is not deterministic")
	}
}

func TestManifestRejectsUnsafeTraits(t *testing.T) {
	cases := []map[string]trait{
		{},
		{"Bad Kind": fixtureTrait()},
		{"runtime.started": {
			Owner: "runtime", Durability: "retained", Payload: "optional",
			Retention: "audit", Correlations: []string{"turn"},
			OTEL: "event", Priority: "normal",
		}},
		{"runtime.started": {
			Owner: "runtime", Durability: "retained", Payload: "optional_sensitive",
			Retention: "audit", Correlations: []string{"runtime"},
			OTEL: "event", Priority: "normal",
		}},
	}
	for index, manifest := range cases {
		if err := validateManifest(manifest); err == nil {
			t.Fatalf("case %d unexpectedly passed", index)
		}
	}
}

func TestCheckFileReportsDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generated.go")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := checkFile(path, []byte("new"))
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("checkFile() error = %v", err)
	}
}

func TestManifestJSONRoundTrip(t *testing.T) {
	manifest := fixtureManifest()
	content, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]trait
	if err := json.Unmarshal(content, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := validateManifest(decoded); err != nil {
		t.Fatal(err)
	}
}

func fixtureManifest() map[string]trait {
	return map[string]trait{"runtime.started": fixtureTrait()}
}

func fixtureTrait() trait {
	return trait{
		Owner: "runtime", Durability: "retained", Payload: "optional",
		Retention: "audit", Correlations: []string{"runtime"},
		OTEL: "span_start", Priority: "critical",
	}
}
