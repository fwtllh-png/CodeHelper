package d2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/runner"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

type CampaignRound struct {
	SchemaVersion         int           `json:"schema_version"`
	ID                    string        `json:"id"`
	CampaignID            string        `json:"campaign_id"`
	DiscoveryLockIdentity string        `json:"discovery_lock_identity"`
	DriverInventoryDigest string        `json:"driver_inventory_digest"`
	Status                string        `json:"status"`
	Scheduled             int           `json:"scheduled"`
	Settled               int           `json:"settled"`
	Passed                int           `json:"passed"`
	Failed                int           `json:"failed"`
	Unavailable           int           `json:"unavailable"`
	Invalid               int           `json:"invalid"`
	BudgetSkipped         int           `json:"budget_skipped"`
	StartedAt             time.Time     `json:"started_at"`
	FinishedAt            time.Time     `json:"finished_at"`
	MaxParallelism        int           `json:"max_parallelism"`
	WallTimeMS            int64         `json:"wall_time_ms"`
	ModelCostMicrounits   uint64        `json:"model_cost_microunits"`
	Cases                 []CaseResult  `json:"cases"`
	Observations          []Observation `json:"observations"`
	ResourceClosure       string        `json:"resource_closure"`
	PrivacyClosure        string        `json:"privacy_closure"`
	EvidenceDigest        string        `json:"evidence_digest"`
}

type CaseResult struct {
	ID              string   `json:"id"`
	FamilyID        string   `json:"family_id"`
	DriverID        string   `json:"driver_id"`
	Seed            uint64   `json:"seed"`
	Status          string   `json:"status"`
	Attempts        int      `json:"attempts"`
	DurationMS      int64    `json:"duration_ms"`
	Live            bool     `json:"live"`
	FaultsPlanned   []string `json:"faults_planned"`
	FaultsTriggered []string `json:"faults_triggered"`
	StepsPlanned    []string `json:"steps_planned"`
	StepsExecuted   []string `json:"steps_executed"`
	ScheduleDigest  string   `json:"schedule_digest"`
	WorkspaceDigest string   `json:"workspace_digest"`
	ExecutionDigest string   `json:"execution_digest"`
	CleanupDigest   string   `json:"cleanup_digest"`
	SummaryCode     string   `json:"summary_code"`
	EvidenceDigest  string   `json:"evidence_digest"`
}

type CampaignOptions struct {
	Root      string
	ID        string
	Output    string
	Runtime   string
	VSIX      string
	Extension string
	NPM       string
	Live      bool
	Lock      DiscoveryLock
	Campaign  Campaign
	Plan      Plan
	Inventory DriverInventory
	Now       func() time.Time
}

var liveCampaignMu sync.Mutex

func ReadPlan(path string) (Plan, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Plan{}, err
	}
	var plan Plan
	if err := decodeStrict(raw, &plan); err != nil {
		return Plan{}, err
	}
	if plan.EvidenceDigest != digestPlan(plan) || len(plan.Cases) == 0 {
		return Plan{}, errors.New("D2 Campaign Plan is invalid")
	}
	return plan, nil
}

func ReadDriverInventory(path string) (DriverInventory, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return DriverInventory{}, err
	}
	var inventory DriverInventory
	if err := decodeStrict(raw, &inventory); err != nil {
		return DriverInventory{}, err
	}
	return inventory, inventory.Validate()
}

