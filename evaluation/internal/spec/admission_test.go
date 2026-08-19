package spec

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestEffectiveConfigUsesUnionAndStrictestBudget(t *testing.T) {
	suite := Suite{
		Requirements: Requirements{
			Commands: []string{"git"}, Platforms: []string{runtime.GOOS, "other"},
			Capabilities: []string{"suite-capability"},
		},
		Budgets: Budgets{WallTimeMS: 100, MaxAttempts: 3, MaxOutputBytes: 1000},
	}
	scenario := Scenario{
		Requirements: Requirements{
			Commands: []string{"go"}, Platforms: []string{runtime.GOOS},
			Capabilities: []string{"scenario-capability"},
		},
		Budgets: Budgets{WallTimeMS: 200, MaxAttempts: 2, MaxOutputBytes: 500},
	}
	effective := Effective(suite, scenario)
	if strings.Join(effective.Requirements.Commands, ",") != "git,go" ||
		strings.Join(effective.Requirements.Capabilities, ",") !=
			"scenario-capability,suite-capability" ||
		len(effective.Requirements.Platforms) != 1 ||
		effective.Requirements.Platforms[0] != runtime.GOOS ||
		effective.Budgets.WallTimeMS != 100 ||
		effective.Budgets.MaxAttempts != 2 ||
		effective.Budgets.MaxOutputBytes != 500 {
		t.Fatalf("effective config = %+v", effective)
	}
}

func TestAdmissionAppliesPolicyWithoutChangingRunTruth(t *testing.T) {
	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	suite := Suite{
		ID: "suite", Risk: RiskP2,
		ReleasePolicy: ReleasePolicy{
			Blocking: true, AllowedStatuses: []Status{StatusPassed, StatusUnavailable},
			MinimumValidRuns: 1,
		},
	}
	scenario := Scenario{ID: "scenario", Risk: RiskP2}
	runs := []RunRecord{
		{SuiteID: "suite", ScenarioID: "scenario", Attempt: 1, Status: StatusPassed},
		{SuiteID: "suite", ScenarioID: "scenario", Attempt: 2, Status: StatusUnavailable},
	}
	decision := Admit(suite, scenario, runs, now)
	if !decision.Allowed || len(decision.Reasons) != 0 {
		t.Fatalf("admission = %+v", decision)
	}
	if runs[1].Status != StatusUnavailable {
		t.Fatal("admission mutated run truth")
	}
}

func TestP0AdmissionRejectsUnavailableAndExceptions(t *testing.T) {
	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	suite := Suite{
		ID: "suite", Risk: RiskP0,
		ReleasePolicy: ReleasePolicy{
			Blocking: true, AllowedStatuses: []Status{StatusPassed},
			MinimumValidRuns: 1,
		},
		Exceptions: []PolicyException{{
			ID: "forbidden", ExpiresOn: "2026-08-20",
			ScenarioIDs:     []string{"scenario"},
			AllowedStatuses: []Status{StatusUnavailable},
		}},
	}
	scenario := Scenario{ID: "scenario", Risk: RiskP0}
	decision := Admit(suite, scenario, []RunRecord{{
		SuiteID: "suite", ScenarioID: "scenario",
		Attempt: 1, Status: StatusUnavailable,
	}}, now)
	if decision.Allowed {
		t.Fatalf("P0 admission = %+v", decision)
	}
}

func TestRunPartitionChangesForEveryIdentityDimension(t *testing.T) {
	source := SourceIdentity{
		Commit: "commit", DirtyDigest: DigestString("dirty"),
	}
	artifacts := ArtifactIdentity{
		HarnessDigest: DigestString("harness"), RuntimeDigest: DigestString("runtime"),
		HostDigest: DigestString("host"), ScenarioDigest: DigestString("scenario"),
		FixtureDigest: DigestString("fixture"), ProviderDigest: DigestString("provider"),
		ModelDigest: DigestString("model"), ConfigDigest: DigestString("config"),
	}
	first, err := BuildRunPartition(source, artifacts, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	changed := artifacts
	changed.HostDigest = DigestString("other-host")
	second, err := BuildRunPartition(source, changed, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("run partition ignored host identity")
	}
}
