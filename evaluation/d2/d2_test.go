package d2

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/foundation"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/freeze"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/qualification"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

func TestRepositoryD2CampaignClosesDeclaredCoverage(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	bundle, err := LoadCampaign(root, "evaluation/spec/d2-campaign.json")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(bundle.Campaign)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Campaign.Families) != 7 ||
		len(plan.Cases) != 129 ||
		plan.Coverage.PairwiseCovered != 539 ||
		plan.Coverage.PairwiseCovered != plan.Coverage.PairwiseTotal ||
		plan.Coverage.RequiredCombinationCovered !=
			plan.Coverage.RequiredCombinationTotal ||
		plan.Coverage.BoundaryCovered != plan.Coverage.BoundaryTotal ||
		plan.Coverage.FaultTriggerCovered != plan.Coverage.FaultTriggerTotal {
		t.Fatalf("D2 plan coverage = %+v, cases=%d", plan.Coverage, len(plan.Cases))
	}
	repeated, err := BuildPlan(bundle.Campaign)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan, repeated) {
		t.Fatal("D2 planner produced a different same-seed inventory")
	}
}

func TestD2DriverInventoryIsDeterministicAndClosesDeclaredControls(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	bundle, err := LoadCampaign(root, "evaluation/spec/d2-campaign.json")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(bundle.Campaign)
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := BuildDriverInventory(bundle.Campaign, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Cases) != 129 ||
		len(inventory.Drivers) != 3 ||
		len(inventory.Faults) != 6 {
		t.Fatalf(
			"D2 Driver inventory cases=%d drivers=%d faults=%d",
			len(inventory.Cases),
			len(inventory.Drivers),
			len(inventory.Faults),
		)
	}
	repeated, err := BuildDriverInventory(bundle.Campaign, plan)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inventory, repeated) {
		t.Fatal("D2 Driver inventory changed for the same plan and seeds")
	}
	raw, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSchemaFile(
		root,
		"evaluation/schema/discovery-driver-inventory.schema.json",
		raw,
	); err != nil {
		t.Fatal(err)
	}
}

func TestD2DriverInventoryRejectsMissingFaultAndCleanupEvidence(t *testing.T) {
	campaign := repositoryCampaign(t)
	plan, err := BuildPlan(campaign)
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := BuildDriverInventory(campaign, plan)
	if err != nil {
		t.Fatal(err)
	}
	missingFault := inventory
	missingFault.Faults = append([]FaultDefinition(nil), inventory.Faults[1:]...)
	missingFault.EvidenceDigest = digestDriverInventory(missingFault)
	if err := missingFault.Validate(); err == nil {
		t.Fatal("D2 Driver inventory accepted a missing fault control")
	}
	missingCleanup := inventory
	missingCleanup.Cases = append([]GeneratedCase(nil), inventory.Cases...)
	missingCleanup.Cases[0].Cleanup = nil
	missingCleanup.Cases[0].EvidenceDigest = digestGeneratedCase(
		missingCleanup.Cases[0],
	)
	missingCleanup.EvidenceDigest = digestDriverInventory(missingCleanup)
	if err := missingCleanup.Validate(); err == nil {
		t.Fatal("D2 Driver inventory accepted missing cleanup evidence")
	}
}

func TestD2RuntimeControlsTriggerAndSyntheticReplayIsExact(t *testing.T) {
	campaign := repositoryCampaign(t)
	plan, err := BuildPlan(campaign)
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := BuildDriverInventory(campaign, plan)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := runFaultControlProbes(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range requiredFaultIDs {
		if hits[id] != 1 {
			t.Fatalf("D2 fault %q hits=%d", id, hits[id])
		}
	}
	var selected GeneratedCase
	for _, generated := range inventory.Cases {
		if generated.Workload.Files > selected.Workload.Files {
			selected = generated
		}
	}
	first, err := MaterializeSyntheticRepository(t.TempDir(), selected)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MaterializeSyntheticRepository(t.TempDir(), selected)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) ||
		first.Files != selected.Workload.Files {
		t.Fatalf("D2 synthetic Replay first=%+v second=%+v", first, second)
	}
}