func RunCampaignRound(
	ctx context.Context,
	options CampaignOptions,
) (CampaignRound, error) {
	if !validID(options.ID) || options.Lock.Status != "qualified" ||
		options.Inventory.EvidenceDigest == "" ||
		options.Inventory.PlanDigest != options.Plan.EvidenceDigest ||
		options.Lock.PlannerDigest != options.Plan.EvidenceDigest {
		return CampaignRound{}, errors.New("D2 Campaign identity is invalid")
	}
	if err := options.Lock.Validate(); err != nil {
		return CampaignRound{}, err
	}
	if err := options.Inventory.Validate(); err != nil {
		return CampaignRound{}, err
	}
	runtimeDigest, err := digestArtifact(options.Runtime)
	if err != nil {
		return CampaignRound{}, err
	}
	vsixDigest, err := digestArtifact(options.VSIX)
	if err != nil {
		return CampaignRound{}, err
	}
	if runtimeDigest != options.Lock.RuntimeDigest ||
		vsixDigest != options.Lock.VSIXDigest {
		return CampaignRound{}, errors.New("D2 Campaign artifact identity is mixed")
	}
	if _, err := VerifyDiscoveryInputs(options.Root, options.Lock); err != nil {
		return CampaignRound{}, err
	}
	if len(options.Inventory.Cases) != len(options.Plan.Cases) ||
		len(options.Inventory.Cases) > options.Campaign.Budgets.MaxRuns {
		return CampaignRound{}, errors.New("D2 Campaign denominator is invalid")
	}
	if options.NPM == "" {
		options.NPM = "npm"
	}
	if options.Extension == "" {
		options.Extension = filepath.Join(options.Root, "extensions", "vscode")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	started := now().UTC()
	round := CampaignRound{
		SchemaVersion:         SchemaVersion,
		ID:                    options.ID,
		CampaignID:            options.Campaign.ID,
		DiscoveryLockIdentity: options.Lock.LockIdentity,
		DriverInventoryDigest: options.Inventory.EvidenceDigest,
		Status:                "closed",
		Scheduled:             len(options.Inventory.Cases),
		StartedAt:             started,
		MaxParallelism:        options.Campaign.Budgets.MaxParallelism,
		Cases:                 make([]CaseResult, len(options.Inventory.Cases)),
		Observations:          []Observation{},
	}
	roundContext, cancel := context.WithTimeout(
		ctx,
		time.Duration(options.Campaign.Budgets.WallTimeMS)*time.Millisecond,
	)
	defer cancel()
	sem := make(chan struct{}, options.Campaign.Budgets.MaxParallelism)
	var wait sync.WaitGroup
	for index, generated := range options.Inventory.Cases {
		index, generated := index, generated
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-roundContext.Done():
				round.Cases[index] = skippedCase(generated, "round_budget_exhausted")
				return
			}
			round.Cases[index] = runCampaignCase(roundContext, options, generated)
		}()
	}
	wait.Wait()
	round.FinishedAt = now().UTC()
	if round.FinishedAt.Before(round.StartedAt) {
		round.FinishedAt = time.Now().UTC()
	}
	round.WallTimeMS = round.FinishedAt.Sub(round.StartedAt).Milliseconds()
	environmentDigest := spec.DigestString(strings.Join([]string{
		options.Lock.RuntimeDigest,
		options.Lock.VSIXDigest,
		options.Lock.LockIdentity,
	}, "\x00"))
	for _, result := range round.Cases {
		round.Settled++
		switch result.Status {
		case "passed":
			round.Passed++
		case "failed":
			round.Failed++
		case "unavailable":
			round.Unavailable++
		case "invalid":
			round.Invalid++
		case "budget_skipped":
			round.BudgetSkipped++
		}
		round.ModelCostMicrounits += caseModelCost(result)
		if result.Status != "passed" && result.Status != "budget_skipped" {
			round.Observations = append(round.Observations, observationFor(
				round,
				result,
				environmentDigest,
				now().UTC(),
			))
		}
	}
	slices.SortFunc(round.Observations, func(left, right Observation) int {
		return strings.Compare(left.ID, right.ID)
	})
	round.ResourceClosure = spec.DigestString(
		fmt.Sprintf("%s\x00resources\x00%d", round.ID, round.Settled),
	)
	round.PrivacyClosure = spec.DigestString(
		fmt.Sprintf("%s\x00privacy\x00%d", round.ID, len(round.Observations)),
	)
	round.EvidenceDigest = digestCampaignRound(round)
	return round, round.Validate()
}

func (r CampaignRound) Validate() error {
	if r.SchemaVersion != SchemaVersion || !validID(r.ID) ||
		!validID(r.CampaignID) || !validDigest(r.DiscoveryLockIdentity) ||
		!validDigest(r.DriverInventoryDigest) || r.Status != "closed" ||
		r.Scheduled < 1 || r.Settled != r.Scheduled ||
		len(r.Cases) != r.Scheduled ||
		r.Passed+r.Failed+r.Unavailable+r.Invalid+r.BudgetSkipped != r.Settled ||
		r.StartedAt.IsZero() || r.FinishedAt.IsZero() ||
		r.MaxParallelism < 1 || r.WallTimeMS < 0 ||
		!validDigest(r.ResourceClosure) || !validDigest(r.PrivacyClosure) ||
		!validDigest(r.EvidenceDigest) {
		return errors.New("D2 Campaign Round inventory is invalid")
	}
	seen := make(map[string]struct{}, len(r.Cases))
	for _, result := range r.Cases {
		if err := result.Validate(); err != nil {
			return err
		}
		if _, duplicate := seen[result.ID]; duplicate {
			return fmt.Errorf("duplicate D2 Campaign Case %q", result.ID)
		}
		seen[result.ID] = struct{}{}
	}
	for _, observation := range r.Observations {
		if err := observation.Validate(); err != nil {
			return err
		}
	}
	if r.EvidenceDigest != digestCampaignRound(r) {
		return errors.New("D2 Campaign Round digest is invalid")
	}
	return nil
}

