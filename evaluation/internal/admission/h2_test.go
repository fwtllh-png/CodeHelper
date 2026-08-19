package admission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestRepositoryH2CatalogHasCompleteLiveDenominator(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	catalog, err := LoadH2(root, "evaluation/spec/h2-execution.json")
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, scenario := range catalog.Scenarios {
		total += scenario.Repetitions
	}
	if total != 12 {
		t.Fatalf("H2 live samples = %d, want 12", total)
	}
}

func TestH2CatalogSchemaValidatesRepositoryAsset(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	schemaRaw, err := os.ReadFile(filepath.Join(
		root,
		"evaluation/schema/h2-execution.schema.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var schemaValue any
	if err := json.Unmarshal(schemaRaw, &schemaValue); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("h2-execution.schema.json", schemaValue); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("h2-execution.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	catalogRaw, err := os.ReadFile(filepath.Join(
		root,
		"evaluation/spec/h2-execution.json",
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

func TestAggregateH2BindsIdentityConfidenceCostLatencyAndDrift(t *testing.T) {
	output := t.TempDir()
	catalog := testH2Catalog()
	for _, scenario := range catalog.Scenarios {
		for sample := 1; sample <= scenario.Repetitions; sample++ {
			evidence := testH2Evidence(scenario, sample)
			if err := writePrivateJSON(
				H2EvidencePath(output, scenario.ID, sample),
				evidence,
			); err != nil {
				t.Fatal(err)
			}
		}
	}
	summary, err := AggregateH2(
		output,
		catalog,
		"h2-test",
		digestH2("source"),
		digestH2("lock"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Scheduled != 12 || summary.Passed != 12 ||
		summary.TotalCostMicrounits != 12_000 ||
		!digestValidH2(summary.EvidenceDigest) {
		t.Fatalf("H2 summary = %+v", summary)
	}
	if summary.Scenarios[0].ConfidenceLowerBasisPts < 6700 ||
		summary.Scenarios[1].ConfidenceLowerBasisPts < 5000 {
		t.Fatalf("H2 confidence = %+v", summary.Scenarios)
	}
	info, err := os.Stat(filepath.Join(output, "h2-summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("H2 summary mode = %o", info.Mode().Perm())
	}
}

func TestAggregateH2RejectsPartitionDrift(t *testing.T) {
	output := t.TempDir()
	catalog := testH2Catalog()
	for _, scenario := range catalog.Scenarios {
		for sample := 1; sample <= scenario.Repetitions; sample++ {
			evidence := testH2Evidence(scenario, sample)
			if scenario.ID == "multi-agent" && sample == 2 {
				evidence.Model = "other-model"
			}
			if err := writePrivateJSON(
				H2EvidencePath(output, scenario.ID, sample),
				evidence,
			); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := AggregateH2(
		output,
		catalog,
		"h2-test",
		digestH2("source"),
		digestH2("lock"),
	); err == nil {
		t.Fatal("H2 aggregation accepted model drift")
	}
}

func TestAggregateH2PreservesStructuredFailureReason(t *testing.T) {
	output := t.TempDir()
	catalog := testH2Catalog()
	for _, scenario := range catalog.Scenarios {
		for sample := 1; sample <= scenario.Repetitions; sample++ {
			evidence := testH2Evidence(scenario, sample)
			if scenario.ID == "multi-agent" && sample == 3 {
				evidence.Status = "failed"
				evidence.FailureReason = "spawn_count_mismatch"
				evidence.AgentSpawnCount = 1
			}
			if err := writePrivateJSON(
				H2EvidencePath(output, scenario.ID, sample),
				evidence,
			); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, err := AggregateH2(
		output,
		catalog,
		"h2-test",
		digestH2("source"),
		digestH2("lock"),
	)
	if err == nil ||
		!strings.Contains(err.Error(), "failed: spawn_count_mismatch") {
		t.Fatalf("AggregateH2 error = %v", err)
	}
}

func testH2Catalog() H2Catalog {
	return H2Catalog{
		SchemaVersion: H2SchemaVersion,
		Provider:      "deepseek-v4-flash",
		Model:         "deepseek-v4-flash",
		Protocol:      "openai_responses",
		Policy: H2Policy{
			MaxTotalCostMicrounits: 1_000_000,
			RequireCostKnown:       true,
			PricingContract:        "deepseek_v4_2026_08_17",
		},
		Scenarios: []H2Scenario{
			{
				ID: "exact-response", Mode: "single", Repetitions: 8,
				Command:                    []string{"make", "deepseek-live-smoke"},
				ExpectedTextSHA256:         digestH2("single"),
				MinConfidenceLowerBasisPts: 6700,
				MaxP95LatencyMS:            180_000,
				MaxCostMicrounits:          50_000,
				MaxEventShapeVariants:      2,
			},
			{
				ID: "multi-agent", Mode: "multi_agent", Repetitions: 4,
				Command:                    []string{"make", "deepseek-multi-agent-smoke"},
				ExpectedTextSHA256:         digestH2("multi"),
				MinConfidenceLowerBasisPts: 5000,
				MaxP95LatencyMS:            300_000,
				MaxCostMicrounits:          200_000,
				MaxEventShapeVariants:      3,
			},
		},
	}
}

func testH2Evidence(scenario H2Scenario, sample int) H2LiveEvidence {
	return H2LiveEvidence{
		SchemaVersion: H2EvidenceSchemaVersion,
		Stage:         "h2_live", Status: "passed", FailureReason: "none",
		QualificationID: "h2-test",
		ScenarioID:      scenario.ID, SampleIndex: sample,
		SourceDigest: digestH2("source"), LockIdentity: digestH2("lock"),
		EndpointHostSHA256: digestH2("endpoint"),
		Provider:           "deepseek-v4-flash", Model: "deepseek-v4-flash",
		Protocol: "openai_responses", PricingWindow: "off_peak",
		ConfigSHA256:  digestH2("config"),
		MultiAgent:    scenario.Mode == "multi_agent",
		TerminalEvent: "turn.completed", TerminalCount: 1,
		TextAssertionSHA: scenario.ExpectedTextSHA256,
		EventShapeSHA256: digestH2("shape"),
		UsageSamples:     1, InputTokens: 100, OutputTokens: 10,
		CostMicrounits: 1000, CostKnown: true, DurationMS: 100,
		AgentSpawnCount: func() int {
			if scenario.Mode == "multi_agent" {
				return 2
			}
			return 0
		}(),
		AgentTerminalCount: func() int {
			if scenario.Mode == "multi_agent" {
				return 2
			}
			return 0
		}(),
		AgentCompleted: func() int {
			if scenario.Mode == "multi_agent" {
				return 2
			}
			return 0
		}(),
	}
}