func TestD2CampaignRoundSchemaAndIdentityAreStrict(t *testing.T) {
	now := time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC)
	result := CaseResult{
		ID: "case-001", FamilyID: "stateful_journey",
		DriverID: "cli", Seed: 101, Status: "passed", Attempts: 1,
		DurationMS: 10, FaultsPlanned: []string{},
		FaultsTriggered: []string{},
		StepsPlanned: []string{
			"prepare_workspace", "start_runtime",
			"submit_prompt", "observe_terminal",
		},
		StepsExecuted: []string{
			"prepare_workspace", "start_runtime",
			"submit_prompt", "observe_terminal",
		},
		ScheduleDigest:  spec.DigestString("schedule"),
		WorkspaceDigest: spec.DigestString("workspace"),
		ExecutionDigest: spec.DigestString("execution"),
		CleanupDigest:   spec.DigestString("cleanup"),
		SummaryCode:     "completed",
	}
	result.EvidenceDigest = digestCaseResult(result)
	round := CampaignRound{
		SchemaVersion: SchemaVersion, ID: "d2-round-test",
		CampaignID:            "complex-scenario-discovery-v1",
		DiscoveryLockIdentity: spec.DigestString("lock"),
		DriverInventoryDigest: spec.DigestString("inventory"),
		Status:                "closed", Scheduled: 1, Settled: 1, Passed: 1,
		StartedAt: now, FinishedAt: now.Add(time.Second),
		MaxParallelism: 1, WallTimeMS: 1000,
		Cases: []CaseResult{result}, Observations: []Observation{},
		ResourceClosure: spec.DigestString("resources"),
		PrivacyClosure:  spec.DigestString("privacy"),
	}
	round.EvidenceDigest = digestCampaignRound(round)
	if err := round.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(round)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join("..", ".."))
	if err := validateSchemaFile(
		root,
		"evaluation/schema/discovery-round.schema.json",
		raw,
	); err != nil {
		t.Fatal(err)
	}
	round.Settled = 0
	round.EvidenceDigest = digestCampaignRound(round)
	if err := round.Validate(); err == nil {
		t.Fatal("D2 Campaign Round accepted an unsettled denominator")
	}
	_, _, _, _, faults := failedExecution(
		"failed",
		"production_boundary_failed",
		errors.New("injected"),
	)
	if faults == nil {
		t.Fatal("D2 failed execution emitted null fault evidence")
	}
	if merged := mergeStrings([]string{}, []string{}); merged == nil {
		t.Fatal("D2 retry evidence merged empty faults into null")
	}
	generated := GeneratedCase{
		DriverID: "acp",
		Values: map[string]string{
			"session_state": "clean_session",
			"lifecycle":     "crash_recovery",
		},
		Steps: []JourneyStep{
			{Action: "prepare_workspace"},
			{Action: "start_runtime"},
			{Action: "submit_prompt"},
			{Action: "crash_runtime"},
			{Action: "restart_runtime"},
			{Action: "reconnect_session"},
			{Action: "observe_terminal"},
		},
	}
	if slices.Equal(plannedSteps(generated), attestedSteps(generated, true)) {
		t.Fatal("D2 step attestation claimed an unexecuted crash-recovery Journey")
	}
	if got := caseModelCost(CaseResult{
		Live: true, Status: "invalid", Attempts: 2,
		SummaryCode: "journey_step_unexecuted",
	}); got != 100_000 {
		t.Fatalf("D2 Live retry cost=%d want 100000", got)
	}
}