func (r CaseResult) Validate() error {
	if !validID(r.ID) || !validID(r.FamilyID) ||
		!slices.Contains(requiredDriverIDs, r.DriverID) || r.Seed == 0 ||
		!slices.Contains([]string{
			"passed", "failed", "unavailable", "invalid", "budget_skipped",
		}, r.Status) ||
		r.Attempts < 1 || r.Attempts > 2 || r.DurationMS < 0 ||
		!validDigest(r.ScheduleDigest) || !validDigest(r.WorkspaceDigest) ||
		!validDigest(r.ExecutionDigest) || !validDigest(r.CleanupDigest) ||
		!validID(r.SummaryCode) || !validDigest(r.EvidenceDigest) {
		return fmt.Errorf("D2 Campaign Case %q is invalid", r.ID)
	}
	if len(r.StepsPlanned) < 4 || r.StepsExecuted == nil {
		return fmt.Errorf("D2 Campaign Case %q step evidence is invalid", r.ID)
	}
	if r.Status == "passed" && !slices.Equal(r.StepsPlanned, r.StepsExecuted) {
		return fmt.Errorf("passed D2 Campaign Case %q has unexecuted steps", r.ID)
	}
	if r.EvidenceDigest != digestCaseResult(r) {
		return fmt.Errorf("D2 Campaign Case %q digest is invalid", r.ID)
	}
	return nil
}

func runCampaignCase(
	ctx context.Context,
	options CampaignOptions,
	generated GeneratedCase,
) CaseResult {
	started := time.Now()
	caseContext, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	result := baseCaseResult(generated)
	status, summary, executionDigest, workspaceDigest, faults, executed, _ :=
		executeCase(
			caseContext,
			options,
			generated,
		)
	if status == "passed" && !slices.Equal(
		plannedSteps(generated),
		executed,
	) {
		status = "invalid"
		summary = "journey_step_unexecuted"
	}
	result.Attempts = 1
	if status == "failed" || status == "invalid" {
		secondStatus, secondSummary, secondDigest, secondWorkspace,
			secondFaults, secondExecuted, _ :=
			executeCase(caseContext, options, generated)
		if secondStatus == "passed" && !slices.Equal(
			plannedSteps(generated),
			secondExecuted,
		) {
			secondStatus = "invalid"
			secondSummary = "journey_step_unexecuted"
		}
		result.Attempts = 2
		if secondStatus == status && secondSummary == summary {
			executionDigest = spec.DigestString(
				executionDigest + "\x00" + secondDigest,
			)
			workspaceDigest = spec.DigestString(
				workspaceDigest + "\x00" + secondWorkspace,
			)
			faults = mergeStrings(faults, secondFaults)
			executed = secondExecuted
		} else {
			status = "failed"
			summary = "non_reproducible_failure"
			executionDigest = spec.DigestString(
				executionDigest + "\x00" + secondDigest,
			)
		}
	}
	result.Status = status
	result.SummaryCode = summary
	result.ExecutionDigest = executionDigest
	result.WorkspaceDigest = workspaceDigest
	result.FaultsTriggered = faults
	result.StepsExecuted = executed
	result.DurationMS = time.Since(started).Milliseconds()
	result.CleanupDigest = spec.DigestString(
		generated.ID + "\x00cleanup\x00closed",
	)
	result.EvidenceDigest = digestCaseResult(result)
	return result
}

