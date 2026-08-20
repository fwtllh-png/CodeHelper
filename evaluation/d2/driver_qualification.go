package d2

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/runner"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

var requiredDriverQualificationChecks = []string{
	"acp-boundary-negative",
	"artifact-identity",
	"cleanup-ownership",
	"cli-boundary-negative",
	"driver-schema",
	"fault-trigger-coverage",
	"input-identity",
	"inventory-determinism",
	"journey-coverage",
	"journey-execution",
	"live-driver-routing",
	"plan-parity",
	"privacy-closure",
	"scale-bounds",
	"schedule-replay",
	"synthetic-repository-replay",
	"topology-driver-routing",
	"vscode-boundary-negative",
}

type DriverQualificationReport struct {
	SchemaVersion                 int                  `json:"schema_version"`
	ID                            string               `json:"id"`
	DiscoveryLockIdentity         string               `json:"discovery_lock_identity"`
	FoundationQualificationDigest string               `json:"foundation_qualification_digest"`
	DriverInventoryDigest         string               `json:"driver_inventory_digest"`
	Status                        string               `json:"status"`
	Scheduled                     int                  `json:"scheduled"`
	Settled                       int                  `json:"settled"`
	Passed                        int                  `json:"passed"`
	Failed                        int                  `json:"failed"`
	Invalid                       int                  `json:"invalid"`
	Checks                        []QualificationCheck `json:"checks"`
	EvidenceDigest                string               `json:"evidence_digest"`
}

type DriverQualificationOptions struct {
	Root       string
	ID         string
	Runtime    string
	VSIX       string
	Extension  string
	NPM        string
	Timeout    time.Duration
	Foundation QualificationReport
	Lock       DiscoveryLock
	Campaign   Campaign
	Plan       Plan
	Inventory  DriverInventory
}