func TestD2DriverQualificationBindsCandidateLock(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	bundle, err := LoadCampaign(root, "evaluation/spec/d2-campaign.json")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(bundle.Campaign)
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := BuildDriverInventory(bundle.Campaign, plan)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := BuildDiscoveryLock(LockOptions{
		Root: root, ID: "d2-driver-qualification",
		Base: testFrozenBaseLock(t, root), Campaign: bundle, Plan: plan,
		InputRoots: DefaultInputRoots(),
	})
	if err != nil {
		t.Fatal(err)
	}
	foundation, err := RunQualification(
		root,
		"d2-driver-qualification",
		bundle,
		plan,
		lock,
	)
	if err != nil {
		t.Fatal(err)
	}
	report := DriverQualificationReport{
		SchemaVersion:                 SchemaVersion,
		ID:                            "d2-driver-qualification",
		DiscoveryLockIdentity:         lock.LockIdentity,
		FoundationQualificationDigest: foundation.EvidenceDigest,
		DriverInventoryDigest:         inventory.EvidenceDigest,
		Status:                        "passed",
		Scheduled:                     len(requiredDriverQualificationChecks),
		Settled:                       len(requiredDriverQualificationChecks),
		Passed:                        len(requiredDriverQualificationChecks),
	}
	for _, id := range requiredDriverQualificationChecks {
		report.Checks = append(report.Checks, QualificationCheck{
			ID: id, Status: "passed",
			EvidenceDigest: spec.DigestString(id),
		})
	}
	report.EvidenceDigest = digestDriverQualification(report)
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
	qualified, err := QualifyDriverLock(lock, report)
	if err != nil {
		t.Fatal(err)
	}
	if qualified.Status != "qualified" ||
		qualified.QualificationDigest != report.EvidenceDigest {
		t.Fatalf("D2 Driver qualified Lock = %+v", qualified)
	}
	report.DiscoveryLockIdentity = spec.DigestString("wrong-lock")
	report.EvidenceDigest = digestDriverQualification(report)
	if _, err := QualifyDriverLock(lock, report); err == nil {
		t.Fatal("D2 Driver Qualification accepted the wrong Lock")
	}
}

func TestD2CampaignRejectsInvalidContracts(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	bundle, err := LoadCampaign(root, "evaluation/spec/d2-campaign.json")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*Campaign)
	}{
		{"empty-axis", func(value *Campaign) { value.Axes = nil }},
		{"zero-case", func(value *Campaign) { value.Budgets.MaxRuns = 0 }},
		{"unbounded-wall-time", func(value *Campaign) {
			value.Budgets.WallTimeMS = 86_400_001
		}},
		{"undeclared-adaptive", func(value *Campaign) {
			value.Adaptive = true
			value.AdaptivePolicy = ""
		}},
		{"missing-cleanup", func(value *Campaign) {
			value.Cleanup.Resources = nil
		}},
		{"private-content", func(value *Campaign) {
			value.Privacy.PersistUserContent = true
		}},
		{"missing-family", func(value *Campaign) {
			value.Families = value.Families[:len(value.Families)-1]
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			campaign := cloneCampaign(bundle.Campaign)
			test.mutate(&campaign)
			if err := campaign.Validate(); err == nil {
				t.Fatalf("D2 campaign accepted %s", test.name)
			}
		})
	}
	var raw map[string]any
	if err := json.Unmarshal(bundle.Raw, &raw); err != nil {
		t.Fatal(err)
	}
	raw["unknown"] = true
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var campaign Campaign
	if err := decodeStrict(encoded, &campaign); err == nil {
		t.Fatal("D2 campaign accepted an unknown field")
	}
}

