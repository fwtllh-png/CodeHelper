package corepack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/evidence"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/replay"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

func TestRepositoryCorePack(t *testing.T) {
	bundle, err := Load(
		filepath.Join("..", "..", ".."),
		"evaluation/scenarios/core/pack.json",
		"evaluation/impact-map.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	report, err := bundle.Check()
	if err != nil {
		t.Fatal(err)
	}
	if report.Scenarios != 36 || report.Families != 36 ||
		report.P0Scenarios != 18 || report.FaultCases != 13 ||
		report.OracleRuns != 81 || report.MutationRuns != 48 {
		t.Fatalf("core pack report = %+v", report)
	}
}

func TestScenarioRequiresApplicableOracleClosure(t *testing.T) {
	bundle := repositoryBundle(t)
	bundle.Pack.Scenarios[0].RequiredOracles = nil
	err := bundle.Validate()
	if err == nil || !strings.Contains(err.Error(), "oracle list is empty") {
		t.Fatalf("P0 oracle error = %v", err)
	}
}

func TestMinimumFamiliesAndImpactCoverageAreEnforced(t *testing.T) {
	bundle := repositoryBundle(t)
	bundle.Pack.Scenarios = bundle.Pack.Scenarios[:29]
	err := bundle.Validate()
	if err == nil || !strings.Contains(err.Error(), "minimum is 30") {
		t.Fatalf("family denominator error = %v", err)
	}

	bundle = repositoryBundle(t)
	bundle.Pack.Scenarios[0].ImpactTags = []string{"unmapped"}
	err = bundle.Validate()
	if err == nil || !strings.Contains(err.Error(), "not selected") {
		t.Fatalf("impact coverage error = %v", err)
	}
}

func TestFixtureTruthAndFaultCoverageAreMandatory(t *testing.T) {
	bundle := repositoryBundle(t)
	bundle.Pack.Scenarios[1].FixtureID = bundle.Pack.Scenarios[0].FixtureID
	if err := bundle.Validate(); err == nil ||
		!strings.Contains(err.Error(), "duplicate core fixture") {
		t.Fatalf("duplicate fixture error = %v", err)
	}

	bundle = repositoryBundle(t)
	bundle.Pack.FaultCases = nil
	if err := bundle.Validate(); err == nil ||
		!strings.Contains(err.Error(), "fault-case coverage") {
		t.Fatalf("empty fault matrix error = %v", err)
	}
}

func TestRequiredMutationNeedsEligibleExecution(t *testing.T) {
	bundle := repositoryBundle(t)
	for index := range bundle.Pack.Scenarios {
		if slices.Contains(
			bundle.Pack.Scenarios[index].RequiredMutations,
			replay.MutationSplit,
		) {
			bundle.Pack.Scenarios[index].RequiredMutations = []replay.MutationKind{
				replay.MutationSplit,
			}
			bundle.Pack.Scenarios[index].ImpactTags = []string{"runtime"}
			events, err := mutationFixture(bundle.Pack.Scenarios[index])
			if err != nil {
				t.Fatal(err)
			}
			events[0].Source = evidence.SourceRuntime
			events, err = evidence.Seal(events)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := replay.VerifyMutationCoverage(
				events,
				[]replay.MutationKind{replay.MutationSplit},
			); err == nil || !strings.Contains(err.Error(), "zero eligible") {
				t.Fatalf("zero split error = %v", err)
			}
			return
		}
	}
	t.Fatal("repository pack has no Provider Split scenario")
}

func TestImpactSelectionIsDeterministicAndRiskAware(t *testing.T) {
	bundle := repositoryBundle(t)
	selected, err := bundle.Select([]string{
		"internal/security/policy/policy.go",
		"internal/persist/state/store.go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) == 0 {
		t.Fatal("impact selection returned no scenarios")
	}
	ids := make([]string, len(selected))
	for index, scenario := range selected {
		ids[index] = scenario.ID
	}
	if !slices.IsSorted(ids) {
		t.Fatalf("impact selection is not sorted: %v", ids)
	}
	for _, required := range []string{
		"security-guard",
		"persistence-replay-rebuild",
		"tool-consequential-once",
	} {
		if !slices.Contains(ids, required) {
			t.Fatalf("impact selection omits %s: %v", required, ids)
		}
	}
}

func TestImpactSelectionFallsBackForUnknownProductPath(t *testing.T) {
	bundle := repositoryBundle(t)
	report, err := bundle.SelectDetailed([]string{"unknown/product/file.go"})
	if err != nil {
		t.Fatal(err)
	}
	selected := report.Scenarios
	if len(selected) != 18 {
		t.Fatalf("fallback selected %d scenarios, want 18 P0", len(selected))
	}
	for _, scenario := range selected {
		if scenario.Risk != spec.RiskP0 {
			t.Fatalf("fallback selected non-P0 scenario %s", scenario.ID)
		}
	}
	if !slices.Equal(report.FallbackPaths, []string{"unknown/product/file.go"}) {
		t.Fatalf("fallback paths = %v", report.FallbackPaths)
	}
}

func TestImpactSelectionExplainsDocumentationExclusion(t *testing.T) {
	bundle := repositoryBundle(t)
	report, err := bundle.SelectDetailed([]string{"docs/en/architecture.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Scenarios) != 0 ||
		!slices.Equal(report.ExcludedPaths, []string{"docs/en/architecture.md"}) {
		t.Fatalf("documentation selection = %+v", report)
	}
}

func TestFaultExpectationDriftFailsPackCheck(t *testing.T) {
	bundle := repositoryBundle(t)
	bundle.Pack.FaultCases[0].ExpectedSignature = "runtime:wrong"
	_, err := bundle.Check()
	if err == nil || !strings.Contains(err.Error(), "fault case") {
		t.Fatalf("fault expectation error = %v", err)
	}
}

func TestLoadRejectsUnknownPackField(t *testing.T) {
	root := fixtureRoot(t)
	packPath := filepath.Join(root, "evaluation", "scenarios", "core", "pack.json")
	raw, err := os.ReadFile(packPath)
	if err != nil {
		t.Fatal(err)
	}
	var pack map[string]any
	decodeErr := json.Unmarshal(raw, &pack)
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	pack["unknown"] = true
	raw, _ = json.Marshal(pack)
	writeErr := os.WriteFile(packPath, raw, 0o600)
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	_, err = Load(
		root,
		"evaluation/scenarios/core/pack.json",
		"evaluation/impact-map.json",
	)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown pack field error = %v", err)
	}
}

func repositoryBundle(t *testing.T) Bundle {
	t.Helper()
	bundle, err := Load(
		filepath.Join("..", "..", ".."),
		"evaluation/scenarios/core/pack.json",
		"evaluation/impact-map.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func fixtureRoot(t *testing.T) string {
	t.Helper()
	source := filepath.Join("..", "..", "..")
	root := t.TempDir()
	for _, relative := range []string{
		"evaluation/scenarios/core/pack.json",
		"evaluation/impact-map.json",
	} {
		data, err := os.ReadFile(filepath.Join(source, relative))
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
