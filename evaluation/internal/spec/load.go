package spec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const maxContractBytes = 1 << 20

var idPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,62}[a-z0-9])?$`)

type Bundle struct {
	Root            string
	EvaluationDir   string
	Manifest        Manifest
	Scenarios       map[string]Scenario
	ScenarioPaths   map[string]string
	ScenarioDigests map[string]string
	HarnessDigest   string
}

func Check(root, manifestPath string, now time.Time) (Bundle, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return Bundle{}, fmt.Errorf("resolve repository root: %w", err)
	}
	manifestFile, err := resolveWithin(root, manifestPath)
	if err != nil {
		return Bundle{}, fmt.Errorf("resolve evaluation manifest: %w", err)
	}
	evaluationDir := filepath.Dir(manifestFile)
	raw, err := readContract(manifestFile)
	if err != nil {
		return Bundle{}, fmt.Errorf("read evaluation manifest: %w", err)
	}
	if err := validateSchema(
		filepath.Join(evaluationDir, "schema", "manifest.schema.json"),
		"evaluation-manifest.schema.json",
		raw,
	); err != nil {
		return Bundle{}, fmt.Errorf("validate evaluation manifest schema: %w", err)
	}
	var manifest Manifest
	if err := decodeStrict(raw, &manifest); err != nil {
		return Bundle{}, fmt.Errorf("decode evaluation manifest: %w", err)
	}
	if err := validateManifest(manifest, now); err != nil {
		return Bundle{}, err
	}

	bundle := Bundle{
		Root:            root,
		EvaluationDir:   evaluationDir,
		Manifest:        manifest,
		Scenarios:       make(map[string]Scenario),
		ScenarioPaths:   make(map[string]string),
		ScenarioDigests: make(map[string]string),
	}
	var harnessParts []string
	harnessParts = append(harnessParts, string(raw))
	loadedPaths := make(map[string]string)
	for _, suite := range manifest.Suites {
		for _, relative := range suite.Scenarios {
			scenarioFile, err := resolveWithin(evaluationDir, relative)
			if err != nil {
				return Bundle{}, fmt.Errorf(
					"suite %q scenario path %q: %w",
					suite.ID,
					relative,
					err,
				)
			}
			scenarioID, loaded := loadedPaths[scenarioFile]
			if !loaded {
				scenario, err := loadScenario(evaluationDir, scenarioFile)
				if err != nil {
					return Bundle{}, fmt.Errorf(
						"suite %q scenario %q: %w",
						suite.ID,
						relative,
						err,
					)
				}
				if previous, exists := bundle.ScenarioPaths[scenario.ID]; exists {
					return Bundle{}, fmt.Errorf(
						"duplicate scenario id %q in %q and %q",
						scenario.ID,
						previous,
						relative,
					)
				}
				bundle.Scenarios[scenario.ID] = scenario
				bundle.ScenarioPaths[scenario.ID] = filepath.ToSlash(relative)
				scenarioRaw, readErr := readContract(scenarioFile)
				if readErr != nil {
					return Bundle{}, readErr
				}
				bundle.ScenarioDigests[scenario.ID] = DigestString(string(scenarioRaw))
				harnessParts = append(
					harnessParts,
					filepath.ToSlash(relative)+"\x00"+string(scenarioRaw),
				)
				loadedPaths[scenarioFile] = scenario.ID
				scenarioID = scenario.ID
			}
			scenario := bundle.Scenarios[scenarioID]
			if err := validateSuiteScenario(suite, scenario); err != nil {
				return Bundle{}, err
			}
		}
	}
	if err := validateExceptionReferences(bundle, now); err != nil {
		return Bundle{}, err
	}
	slices.Sort(harnessParts)
	bundle.HarnessDigest = DigestString(strings.Join(harnessParts, "\x00"))
	return bundle, nil
}

func (b Bundle) Suite(id string) (Suite, bool) {
	for _, suite := range b.Manifest.Suites {
		if suite.ID == id {
			return suite, true
		}
	}
	return Suite{}, false
}

func (b Bundle) Scenario(id string) (Scenario, bool) {
	scenario, ok := b.Scenarios[id]
	return scenario, ok
}

func loadScenario(evaluationDir, path string) (Scenario, error) {
	raw, err := readContract(path)
	if err != nil {
		return Scenario{}, err
	}
	if err := validateSchema(
		filepath.Join(evaluationDir, "schema", "scenario.schema.json"),
		"evaluation-scenario.schema.json",
		raw,
	); err != nil {
		return Scenario{}, fmt.Errorf("validate schema: %w", err)
	}
	var scenario Scenario
	if err := decodeStrict(raw, &scenario); err != nil {
		return Scenario{}, fmt.Errorf("decode: %w", err)
	}
	if err := validateScenario(path, scenario); err != nil {
		return Scenario{}, err
	}
	return scenario, nil
}

func validateSchema(path, resource string, raw []byte) error {
	schemaRaw, err := readContract(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	schemaValue, err := decodeValue(schemaRaw)
	if err != nil {
		return fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	compiler.UseLoader(rejectExternalSchemas{})
	if addErr := compiler.AddResource(resource, schemaValue); addErr != nil {
		return fmt.Errorf("add schema resource: %w", addErr)
	}
	compiled, err := compiler.Compile(resource)
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	value, err := decodeValue(raw)
	if err != nil {
		return err
	}
	if validationErr := compiled.Validate(value); validationErr != nil {
		return validationErr
	}
	return nil
}

func validateManifest(manifest Manifest, now time.Time) error {
	if manifest.SchemaVersion != SchemaVersion {
		return fmt.Errorf(
			"evaluation manifest schema_version = %d, want %d",
			manifest.SchemaVersion,
			SchemaVersion,
		)
	}
	if len(manifest.Suites) == 0 {
		return errors.New("evaluation manifest requires at least one suite")
	}
	seen := make(map[string]struct{}, len(manifest.Suites))
	for _, suite := range manifest.Suites {
		if !validID(suite.ID) {
			return fmt.Errorf("evaluation suite id %q is invalid", suite.ID)
		}
		if _, exists := seen[suite.ID]; exists {
			return fmt.Errorf("duplicate evaluation suite id %q", suite.ID)
		}
		seen[suite.ID] = struct{}{}
		if err := validateSuite(suite, now); err != nil {
			return fmt.Errorf("evaluation suite %q: %w", suite.ID, err)
		}
	}
	return nil
}

func validateSuite(suite Suite, now time.Time) error {
	if strings.TrimSpace(suite.Owner) == "" || !suite.Risk.Valid() ||
		!validID(suite.DefaultLane) {
		return errors.New("owner, risk, and default_lane are required")
	}
	if err := uniqueStrings(suite.Scenarios, "scenario path"); err != nil {
		return err
	}
	if err := uniqueIDs(suite.RequiredOracles, "required oracle"); err != nil {
		return err
	}
	if suite.Repetitions < 1 {
		return errors.New("repetitions must be positive")
	}
	if err := validateRequirements(suite.Requirements); err != nil {
		return err
	}
	if err := validateBudgets(suite.Budgets); err != nil {
		return err
	}
	if suite.Budgets.MaxAttempts < suite.Repetitions {
		return errors.New("max_attempts cannot be lower than repetitions")
	}
	if err := validateReleasePolicy(suite.Risk, suite.Repetitions, suite.ReleasePolicy); err != nil {
		return err
	}
	if suite.Risk == RiskP0 && len(suite.Exceptions) != 0 {
		return errors.New("P0 suite cannot declare policy exceptions")
	}
	seen := make(map[string]struct{}, len(suite.Exceptions))
	for _, exception := range suite.Exceptions {
		if !validID(exception.ID) {
			return fmt.Errorf("policy exception id %q is invalid", exception.ID)
		}
		if _, exists := seen[exception.ID]; exists {
			return fmt.Errorf("duplicate policy exception id %q", exception.ID)
		}
		seen[exception.ID] = struct{}{}
		if err := validateException(exception, now); err != nil {
			return fmt.Errorf("policy exception %q: %w", exception.ID, err)
		}
	}
	return nil
}

func validateRequirements(requirements Requirements) error {
	if err := uniqueIDs(requirements.Commands, "required command"); err != nil {
		return err
	}
	if err := uniqueIDs(requirements.Platforms, "required platform"); err != nil {
		return err
	}
	if err := uniqueIDsAllowEmpty(requirements.Capabilities, "required capability"); err != nil {
		return err
	}
	return nil
}

func validateBudgets(budgets Budgets) error {
	if budgets.WallTimeMS < 1 || budgets.MaxAttempts < 1 ||
		budgets.MaxOutputBytes < 1 {
		return errors.New(
			"wall_time_ms, max_attempts, and max_output_bytes must be positive",
		)
	}
	return nil
}

func validateReleasePolicy(risk Risk, repetitions int, policy ReleasePolicy) error {
	if err := uniqueStatuses(policy.AllowedStatuses, "allowed status"); err != nil {
		return err
	}
	if !slices.Contains(policy.AllowedStatuses, StatusPassed) {
		return errors.New("release policy must allow passed")
	}
	if policy.MinimumValidRuns < 1 || policy.MinimumValidRuns > repetitions {
		return errors.New(
			"minimum_valid_runs must be positive and no greater than repetitions",
		)
	}
	if risk == RiskP0 {
		if len(policy.AllowedStatuses) != 1 ||
			policy.AllowedStatuses[0] != StatusPassed {
			return errors.New("P0 release policy can allow only passed")
		}
		if !policy.Blocking {
			return errors.New("P0 release policy must be blocking")
		}
	}
	for _, status := range policy.AllowedStatuses {
		if status == StatusFailed || status == StatusInvalid {
			return fmt.Errorf("release policy cannot allow %q", status)
		}
	}
	return nil
}

func validateException(exception PolicyException, now time.Time) error {
	if strings.TrimSpace(exception.Owner) == "" ||
		strings.TrimSpace(exception.Reason) == "" {
		return errors.New("owner and reason are required")
	}
	if err := uniqueIDs(exception.ScenarioIDs, "scenario id"); err != nil {
		return err
	}
	if err := uniqueStatuses(exception.AllowedStatuses, "allowed status"); err != nil {
		return err
	}
	for _, status := range exception.AllowedStatuses {
		if status != StatusUnavailable && status != StatusNotEvaluated {
			return fmt.Errorf("exception cannot allow %q", status)
		}
	}
	expires, err := time.Parse(time.DateOnly, exception.ExpiresOn)
	if err != nil {
		return fmt.Errorf("expires_on: %w", err)
	}
	today := now.UTC().Truncate(24 * time.Hour)
	if !expires.After(today) {
		return fmt.Errorf("expired on %s", exception.ExpiresOn)
	}
	return nil
}

func validateScenario(path string, scenario Scenario) error {
	if scenario.SchemaVersion != SchemaVersion {
		return fmt.Errorf(
			"scenario %q schema_version = %d, want %d",
			path,
			scenario.SchemaVersion,
			SchemaVersion,
		)
	}
	if !validID(scenario.ID) || !validID(scenario.Family) ||
		strings.TrimSpace(scenario.Title) == "" ||
		strings.TrimSpace(scenario.Owner) == "" || !scenario.Risk.Valid() {
		return errors.New("scenario id, family, title, owner, and risk are required")
	}
	if !scenario.Driver.Valid() {
		return fmt.Errorf("scenario %q has invalid driver %q", scenario.ID, scenario.Driver)
	}
	switch scenario.ProviderMode {
	case "fixture", "recorded", "live", "none":
	default:
		return fmt.Errorf(
			"scenario %q has invalid provider_mode %q",
			scenario.ID,
			scenario.ProviderMode,
		)
	}
	if err := validateRelativePath(scenario.Workspace); err != nil {
		return fmt.Errorf("scenario %q workspace: %w", scenario.ID, err)
	}
	if !validID(scenario.FixtureID) || !validID(scenario.CleanupContract) {
		return fmt.Errorf("scenario %q fixture_id or cleanup_contract is invalid", scenario.ID)
	}
	if err := uniqueIDs(scenario.ExpectedFacts, "expected fact"); err != nil {
		return fmt.Errorf("scenario %q: %w", scenario.ID, err)
	}
	if err := uniqueIDs(scenario.Oracles, "oracle"); err != nil {
		return fmt.Errorf("scenario %q: %w", scenario.ID, err)
	}
	if err := uniqueIDs(scenario.RequiredEvidence, "required evidence"); err != nil {
		return fmt.Errorf("scenario %q: %w", scenario.ID, err)
	}
	if err := uniqueIDsAllowEmpty(scenario.RequiredMutations, "required mutation"); err != nil {
		return fmt.Errorf("scenario %q: %w", scenario.ID, err)
	}
	if scenario.RunPlan.Attempts < 1 ||
		!validID(scenario.RunPlan.CollectAllGroup) {
		return fmt.Errorf("scenario %q run_plan is invalid", scenario.ID)
	}
	if err := validateBudgets(scenario.Budgets); err != nil {
		return fmt.Errorf("scenario %q: %w", scenario.ID, err)
	}
	if err := validateRequirements(scenario.Requirements); err != nil {
		return fmt.Errorf("scenario %q: %w", scenario.ID, err)
	}
	if err := uniqueIDsAllowEmpty(scenario.Tags, "tag"); err != nil {
		return fmt.Errorf("scenario %q: %w", scenario.ID, err)
	}
	for _, turn := range scenario.Turns {
		if err := validateRelativePath(turn.PromptFile); err != nil {
			return fmt.Errorf("scenario %q prompt_file: %w", scenario.ID, err)
		}
	}
	for _, fault := range scenario.Faults {
		if strings.TrimSpace(fault.At) == "" || !validID(fault.Action) {
			return fmt.Errorf("scenario %q has invalid fault", scenario.ID)
		}
	}
	if err := validateRelativePath(scenario.Execution.WorkingDirectory); err != nil {
		return fmt.Errorf("scenario %q working_directory: %w", scenario.ID, err)
	}
	if scenario.Driver == DriverCommand && len(scenario.Execution.Command) == 0 {
		return fmt.Errorf("scenario %q command driver requires execution.command", scenario.ID)
	}
	if scenario.Driver == DriverCommand {
		if len(scenario.Oracles) != 1 ||
			scenario.Oracles[0] != "command_verification" ||
			!slices.Contains(scenario.RequiredEvidence, "command_result") {
			return fmt.Errorf(
				"scenario %q command driver requires only command_verification over command_result",
				scenario.ID,
			)
		}
	}
	for _, argument := range scenario.Execution.Command {
		if strings.TrimSpace(argument) == "" {
			return fmt.Errorf("scenario %q command contains an empty argument", scenario.ID)
		}
	}
	if len(scenario.Execution.Command) == 0 && len(scenario.Turns) == 0 {
		return fmt.Errorf("scenario %q requires execution.command or turns", scenario.ID)
	}
	return nil
}

func validateSuiteScenario(suite Suite, scenario Scenario) error {
	if scenario.Risk != suite.Risk {
		return fmt.Errorf(
			"suite %q risk %s does not match scenario %q risk %s",
			suite.ID,
			suite.Risk,
			scenario.ID,
			scenario.Risk,
		)
	}
	if scenario.RunPlan.Attempts != suite.Repetitions {
		return fmt.Errorf(
			"suite %q repetitions %d do not match scenario %q attempts %d",
			suite.ID,
			suite.Repetitions,
			scenario.ID,
			scenario.RunPlan.Attempts,
		)
	}
	if Effective(suite, scenario).Budgets.MaxAttempts < suite.Repetitions {
		return fmt.Errorf(
			"suite %q effective max_attempts is lower than repetitions",
			suite.ID,
		)
	}
	for _, required := range suite.RequiredOracles {
		if !slices.Contains(scenario.Oracles, required) {
			return fmt.Errorf(
				"suite %q scenario %q omits required oracle %q",
				suite.ID,
				scenario.ID,
				required,
			)
		}
	}
	return nil
}

func validateExceptionReferences(bundle Bundle, _ time.Time) error {
	for _, suite := range bundle.Manifest.Suites {
		suiteScenarioIDs := make(map[string]struct{}, len(suite.Scenarios))
		for _, path := range suite.Scenarios {
			absolute, err := resolveWithin(bundle.EvaluationDir, path)
			if err != nil {
				return err
			}
			suiteScenarioIDs[bundle.Scenarios[findScenarioID(bundle, absolute)].ID] = struct{}{}
		}
		for _, exception := range suite.Exceptions {
			for _, scenarioID := range exception.ScenarioIDs {
				if _, exists := suiteScenarioIDs[scenarioID]; !exists {
					return fmt.Errorf(
						"suite %q exception %q references unknown scenario %q",
						suite.ID,
						exception.ID,
						scenarioID,
					)
				}
			}
		}
	}
	return nil
}

func findScenarioID(bundle Bundle, absolute string) string {
	for id, relative := range bundle.ScenarioPaths {
		candidate, err := resolveWithin(bundle.EvaluationDir, relative)
		if err == nil && candidate == absolute {
			return id
		}
	}
	return ""
}

func uniqueStrings(values []string, name string) error {
	if len(values) == 0 {
		return fmt.Errorf("%s list is empty", name)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is empty", name)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate %s %q", name, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func uniqueIDs(values []string, name string) error {
	if len(values) == 0 {
		return fmt.Errorf("%s list is empty", name)
	}
	return uniqueIDsAllowEmpty(values, name)
}

func uniqueIDsAllowEmpty(values []string, name string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validID(value) {
			return fmt.Errorf("%s %q is invalid", name, value)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate %s %q", name, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func uniqueStatuses(values []Status, name string) error {
	if len(values) == 0 {
		return fmt.Errorf("%s list is empty", name)
	}
	seen := make(map[Status]struct{}, len(values))
	for _, status := range values {
		if !status.Valid() {
			return fmt.Errorf("%s %q is invalid", name, status)
		}
		if _, exists := seen[status]; exists {
			return fmt.Errorf("duplicate %s %q", name, status)
		}
		seen[status] = struct{}{}
	}
	return nil
}

func validID(value string) bool {
	return idPattern.MatchString(value)
}

func validateRelativePath(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("path is required")
	}
	if filepath.IsAbs(path) || filepath.Clean(path) != filepath.FromSlash(path) {
		return fmt.Errorf("path %q must be clean and relative", path)
	}
	if path == ".." || strings.HasPrefix(filepath.ToSlash(path), "../") {
		return fmt.Errorf("path %q escapes its root", path)
	}
	return nil
}

func resolveWithin(root, path string) (string, error) {
	if filepath.IsAbs(path) {
		absolute := filepath.Clean(path)
		relative, err := filepath.Rel(root, absolute)
		if err != nil || relative == ".." ||
			strings.HasPrefix(filepath.ToSlash(relative), "../") {
			return "", fmt.Errorf("path %q escapes root", path)
		}
		return absolute, nil
	}
	if err := validateRelativePath(filepath.ToSlash(path)); err != nil {
		return "", err
	}
	absolute := filepath.Join(root, filepath.FromSlash(path))
	relative, err := filepath.Rel(root, absolute)
	if err != nil || relative == ".." ||
		strings.HasPrefix(filepath.ToSlash(relative), "../") {
		return "", fmt.Errorf("path %q escapes root", path)
	}
	return filepath.Clean(absolute), nil
}

func readContract(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%q is not a regular file", path)
	}
	if info.Size() > maxContractBytes {
		return nil, fmt.Errorf(
			"%q is %d bytes, limit is %d",
			path,
			info.Size(),
			maxContractBytes,
		)
	}
	return os.ReadFile(path)
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("document must contain exactly one JSON value")
	}
	return nil
}

func decodeValue(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("document must contain exactly one JSON value")
	}
	return value, nil
}

type rejectExternalSchemas struct{}

func (rejectExternalSchemas) Load(url string) (any, error) {
	return nil, fmt.Errorf("external schema loading is disabled: %s", url)
}