func executeCase(
	ctx context.Context,
	options CampaignOptions,
	generated GeneratedCase,
) (
	status, summary, executionDigest, workspaceDigest string,
	faults, executed []string,
	executionErr error,
) {
	faults = []string{}
	executed = []string{}
	temporary, err := os.MkdirTemp("", "codehelper-d2-case-"+generated.ID+"-")
	if err != nil {
		return failedExecution("invalid", "workspace_create_failed", err)
	}
	defer os.RemoveAll(temporary)
	workspace := filepath.Join(temporary, "workspace")
	stateDir := filepath.Join(temporary, "state")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		return failedExecution("invalid", "workspace_create_failed", err)
	}
	executed = append(executed, "prepare_workspace")
	repository, err := MaterializeSyntheticRepository(workspace, generated)
	if err != nil {
		return failedExecution("invalid", "workspace_generation_failed", err)
	}
	workspaceDigest = repository.EvidenceDigest
	if len(generated.Faults) != 0 {
		hits, probeErr := runFaultControlProbes(ctx, options.Root)
		if probeErr != nil {
			return failedExecution("failed", "fault_control_failed", probeErr)
		}
		for _, planned := range generated.Faults {
			if hits[planned.ID] != 1 {
				return failedExecution(
					"failed",
					"fault_trigger_missing",
					fmt.Errorf("%s did not trigger", planned.ID),
				)
			}
			faults = append(faults, planned.ID)
		}
		slices.Sort(faults)
	}
	live := generated.Values["model_variability"] == "live_primary"
	if live && !options.Live {
		return "unavailable", "live_provider_not_authorized",
			spec.DigestString(generated.ID + "\x00live-unavailable"),
			workspaceDigest, faults, executed, nil
	}
	var execution runner.OwnedCommandResult
	switch {
	case live:
		liveCampaignMu.Lock()
		defer liveCampaignMu.Unlock()
		execution, err = runner.RunOwnedCommand(
			ctx,
			options.Root,
			[]string{"make", "deepseek-live-smoke"},
			[]string{
				"CODEHELPER_STAGE=d2_campaign",
				"CODEHELPER_STAGE_RUN_ID=" + options.ID,
				"CODEHELPER_D2_CASE_ID=" + generated.ID,
				"CODEHELPER_LIVE_BINARY=" + options.Runtime,
			},
			8<<20,
		)
		if err == nil {
			executed = append(
				executed,
				"start_runtime",
				"submit_prompt",
				"observe_terminal",
			)
		}
	case generated.DriverID == "cli":
		var driverSteps []string
		execution, driverSteps, err = runCLICase(
			ctx,
			options,
			generated,
			workspace,
			stateDir,
		)
		executed = append(executed, driverSteps...)
	case generated.DriverID == "acp":
		var digest string
		var driverSteps []string
		digest, driverSteps, err = runACPCase(
			ctx,
			options,
			generated,
			workspace,
			stateDir,
		)
		execution.StdoutDigest = digest
		execution.StderrDigest = spec.DigestString("")
		executed = append(executed, driverSteps...)
	case generated.DriverID == "vscode":
		var driverSteps []string
		execution, driverSteps, err = runVSCodeCase(
			ctx,
			options,
			generated,
			workspace,
			stateDir,
		)
		executed = append(executed, driverSteps...)
	default:
		err = errors.New("unsupported D2 Driver")
	}
	executionDigest = spec.DigestString(strings.Join([]string{
		execution.StdoutDigest,
		execution.StderrDigest,
		fmt.Sprintf("%d", execution.ExitCode),
		fmt.Sprintf("%t", execution.TimedOut),
	}, "\x00"))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || execution.TimedOut {
			return "failed", "execution_timeout", executionDigest,
				workspaceDigest, faults, executed, err
		}
		if live && strings.Contains(err.Error(), "credential") {
			return "unavailable", "live_credential_unavailable",
				executionDigest, workspaceDigest, faults, executed, err
		}
		return "failed", "production_boundary_failed",
			executionDigest, workspaceDigest, faults, executed, err
	}
	return "passed", "completed", executionDigest, workspaceDigest,
		faults, executed, nil
}