func TestD2QualificationProducesQualifiedIndependentLock(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	bundle, err := LoadCampaign(root, "evaluation/spec/d2-campaign.json")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(bundle.Campaign)
	if err != nil {
		t.Fatal(err)
	}
	base := testFrozenBaseLock(t, root)
	candidate, err := BuildDiscoveryLock(LockOptions{
		Root: root, ID: "d2-foundation-01",
		Base: base, Campaign: bundle, Plan: plan,
		InputRoots: DefaultInputRoots(),
		Now: func() time.Time {
			return time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.BaseLockIdentity != base.LockIdentity ||
		candidate.Status != "candidate" ||
		candidate.QualificationDigest != "" {
		t.Fatalf("candidate Discovery Lock = %+v", candidate)
	}
	for schema, value := range map[string]any{
		"evaluation/schema/discovery-plan.schema.json": plan,
		"evaluation/schema/discovery-lock.schema.json": candidate,
	} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateSchemaFile(root, schema, raw); err != nil {
			t.Fatalf("%s candidate parity: %v", schema, err)
		}
	}
	report, err := RunQualification(
		root,
		"d2-foundation-01",
		bundle,
		plan,
		candidate,
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "passed" ||
		report.Scheduled != len(requiredQualificationChecks) ||
		report.Passed != report.Scheduled {
		t.Fatalf("D2 qualification = %+v", report)
	}
	qualified, err := QualifyDiscoveryLock(candidate, report)
	if err != nil {
		t.Fatal(err)
	}
	if qualified.Status != "qualified" ||
		qualified.LockIdentity != candidate.LockIdentity ||
		qualified.QualificationDigest != report.EvidenceDigest {
		t.Fatalf("qualified Discovery Lock = %+v", qualified)
	}
	output := filepath.Join(t.TempDir(), "qualification")
	if err := WriteQualificationBundle(output, plan, report, qualified); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"campaign-plan.json",
		"discovery-qualification.json",
		"discovery-lock.json",
	} {
		info, err := os.Stat(filepath.Join(output, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o", name, info.Mode().Perm())
		}
	}
	if err := WriteQualificationBundle(output, plan, report, qualified); err == nil {
		t.Fatal("D2 qualification bundle overwrote an existing Epoch")
	}
	for schema, value := range map[string]any{
		"evaluation/schema/discovery-lock.schema.json":          qualified,
		"evaluation/schema/discovery-qualification.schema.json": report,
	} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateSchemaFile(root, schema, raw); err != nil {
			t.Fatalf("%s: %v", schema, err)
		}
	}
}

func TestD2DiscoveryLockRejectsInputAndBaseIdentityDrift(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "evaluation", "d2"), 0o700); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(root, "evaluation", "d2", "contract.json")
	if err := os.WriteFile(input, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	campaign := repositoryCampaign(t)
	raw, err := json.Marshal(campaign)
	if err != nil {
		t.Fatal(err)
	}
	bundle := CampaignBundle{
		Root: root, Path: "evaluation/spec/d2-campaign.json",
		Raw: raw, Digest: spec.DigestString(string(raw)), Campaign: campaign,
	}
	plan, err := BuildPlan(campaign)
	if err != nil {
		t.Fatal(err)
	}
	base := testFrozenBaseLock(t, root)
	lock, err := BuildDiscoveryLock(LockOptions{
		Root: root, ID: "d2-drift",
		Base: base, Campaign: bundle, Plan: plan,
		InputRoots: []string{"evaluation/d2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	drifted := lock
	drifted.RuntimeDigest = spec.DigestString("other-runtime")
	if err := drifted.Validate(); err == nil {
		t.Fatal("D2 Discovery Lock accepted mixed base identity")
	}
	if err := os.WriteFile(input, []byte(`{"changed":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyDiscoveryInputs(root, lock); err == nil {
		t.Fatal("D2 Discovery Lock accepted changed D2 input")
	}
}

func TestD2ObservationRejectsPrivateOrUnboundEvidence(t *testing.T) {
	observation := Observation{
		SchemaVersion:         SchemaVersion,
		ID:                    "observation-001",
		CampaignID:            "campaign",
		CaseID:                "case-001",
		DiscoveryLockIdentity: spec.DigestString("lock"),
		EnvironmentDigest:     spec.DigestString("environment"),
		Producer:              "d2-driver",
		Classification:        "product_candidate",
		Severity:              "p1",
		Reproducibility:       "exact_seed",
		Attempts:              2,
		EvidenceDigests:       []string{spec.DigestString("evidence")},
		FirstObservedAt:       time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC),
		SummaryCode:           "terminal_cardinality",
	}
	if err := observation.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join("..", ".."))
	if err := validateSchemaFile(
		root,
		"evaluation/schema/discovery-observation.schema.json",
		raw,
	); err != nil {
		t.Fatal(err)
	}
	observation.ContainsPrivateContent = true
	if err := observation.Validate(); err == nil {
		t.Fatal("D2 observation accepted private content")
	}
	observation.ContainsPrivateContent = false
	observation.EvidenceDigests = nil
	if err := observation.Validate(); err == nil {
		t.Fatal("D2 observation accepted empty evidence")
	}
}

func repositoryCampaign(t *testing.T) Campaign {
	t.Helper()
	root := filepath.Clean(filepath.Join("..", ".."))
	bundle, err := LoadCampaign(root, "evaluation/spec/d2-campaign.json")
	if err != nil {
		t.Fatal(err)
	}
	return bundle.Campaign
}

func testFrozenBaseLock(t *testing.T, root string) freeze.Lock {
	t.Helper()
	artifacts := t.TempDir()
	evaluationBinary := writeTestFile(t, artifacts, "codehelper-eval", "evaluation")
	runtimeBinary := writeTestFile(t, artifacts, "codehelper", "runtime")
	vsix := filepath.Join(artifacts, "extension.vsix")
	file, err := os.Create(vsix)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	writer, err := archive.Create("extension/package.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte(`{"name":"codehelper"}`)); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	baseInputRoot := "evaluation/scenarios/contract-self-check"
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(baseInputRoot))); err != nil {
		baseInputRoot = "base-inputs"
		baseInput := filepath.Join(root, baseInputRoot)
		if err := os.MkdirAll(baseInput, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(baseInput, "fixture.json"),
			[]byte("{}"),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	lock, _, err := freeze.BuildCandidate(freeze.CandidateOptions{
		Root: root, ID: "base-lock",
		Source: spec.SourceIdentity{
			Commit: "commit",
			Dirty:  true, DirtyDigest: spec.DigestString("source"),
		},
		Foundation: foundation.Bundle{
			HarnessInputHash: spec.DigestString("foundation"),
		},
		EvaluationBinary: evaluationBinary,
		RuntimeBinary:    runtimeBinary,
		VSIX:             vsix,
		InputRoots:       []string{baseInputRoot},
		Now: func() time.Time {
			return time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 3; index++ {
		report := qualification.Report{
			SchemaVersion:    qualification.SchemaVersion,
			ID:               "integration-00" + string(rune('0'+index)),
			Kind:             "integration",
			FoundationDigest: lock.FoundationDigest,
			SourceDigest:     lock.SourceDigest,
			RuntimeDigest:    lock.RuntimeDigest,
			VSIXDigest:       lock.VSIXDigest,
			LockIdentity:     lock.LockIdentity,
			Status:           spec.StatusPassed,
			StartedAt:        time.Date(2026, 8, 20, 0, 0, index, 0, time.UTC),
			EndedAt:          time.Date(2026, 8, 20, 0, 0, index+1, 0, time.UTC),
			Scheduled:        1,
			Settled:          1,
			Passed:           1,
			Results: []qualification.TaskResult{{
				ID:                    "integration",
				Status:                spec.StatusPassed,
				EvidenceDigest:        spec.DigestString("task"),
				ReasonCode:            "passed",
				CleanupEvidenceDigest: spec.DigestString("cleanup"),
			}},
		}
		report.EvidenceDigest = qualification.Digest(report)
		lock, err = freeze.AppendIntegrationRun(lock, report)
		if err != nil {
			t.Fatal(err)
		}
	}
	return lock
}

func writeTestFile(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
