package foundation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryFoundationContract(t *testing.T) {
	bundle, err := Load(
		filepath.Join("..", "..", ".."),
		"evaluation/spec/foundation.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Oracles.Oracles) != 9 ||
		len(bundle.Mutations.Mutations) != 7 ||
		!strings.HasPrefix(bundle.HarnessInputHash, "sha256:") {
		t.Fatalf("Foundation bundle = %+v", bundle)
	}
}

func TestFoundationRejectsMissingOracleAndMutation(t *testing.T) {
	bundle, err := Load(
		filepath.Join("..", "..", ".."),
		"evaluation/spec/foundation.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Oracles.Oracles = bundle.Oracles.Oracles[:8]
	if err := bundle.Validate(); err == nil ||
		!strings.Contains(err.Error(), "omits") {
		t.Fatalf("missing Oracle error = %v", err)
	}
	bundle, err = Load(
		filepath.Join("..", "..", ".."),
		"evaluation/spec/foundation.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Mutations.Mutations = bundle.Mutations.Mutations[:6]
	if err := bundle.Validate(); err == nil ||
		!strings.Contains(err.Error(), "omits") {
		t.Fatalf("missing Mutation error = %v", err)
	}
}

func TestFoundationRejectsUnknownManifestField(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(
		"..",
		"..",
		"..",
		"evaluation",
		"spec",
		"foundation.json",
	)
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	value["unknown"] = true
	raw, _ = json.Marshal(value)
	path := filepath.Join(root, "foundation.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root, "foundation.json"); err == nil ||
		!strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
}