func runCLICase(
	ctx context.Context,
	options CampaignOptions,
	generated GeneratedCase,
	workspace, stateDir string,
) (runner.OwnedCommandResult, []string, error) {
	args := []string{
		options.Runtime,
		"exec",
		"--provider-fixture", journeyFixture(options.Root),
		"--provider", "openai",
		"--model", "gpt-fixture",
		"--workspace", workspace,
		"--data-dir", stateDir,
		"--session-id", generated.ID,
		"--thread-id", generated.ID,
		"--posture", "never",
		"--enable-tools",
		"say hello",
	}
	if generated.Values["session_state"] == "canceled_effect" {
		result, err := runner.RunOwnedCommand(
			ctx,
			options.Root,
			args,
			nil,
			8<<20,
		)
		if err != nil {
			return result, []string{"start_runtime"}, err
		}
		cancelContext, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		canceled, cancelErr := runner.RunOwnedCommand(
			cancelContext,
			options.Root,
			[]string{
				options.Runtime,
				"exec",
				"--provider-fixture", filepath.Join(
					options.Root,
					"testdata",
					"providers",
					"slow",
				),
				"--workspace", workspace,
				"--data-dir", stateDir,
				"--session-id", generated.ID,
				"--resume",
				"--enable-tools",
				"wait for interrupt",
			},
			nil,
			8<<20,
		)
		result.StdoutDigest = spec.DigestString(
			result.StdoutDigest + "\x00" + canceled.StdoutDigest,
		)
		result.StderrDigest = spec.DigestString(
			result.StderrDigest + "\x00" + canceled.StderrDigest,
		)
		if canceled.TimedOut && errors.Is(cancelErr, context.DeadlineExceeded) {
			result.TimedOut = false
			result.ExitCode = 0
			steps := []string{
				"start_runtime",
				"submit_prompt",
				"start_effect",
				"cancel_turn",
			}
			result, steps, err = completeCLILifecycle(
				ctx,
				options,
				generated,
				workspace,
				stateDir,
				result,
				steps,
			)
			if err != nil {
				return result, steps, err
			}
			steps = append(steps, "observe_terminal")
			return result, steps, nil
		}
		return result, []string{"start_runtime", "submit_prompt", "start_effect"},
			errors.New("CLI cancellation control did not interrupt the Turn")
	}
	result, err := runner.RunOwnedCommand(ctx, options.Root, args, nil, 8<<20)
	if err != nil {
		return result, []string{"start_runtime"}, err
	}
	steps := []string{"start_runtime", "submit_prompt"}
	if generated.Values["session_state"] == "checkpoint_resume" ||
		generated.Values["session_state"] == "long_compacted" ||
		generated.Values["lifecycle"] == "version_upgrade" ||
		generated.Values["lifecycle"] == "rollback_reconnect" {
		resumed, resumeErr := runner.RunOwnedCommand(
			ctx,
			options.Root,
			[]string{
				options.Runtime, "exec",
				"--provider-fixture", journeyFixture(options.Root),
				"--workspace", workspace,
				"--data-dir", stateDir,
				"--session-id", generated.ID,
				"--resume",
				"--enable-tools",
				"say hello",
			},
			nil,
			8<<20,
		)
		result.StdoutDigest = spec.DigestString(
			result.StdoutDigest + "\x00" + resumed.StdoutDigest,
		)
		result.StderrDigest = spec.DigestString(
			result.StderrDigest + "\x00" + resumed.StderrDigest,
		)
		switch generated.Values["session_state"] {
		case "checkpoint_resume":
			steps = append(
				steps,
				"list_checkpoint",
				"restore_checkpoint",
				"resume_session",
			)
		case "long_compacted":
			steps = append(steps, "extend_session")
		}
		if resumeErr != nil {
			return result, steps, resumeErr
		}
	}
	result, steps, err = completeCLILifecycle(
		ctx,
		options,
		generated,
		workspace,
		stateDir,
		result,
		steps,
	)
	if err != nil {
		return result, steps, err
	}
	steps = append(steps, "observe_terminal")
	return result, steps, nil
}

func runVSCodeCase(
	ctx context.Context,
	options CampaignOptions,
	generated GeneratedCase,
	workspace, stateDir string,
) (runner.OwnedCommandResult, []string, error) {
	return runOfficialVSCodeJourney(
		ctx,
		options,
		generated,
		workspace,
		stateDir,
	)
}

func baseCaseResult(generated GeneratedCase) CaseResult {
	faults := make([]string, 0, len(generated.Faults))
	for _, fault := range generated.Faults {
		faults = append(faults, fault.ID)
	}
	scheduleRaw, _ := json.Marshal(generated.Schedule)
	return CaseResult{
		ID: generated.ID, FamilyID: generated.FamilyID,
		DriverID: generated.DriverID, Seed: generated.Seed,
		Live:          generated.Values["model_variability"] == "live_primary",
		FaultsPlanned: faults, FaultsTriggered: []string{},
		StepsPlanned: plannedSteps(generated), StepsExecuted: []string{},
		ScheduleDigest: spec.DigestString(string(scheduleRaw)),
	}
}

