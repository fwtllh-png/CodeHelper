package spec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRepositoryEvaluationContract(t *testing.T) {
	bundle, err := Check(
		filepath.Join("..", "..", ".."),
		"evaluation/manifest.json",
		time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Manifest.Suites) != 3 || len(bundle.Scenarios) != 3 {
		t.Fatalf(
			"bundle = %d suites, %d scenarios",
			len(bundle.Manifest.Suites),
			len(bundle.Scenarios),
		)
	}
}

func TestContractRejectsUnknownFields(t *testing.T) {
	root, manifest, scenario := contractFixture(t)
	var document map[string]any
	if err := json.Unmarshal(manifest, &document); err != nil {
		t.Fatal(err)
	}
	document["unexpected"] = true
	writeJSON(t, filepath.Join(root, "evaluation", "manifest.json"), document)
	writeFile(t, scenarioPath(root), scenario)

	_, err := Check(root, "evaluation/manifest.json", fixtureNow())
	if err == nil || !strings.Contains(err.Error(), "additional properties") {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestContractRejectsDuplicateSuiteIDs(t *testing.T) {
	root, manifestRaw, scenario := contractFixture(t)
	var manifest Manifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Suites = append(manifest.Suites, manifest.Suites[0])
	writeJSON(t, filepath.Join(root, "evaluation", "manifest.json"), manifest)
	writeFile(t, scenarioPath(root), scenario)

	_, err := Check(root, "evaluation/manifest.json", fixtureNow())
	if err == nil || !strings.Contains(err.Error(), "duplicate evaluation suite") {
		t.Fatalf("duplicate suite error = %v", err)
	}
}

func TestContractRejectsMissingRequiredOracle(t *testing.T) {
	root, manifest, scenarioRaw := contractFixture(t)
	var scenario Scenario
	if err := json.Unmarshal(scenarioRaw, &scenario); err != nil {
		t.Fatal(err)
	}
	scenario.Oracles = []string{"contract_valid"}
	writeFile(t, filepath.Join(root, "evaluation", "manifest.json"), manifest)
	writeJSON(t, scenarioPath(root), scenario)

	_, err := Check(root, "evaluation/manifest.json", fixtureNow())
	if err == nil || !strings.Contains(err.Error(), "command driver requires") {
		t.Fatalf("missing oracle error = %v", err)
	}
}

func TestContractRejectsEmptyReleaseDenominator(t *testing.T) {
	root, manifestRaw, scenario := contractFixture(t)
	var manifest Manifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Suites[0].ReleasePolicy.MinimumValidRuns = 0
	writeJSON(t, filepath.Join(root, "evaluation", "manifest.json"), manifest)
	writeFile(t, scenarioPath(root), scenario)

	_, err := Check(root, "evaluation/manifest.json", fixtureNow())
	if err == nil || !strings.Contains(err.Error(), "minimum_valid_runs") {
		t.Fatalf("empty denominator error = %v", err)
	}
}

func TestContractRejectsExpiredPolicyException(t *testing.T) {
	root, manifestRaw, scenarioRaw := contractFixture(t)
	var manifest Manifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	var scenario Scenario
	if err := json.Unmarshal(scenarioRaw, &scenario); err != nil {
		t.Fatal(err)
	}
	manifest.Suites = manifest.Suites[:1]
	manifest.Suites[0].Risk = RiskP2
	manifest.Suites[0].Exceptions = []PolicyException{{
		ID:              "temporary-unavailable",
		Owner:           "evaluation",
		Reason:          "fixture",
		ExpiresOn:       "2026-08-18",
		ScenarioIDs:     []string{scenario.ID},
		AllowedStatuses: []Status{StatusUnavailable},
	}}
	scenario.Risk = RiskP2
	writeJSON(t, filepath.Join(root, "evaluation", "manifest.json"), manifest)
	writeJSON(t, scenarioPath(root), scenario)

	_, err := Check(root, "evaluation/manifest.json", fixtureNow())
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired exception error = %v", err)
	}
}

func contractFixture(t *testing.T) (string, []byte, []byte) {
	t.Helper()
	root := t.TempDir()
	schemaDir := filepath.Join(root, "evaluation", "schema")
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"manifest.schema.json", "scenario.schema.json"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "schema", name))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(schemaDir, name), data)
	}
	manifest, err := os.ReadFile(filepath.Join("..", "..", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := os.ReadFile(filepath.Join(
		"..",
		"..",
		"scenarios",
		"contract-self-check",
		"scenario.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	replayScenario, err := os.ReadFile(filepath.Join(
		"..",
		"..",
		"scenarios",
		"replay-corpus-check",
		"scenario.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(
		root,
		"evaluation",
		"scenarios",
		"replay-corpus-check",
		"scenario.json",
	), replayScenario)
	oracleScenario, err := os.ReadFile(filepath.Join(
		"..",
		"..",
		"scenarios",
		"oracle-core-check",
		"scenario.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(
		root,
		"evaluation",
		"scenarios",
		"oracle-core-check",
		"scenario.json",
	), oracleScenario)
	return root, manifest, scenario
}

func scenarioPath(root string) string {
	return filepath.Join(
		root,
		"evaluation",
		"scenarios",
		"contract-self-check",
		"scenario.json",
	)
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, append(data, '\n'))
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixtureNow() time.Time {
	return time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
}