func RunDriverQualification(
	ctx context.Context,
	options DriverQualificationOptions,
) (DriverQualificationReport, error) {
	if options.NPM == "" {
		options.NPM = "npm"
	}
	if options.Timeout <= 0 {
		options.Timeout = 2 * time.Minute
	}
	if !validID(options.ID) {
		return DriverQualificationReport{}, errors.New(
			"D2 Driver qualification ID is invalid",
		)
	}
	type check struct {
		id  string
		run func(context.Context) error
	}
	checks := []check{
		{"driver-schema", func(context.Context) error {
			raw, err := json.Marshal(options.Inventory)
			if err != nil {
				return err
			}
			return validateSchemaFile(
				options.Root,
				"evaluation/schema/discovery-driver-inventory.schema.json",
				raw,
			)
		}},
		{"inventory-determinism", func(context.Context) error {
			repeated, err := BuildDriverInventory(options.Campaign, options.Plan)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(repeated, options.Inventory) {
				return errors.New("D2 Driver inventory is not deterministic")
			}
			return nil
		}},
		{"plan-parity", func(context.Context) error {
			if len(options.Inventory.Cases) != len(options.Plan.Cases) {
				return errors.New("D2 Driver inventory changed the Case denominator")
			}
			for index, planned := range options.Plan.Cases {
				generated := options.Inventory.Cases[index]
				if generated.ID != planned.ID ||
					generated.FamilyID != planned.FamilyID ||
					generated.Seed != planned.Seed ||
					!reflect.DeepEqual(generated.Values, planned.Values) {
					return fmt.Errorf("D2 Driver Case %q drifted from plan", planned.ID)
				}
			}
			return nil
		}},
		{"cli-boundary-negative", func(checkCtx context.Context) error {
			return probeCLI(checkCtx, options.Root, options.Runtime)
		}},
		{"acp-boundary-negative", func(checkCtx context.Context) error {
			return probeACP(checkCtx, options.Root, options.Runtime)
		}},
		{"vscode-boundary-negative", func(checkCtx context.Context) error {
			return probeVSCode(
				checkCtx,
				options.Root,
				options.Extension,
				options.Runtime,
				options.NPM,
			)
		}},
		{"fault-trigger-coverage", func(checkCtx context.Context) error {
			hits := make(map[string]int)
			for _, generated := range options.Inventory.Cases {
				for _, fault := range generated.Faults {
					hits[fault.ID] += fault.ExpectedHit
				}
			}
			for _, id := range requiredFaultIDs {
				if hits[id] < 1 {
					return fmt.Errorf("D2 fault %q has zero planned trigger", id)
				}
			}
			observed, err := runFaultControlProbes(checkCtx, options.Root)
			if err != nil {
				return err
			}
			for _, id := range requiredFaultIDs {
				if observed[id] != 1 {
					return fmt.Errorf("D2 fault %q did not trigger exactly once", id)
				}
			}
			return nil
		}},
		{"schedule-replay", func(context.Context) error {
			repeated, err := BuildDriverInventory(options.Campaign, options.Plan)
			if err != nil {
				return err
			}
			for index := range options.Inventory.Cases {
				if !reflect.DeepEqual(
					options.Inventory.Cases[index].Schedule,
					repeated.Cases[index].Schedule,
				) {
					return errors.New("D2 same-seed schedule replay drifted")
				}
			}
			return nil
		}},
		{"cleanup-ownership", func(context.Context) error {
			for _, generated := range options.Inventory.Cases {
				if err := generated.Validate(); err != nil {
					return err
				}
			}
			return nil
		}},
		{"privacy-closure", func(context.Context) error {
			if options.Campaign.Privacy.PersistUserContent ||
				!options.Campaign.Privacy.PromotionReviewRequired {
				return errors.New("D2 Driver privacy is fail open")
			}
			raw, err := json.Marshal(options.Inventory)
			if err != nil {
				return err
			}
			for _, forbidden := range []string{
				"prompt_text",
				"stdout_content",
				"stderr_content",
				"user_content",
			} {
				if bytes.Contains(raw, []byte(forbidden)) {
					return fmt.Errorf("D2 Driver inventory persists %s", forbidden)
				}
			}
			return nil
		}},
		{"scale-bounds", func(context.Context) error {
			for _, generated := range options.Inventory.Cases {
				if err := generated.Validate(); err != nil {
					return err
				}
			}
			return nil
		}},
		{"synthetic-repository-replay", func(context.Context) error {
			var selected GeneratedCase
			for _, generated := range options.Inventory.Cases {
				if generated.Workload.Files > selected.Workload.Files {
					selected = generated
				}
			}
			firstRoot, err := os.MkdirTemp("", "codehelper-d2-repository-a-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(firstRoot)
			secondRoot, err := os.MkdirTemp("", "codehelper-d2-repository-b-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(secondRoot)
			first, err := MaterializeSyntheticRepository(firstRoot, selected)
			if err != nil {
				return err
			}
			second, err := MaterializeSyntheticRepository(secondRoot, selected)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(first, second) ||
				first.Files != selected.Workload.Files {
				return errors.New("D2 synthetic repository Replay drifted")
			}
			return nil
		}},
		{"journey-coverage", func(context.Context) error {
			required := map[string]bool{
				"cancel_turn": false, "observe_compaction": false,
				"restore_checkpoint": false, "resume_session": false,
				"crash_runtime": false, "upgrade_runtime": false,
				"rollback_runtime": false, "reconnect_session": false,
			}
			assertions := map[string]bool{
				"equivalent_task_terminal": false,
				"restart_boundary_replay":  false,
			}
			for _, generated := range options.Inventory.Cases {
				for _, step := range generated.Steps {
					if _, exists := required[step.Action]; exists {
						required[step.Action] = true
					}
				}
				for _, assertion := range generated.Assertions {
					if _, exists := assertions[assertion]; exists {
						assertions[assertion] = true
					}
				}
			}
			for id, covered := range required {
				if !covered {
					return fmt.Errorf("D2 Journey action %q is uncovered", id)
				}
			}
			for id, covered := range assertions {
				if !covered {
					return fmt.Errorf("D2 assertion %q is uncovered", id)
				}
			}
			return nil
		}},
		{"journey-execution", func(checkCtx context.Context) error {
			return qualifyJourneyExecution(checkCtx, options)
		}},
		{"live-driver-routing", func(context.Context) error {
			liveCases := 0
			expectedSteps := []string{
				"prepare_workspace",
				"start_runtime",
				"submit_prompt",
				"observe_terminal",
			}
			for _, generated := range options.Inventory.Cases {
				if generated.Values["model_variability"] != "live_primary" {
					continue
				}
				liveCases++
				if generated.FamilyID != "live_model_variability" ||
					generated.DriverID != "cli" ||
					!slices.Equal(plannedSteps(generated), expectedSteps) {
					return fmt.Errorf(
						"D2 Live Case %q does not use the authoritative CLI path",
						generated.ID,
					)
				}
			}
			if liveCases == 0 {
				return errors.New("D2 Live Driver routing has no Case")
			}
			return nil
		}},
		{"topology-driver-routing", func(context.Context) error {
			for _, generated := range options.Inventory.Cases {
				topology := generated.Values["topology"]
				if topology == "" {
					continue
				}
				if topology != generated.DriverID {
					return fmt.Errorf(
						"D2 topology %q is not executed by Driver %q",
						topology,
						generated.DriverID,
					)
				}
			}
			return nil
		}},
		{"artifact-identity", func(context.Context) error {
			runtimeDigest, err := digestArtifact(options.Runtime)
			if err != nil {
				return err
			}
			vsixDigest, err := digestArtifact(options.VSIX)
			if err != nil {
				return err
			}
			if runtimeDigest != options.Lock.RuntimeDigest ||
				vsixDigest != options.Lock.VSIXDigest {
				return errors.New("D2 Driver artifact identity is mixed")
			}
			return nil
		}},
		{"input-identity", func(context.Context) error {
			if options.Foundation.Status != "passed" ||
				options.Foundation.DiscoveryLockIdentity != options.Lock.LockIdentity {
				return errors.New("D2.1 Qualification does not bind this Lock")
			}
			_, err := VerifyDiscoveryInputs(options.Root, options.Lock)
			return err
		}},
	}
	report := DriverQualificationReport{
		SchemaVersion:                 SchemaVersion,
		ID:                            options.ID,
		DiscoveryLockIdentity:         options.Lock.LockIdentity,
		FoundationQualificationDigest: options.Foundation.EvidenceDigest,
		DriverInventoryDigest:         options.Inventory.EvidenceDigest,
		Status:                        "passed",
		Scheduled:                     len(checks),
	}
	var failureDetails []string
	for _, item := range checks {
		checkCtx, cancel := context.WithTimeout(ctx, options.Timeout)
		err := item.run(checkCtx)
		cancel()
		result := QualificationCheck{ID: item.id, Status: "passed"}
		if err != nil {
			result.Status = "failed"
			report.Status = "failed"
			report.Failed++
			failureDetails = append(
				failureDetails,
				item.id+": "+sanitizeError(err),
			)
			result.EvidenceDigest = spec.DigestString(
				item.id + "\x00failed\x00" + sanitizeError(err),
			)
		} else {
			report.Passed++
			result.EvidenceDigest = spec.DigestString(strings.Join([]string{
				item.id,
				"passed",
				options.Lock.LockIdentity,
				options.Inventory.EvidenceDigest,
			}, "\x00"))
		}
		report.Checks = append(report.Checks, result)
		report.Settled++
	}
	slices.SortFunc(report.Checks, func(left, right QualificationCheck) int {
		return strings.Compare(left.ID, right.ID)
	})
	report.EvidenceDigest = digestDriverQualification(report)
	if err := report.Validate(); err != nil {
		return report, err
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return report, err
	}
	if err := validateSchemaFile(
		options.Root,
		"evaluation/schema/discovery-driver-qualification.schema.json",
		raw,
	); err != nil {
		return report, err
	}
	if len(failureDetails) != 0 {
		return report, errors.New(strings.Join(failureDetails, "; "))
	}
	return report, nil
}

func (r DriverQualificationReport) Validate() error {
	if r.SchemaVersion != SchemaVersion || !validID(r.ID) ||
		!validDigest(r.DiscoveryLockIdentity) ||
		!validDigest(r.FoundationQualificationDigest) ||
		!validDigest(r.DriverInventoryDigest) ||
		(r.Status != "passed" && r.Status != "failed" && r.Status != "invalid") ||
		r.Scheduled < 1 || r.Settled != r.Scheduled ||
		len(r.Checks) != r.Scheduled ||
		r.Passed+r.Failed+r.Invalid != r.Settled ||
		!validDigest(r.EvidenceDigest) {
		return errors.New("D2 Driver qualification inventory is invalid")
	}
	ids := make([]string, 0, len(r.Checks))
	for _, check := range r.Checks {
		if !validID(check.ID) ||
			(check.Status != "passed" && check.Status != "failed" &&
				check.Status != "invalid") ||
			!validDigest(check.EvidenceDigest) {
			return fmt.Errorf("D2 Driver qualification check %q is invalid", check.ID)
		}
		ids = append(ids, check.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, requiredDriverQualificationChecks) {
		return errors.New("D2 Driver qualification checks are incomplete")
	}
	if r.Status == "passed" && (r.Passed != r.Scheduled ||
		r.Failed != 0 || r.Invalid != 0) {
		return errors.New("passed D2 Driver qualification has non-passing checks")
	}
	if r.EvidenceDigest != digestDriverQualification(r) {
		return errors.New("D2 Driver qualification digest is invalid")
	}
	return nil
}

func QualifyDriverLock(
	lock DiscoveryLock,
	report DriverQualificationReport,
) (DiscoveryLock, error) {
	if err := lock.Validate(); err != nil {
		return DiscoveryLock{}, err
	}
	if err := report.Validate(); err != nil {
		return DiscoveryLock{}, err
	}
	if lock.Status != "candidate" || report.Status != "passed" ||
		report.DiscoveryLockIdentity != lock.LockIdentity {
		return DiscoveryLock{}, errors.New(
			"D2 Driver qualification does not match candidate Discovery Lock",
		)
	}
	lock.Status = "qualified"
	lock.QualificationDigest = report.EvidenceDigest
	return lock, lock.Validate()
}

func WriteDriverQualificationBundle(
	output string,
	plan Plan,
	inventory DriverInventory,
	foundation QualificationReport,
	report DriverQualificationReport,
	lock DiscoveryLock,
) error {
	if err := inventory.Validate(); err != nil {
		return err
	}
	if err := foundation.Validate(); err != nil {
		return err
	}
	if err := report.Validate(); err != nil {
		return err
	}
	if err := lock.Validate(); err != nil {
		return err
	}
	if lock.Status != "qualified" ||
		lock.QualificationDigest != report.EvidenceDigest ||
		report.FoundationQualificationDigest != foundation.EvidenceDigest ||
		report.DriverInventoryDigest != inventory.EvidenceDigest ||
		plan.EvidenceDigest != lock.PlannerDigest {
		return errors.New("D2 Driver qualification bundle identity is inconsistent")
	}
	return writeAtomicBundle(output, []struct {
		name  string
		value any
	}{
		{"campaign-plan.json", plan},
		{"driver-inventory.json", inventory},
		{"foundation-qualification.json", foundation},
		{"driver-qualification.json", report},
		{"discovery-lock.json", lock},
	})
}

func writeAtomicBundle(
	output string,
	artifacts []struct {
		name  string
		value any
	},
) error {
	parent := filepath.Dir(output)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(output); err == nil {
		return fmt.Errorf("D2 output %q already exists", output)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".d2-driver-qualification-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	for _, artifact := range artifacts {
		if err := WriteJSON(filepath.Join(staging, artifact.name), artifact.value); err != nil {
			return err
		}
	}
	return os.Rename(staging, output)
}

func probeCLI(ctx context.Context, root, runtimePath string) error {
	command := exec.CommandContext(
		ctx,
		runtimePath,
		"exec",
		"--workspace", root,
		"--provider-fixture", filepath.Join(root, "testdata", "providers", "openai"),
		"--output-format", "d2-invalid",
		"negative control",
	)
	raw, err := command.CombinedOutput()
	if err == nil || !bytes.Contains(
		raw,
		[]byte("exec currently supports only --output-format stream-json"),
	) {
		return errors.New("CLI negative control did not reach the exec boundary")
	}
	return nil
}

func probeACP(ctx context.Context, root, runtimePath string) error {
	dataDir, err := os.MkdirTemp("", "codehelper-d2-acp-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dataDir)
	command := exec.CommandContext(
		ctx,
		runtimePath,
		"host", "--adapter", "acp",
		"--data-dir", filepath.Join(dataDir, "state"),
		"--provider-fixture", filepath.Join(root, "testdata", "providers", "openai"),
		"--workspace", root,
		"--posture", "never",
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return err
	}
	defer func() {
		_ = stdin.Close()
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	requests := []string{
		`{"jsonrpc":"2.0","id":"negative","method":"initialize","params":{"protocolVersion":99}}`,
		`{"jsonrpc":"2.0","id":"shutdown","method":"shutdown","params":{}}`,
	}
	for _, request := range requests {
		if _, err := io.WriteString(stdin, request+"\n"); err != nil {
			return err
		}
	}
	scanner := bufio.NewScanner(stdout)
	foundNegative := false
	foundShutdown := false
	for scanner.Scan() {
		var frame struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			return err
		}
		switch string(frame.ID) {
		case `"negative"`:
			foundNegative = frame.Error != nil && frame.Error.Code == -32602
		case `"shutdown"`:
			foundShutdown = frame.Error == nil && len(frame.Result) != 0
		}
		if foundNegative && foundShutdown {
			break
		}
	}
	_ = stdin.Close()
	if err := command.Wait(); err != nil {
		return fmt.Errorf("ACP negative control shutdown: %w: %s", err, stderr.String())
	}
	if !foundNegative || !foundShutdown || strings.TrimSpace(stderr.String()) != "" {
		return errors.New("ACP negative control did not reach the protocol boundary")
	}
	return nil
}

func probeVSCode(
	ctx context.Context,
	root, extension, runtimePath, npm string,
) error {
	if extension == "" {
		extension = filepath.Join(root, "extensions", "vscode")
	}
	result, err := runner.RunOwnedCommand(
		ctx,
		extension,
		[]string{npm, "test", "--", "runtime"},
		[]string{
			"CODEHELPER_VSCODE_BINARY=" + runtimePath,
			"CODEHELPER_VSCODE_FIXTURE=" + filepath.Join(
				root, "testdata", "providers", "tools",
			),
			"CODEHELPER_VSCODE_CONTEXT_FIXTURE=" + filepath.Join(
				root, "testdata", "providers", "editor-context",
			),
		},
		8<<20,
	)
	if err != nil {
		return fmt.Errorf(
			"Official VS Code Runtime negative-control suite failed: %w (%s/%s)",
			err,
			result.StdoutDigest,
			result.StderrDigest,
		)
	}
	if result.TimedOut || result.Truncated {
		return errors.New("Official VS Code Runtime probe exceeded its bounds")
	}
	return nil
}

func digestArtifact(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil)), nil
}

func digestDriverQualification(report DriverQualificationReport) string {
	report.EvidenceDigest = ""
	raw, _ := json.Marshal(report)
	return spec.DigestString(string(raw))
}