func skippedCase(generated GeneratedCase, summary string) CaseResult {
	result := baseCaseResult(generated)
	result.Status = "budget_skipped"
	result.Attempts = 1
	result.SummaryCode = summary
	result.WorkspaceDigest = spec.DigestString(generated.ID + "\x00not-materialized")
	result.ExecutionDigest = spec.DigestString(generated.ID + "\x00not-executed")
	result.CleanupDigest = spec.DigestString(generated.ID + "\x00cleanup\x00not-owned")
	result.EvidenceDigest = digestCaseResult(result)
	return result
}

func observationFor(
	round CampaignRound,
	result CaseResult,
	environmentDigest string,
	now time.Time,
) Observation {
	classification := "unattributed"
	severity := "p2"
	reproducibility := "unreproduced"
	switch result.Status {
	case "unavailable":
		classification = "environment_failure"
		severity = "p3"
	case "invalid":
		classification = "harness_incident"
		severity = "p1"
		if result.Attempts == 2 {
			reproducibility = "exact_seed"
		}
	case "failed":
		if result.Attempts == 2 && result.SummaryCode != "non_reproducible_failure" {
			classification = "product_candidate"
			reproducibility = "exact_seed"
			severity = "p1"
		}
	}
	return Observation{
		SchemaVersion:         SchemaVersion,
		ID:                    "observation-" + result.ID,
		CampaignID:            round.CampaignID,
		CaseID:                result.ID,
		DiscoveryLockIdentity: round.DiscoveryLockIdentity,
		EnvironmentDigest:     environmentDigest,
		Producer:              "d2-campaign",
		Classification:        classification,
		Severity:              severity,
		Reproducibility:       reproducibility,
		Attempts:              result.Attempts,
		EvidenceDigests:       []string{result.EvidenceDigest},
		FirstObservedAt:       now,
		SummaryCode:           result.SummaryCode,
	}
}

func WriteCampaignBundle(
	output string,
	round CampaignRound,
	plan Plan,
	inventory DriverInventory,
	lock DiscoveryLock,
) error {
	if err := round.Validate(); err != nil {
		return err
	}
	if round.DiscoveryLockIdentity != lock.LockIdentity ||
		round.DriverInventoryDigest != inventory.EvidenceDigest ||
		inventory.PlanDigest != plan.EvidenceDigest {
		return errors.New("D2 Campaign bundle identity is inconsistent")
	}
	return writeAtomicBundle(output, []struct {
		name  string
		value any
	}{
		{"campaign-plan.json", plan},
		{"driver-inventory.json", inventory},
		{"discovery-lock.json", lock},
		{"campaign-round.json", round},
	})
}

func failedExecution(
	status, summary string,
	err error,
) (string, string, string, string, []string, []string, error) {
	digest := spec.DigestString(summary + "\x00" + sanitizeError(err))
	return status, summary, digest, spec.DigestString("workspace-unavailable"),
		[]string{}, []string{}, err
}

func mergeStrings(left, right []string) []string {
	merged := make([]string, 0, len(left)+len(right))
	merged = append(merged, left...)
	merged = append(merged, right...)
	slices.Sort(merged)
	return slices.Compact(merged)
}

func caseModelCost(result CaseResult) uint64 {
	if !result.Live || result.Status == "unavailable" ||
		result.SummaryCode == "live_provider_not_authorized" ||
		result.SummaryCode == "live_credential_unavailable" {
		return 0
	}
	// H2 established 50,000 microunits as the upper bound for one
	// exact-response sample. Same-seed reruns consume the same bound again.
	return uint64(result.Attempts) * 50_000
}

func plannedSteps(generated GeneratedCase) []string {
	steps := make([]string, 0, len(generated.Steps))
	for _, step := range generated.Steps {
		steps = append(steps, step.Action)
	}
	return steps
}

func digestCaseResult(result CaseResult) string {
	result.EvidenceDigest = ""
	raw, _ := json.Marshal(result)
	return spec.DigestString(string(raw))
}

func digestCampaignRound(round CampaignRound) string {
	round.EvidenceDigest = ""
	raw, _ := json.Marshal(round)
	return spec.DigestString(string(raw))
}
