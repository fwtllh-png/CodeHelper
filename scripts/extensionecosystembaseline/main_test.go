package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryExtensionEcosystemBaseline(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	measured, err := measure(root, "test-commit")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := readReport(
		filepath.Join(root, "docs/extension-ecosystem-ee0-baseline.json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCandidate(baseline, measured); err != nil {
		t.Fatal(err)
	}
	if len(measured.SkillScales) != 4 ||
		measured.SkillScales[3].Discovered != 1000 ||
		!measured.SkillScales[3].PromptTruncated {
		t.Fatalf("skill scales = %+v", measured.SkillScales)
	}
	if len(measured.PluginScales) != 3 ||
		measured.PluginScales[2].Discovered != 100 {
		t.Fatalf("plugin scales = %+v", measured.PluginScales)
	}
	if !measured.CombinedGolden.FixtureHermetic ||
		len(measured.CombinedGolden.ModelTools) != 3 {
		t.Fatalf("combined golden = %+v", measured.CombinedGolden)
	}
	if !measured.Contracts.PluginSkillProductionReachable {
		t.Fatal("plugin skills are not reachable through production wiring")
	}
	if !measured.Contracts.UnifiedExtensionLifecycle {
		t.Fatal("unified extension lifecycle contract was not detected")
	}
}

func TestDurationStatsAndAdaptiveSampling(t *testing.T) {
	stats := calculateDurationStats([]int64{100, 100, 110, 120, 1000})
	if stats.Runs != 5 || stats.MedianMicros != 110 ||
		stats.P95Micros != 1000 || stats.MADMicros != 10 {
		t.Fatalf("duration stats = %+v", stats)
	}
	runs := 0
	adaptive, err := stableDuration(func() error {
		runs++
		if runs%2 == 0 {
			for range 1000 {
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if adaptive.Runs != initialRuns && adaptive.Runs != extendedRuns {
		t.Fatalf("adaptive runs = %d", adaptive.Runs)
	}
}

func TestValidateCandidateAllowsMonotonicContractImprovement(t *testing.T) {
	baseline := fixtureReport()
	candidate := fixtureReport()
	candidate.Contracts.TypedExtensionAPI = true
	candidate.KnownGaps = knownGaps(candidate.Contracts)
	if err := validateCandidate(baseline, candidate); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCandidateRejectsContractRegression(t *testing.T) {
	baseline := fixtureReport()
	candidate := fixtureReport()
	candidate.Contracts.SharedToolRegistry = false
	err := validateCandidate(baseline, candidate)
	if err == nil || !strings.Contains(err.Error(), "SharedToolRegistry") {
		t.Fatalf("validateCandidate() error = %v", err)
	}
}

func TestPluginSkillReachabilityDetection(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "fixture.go")
	source := `package fixture
import "example/skill"
func build() {
	_ = skill.DiscoveryOptions{Workspace: ".", Plugins: snapshots}
}`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	reachable, err := pluginSkillProductionReachable(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reachable {
		t.Fatal("plugin skill wiring was not detected")
	}
}

func TestLocalLifecycleRestartAndRevoke(t *testing.T) {
	restart, revoke, err := measureLocalLifecycle(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !restart || !revoke {
		t.Fatalf("restart=%t revoke=%t", restart, revoke)
	}
}

func fixtureReport() report {
	return report{
		SchemaVersion: schemaVersion,
		Stage:         stageEE0,
		SkillScales: []skillScale{
			{Count: 0, Discovered: 0},
			{Count: 10, Discovered: 10},
			{Count: 100, Discovered: 100},
			{
				Count: 1000, Discovered: 1000,
				TokenSavingsBP: 8000, CriticalSkillRecall: true,
			},
		},
		PluginScales: []pluginScale{
			{Count: 0, Discovered: 0},
			{Count: 10, Discovered: 10},
			{Count: 100, Discovered: 100},
		},
		CombinedGolden: combinedGolden{
			Digest: "sha256:fixture", FixtureHermetic: true,
			ModelTools: []string{"load_skill"},
		},
		LifecycleGoldens: []lifecycleGolden{{
			Scenario: "fixture", Measured: true,
		}},
		Contracts: contractMetrics{
			SharedToolRegistry: true,
		},
	}
}
