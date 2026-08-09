package config

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

var secretNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_./:@-]*$`)
var diagnosticExtensionPattern = regexp.MustCompile(`^\.[A-Za-z0-9][A-Za-z0-9._+-]{0,15}$`)
var diagnosticCommandPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)

type FieldError struct {
	Field  string
	Source Source
	Reason string
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("invalid config field %s from %s: %s", e.Field, e.Source, e.Reason)
}

func (s Snapshot) Validate() error {
	checkRange := func(field string, value, maximum int) error {
		if value < 1 || value > maximum {
			return fieldError(field, s.Provenance, fmt.Sprintf("must be between 1 and %d", maximum))
		}
		return nil
	}
	if err := checkRange(fieldOperationBuffer, s.Config.Runtime.OperationBuffer, 65536); err != nil {
		return err
	}
	if err := checkRange(fieldEventHistory, s.Config.Runtime.EventHistory, 1_000_000); err != nil {
		return err
	}
	if err := checkRange(fieldSubscriberBuffer, s.Config.Runtime.SubscriberBuffer, 65536); err != nil {
		return err
	}
	if strings.TrimSpace(s.Config.State.DataDir) == "" {
		return fieldError(fieldStateDataDir, s.Provenance, "must not be empty")
	}
	if s.Config.State.BusyTimeout <= 0 || s.Config.State.BusyTimeout > 5*time.Minute {
		return fieldError(fieldStateBusyTimeout, s.Provenance, "must be between 1ns and 5m")
	}
	if err := checkRange(fieldStateRetention, s.Config.State.EventRetention, 100_000_000); err != nil {
		return err
	}
	if strings.TrimSpace(s.Config.Memory.Path) == "" {
		return fieldError(fieldMemoryPath, s.Provenance, "must not be empty")
	}
	if index := s.Config.Context.Index; index.Enabled {

		if index.MaxFileBytes < 1024 || index.MaxFileBytes > 64<<20 {
			return fieldError(fieldIndexMaxBytes, s.Provenance,
				"must be between 1024 and 67108864")
		}
		if err := checkRange(fieldIndexMaxFiles, index.MaxFiles, 1_000_000); err != nil {
			return err
		}
	}
	if repoMap := s.Config.Context.RepoMap; repoMap.Enabled {

		if repoMap.MaxBytes < 256 || repoMap.MaxBytes > 1<<20 {
			return fieldError(fieldRepoMapMaxBytes, s.Provenance,
				"must be between 256 and 1048576")
		}
		if err := checkRange(fieldRepoMapMaxDirectories, repoMap.MaxDirectories, 512); err != nil {
			return err
		}
	}
	if workingSet := s.Config.Context.WorkingSet; workingSet.Enabled {
		if err := checkRange(fieldWorkingSetMaxEntries, workingSet.MaxEntries, 256); err != nil {
			return err
		}
		if workingSet.MaxBytes < 256 || workingSet.MaxBytes > 1<<20 {
			return fieldError(fieldWorkingSetMaxBytes, s.Provenance,
				"must be between 256 and 1048576")
		}
	}
	if evidence := s.Config.Context.Evidence; evidence.Enabled {
		if err := checkRange(fieldEvidenceMaxEntries, evidence.MaxEntries, 256); err != nil {
			return err
		}
		if evidence.MaxBytes < 256 || evidence.MaxBytes > 1<<20 {
			return fieldError(fieldEvidenceMaxBytes, s.Provenance,
				"must be between 256 and 1048576")
		}
	}

	compaction := s.Config.Context.Compact
	if compaction.MaxHistoryBytes < 256 || compaction.MaxHistoryBytes > 64<<20 {
		return fieldError(fieldCompactMaxHistory, s.Provenance,
			"must be between 256 and 67108864")
	}

	if compaction.SummaryMaxBytes < 256 || compaction.SummaryMaxBytes > 1<<20 {
		return fieldError(fieldCompactSummaryMax, s.Provenance,
			"must be between 256 and 1048576")
	}
	if err := checkRange(fieldCompactMaxDigest, compaction.MaxDigestEntries, 4096); err != nil {
		return err
	}
	switch s.Config.Telemetry.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fieldError(fieldLogLevel, s.Provenance, "must be debug, info, warn, or error")
	}
	execution := s.Config.Execution
	switch execution.Protocol {
	case "openai_chat", "openai_responses", "anthropic":
	default:
		return fieldError(fieldProtocol, s.Provenance, "unsupported provider protocol")
	}
	switch execution.Mode {
	case "plan", "act", "operate":
	default:
		return fieldError(fieldMode, s.Provenance, "must be plan, act, or operate")
	}
	if execution.Workspace == "" {
		return fieldError(fieldWorkspace, s.Provenance, "must not be empty")
	}
	if execution.MaxOutputTokens == 0 {
		return fieldError(fieldMaxOutputTokens, s.Provenance, "must be positive")
	}
	if execution.MaxSteps < 1 {
		return fieldError(fieldMaxSteps, s.Provenance, "must be positive")
	}
	if execution.Timeout <= 0 {
		return fieldError(fieldTimeout, s.Provenance, "must be positive")
	}
	if execution.IdleTimeout <= 0 {
		return fieldError(fieldIdleTimeout, s.Provenance, "must be positive")
	}
	if execution.MaxConcurrent < 1 {
		return fieldError(fieldMaxConcurrent, s.Provenance, "must be positive")
	}
	if execution.RateLimit < 0 {
		return fieldError(fieldRateLimit, s.Provenance, "must be non-negative")
	}
	if execution.BudgetUSD < 0 {
		return fieldError(fieldBudgetUSD, s.Provenance, "must be non-negative")
	}
	ref := s.Config.Credential
	if !ref.Empty() {
		switch ref.Kind {
		case "env", "file", "keyring":
		default:
			return fieldError(fieldCredentialKind, s.Provenance, "must be env, file, or keyring")
		}
		if !secretNamePattern.MatchString(ref.Name) {
			return fieldError(fieldCredentialName, s.Provenance, "must be a non-secret reference name")
		}
	}
	if err := s.validateVerify(); err != nil {
		return err
	}
	if err := s.validateSubagent(); err != nil {
		return err
	}
	if err := s.validateWorker(); err != nil {
		return err
	}
	if err := s.validateVision(); err != nil {
		return err
	}
	if err := s.validateRoute(); err != nil {
		return err
	}
	if err := s.validateDiagnostics(); err != nil {
		return err
	}
	return s.validateWeb()
}

func (s Snapshot) validateDiagnostics() error {
	extensions := make([]string, 0, len(s.Config.Diagnostics.Commands))
	for extension := range s.Config.Diagnostics.Commands {
		extensions = append(extensions, extension)
	}
	slices.Sort(extensions)
	if len(extensions) > 32 {
		return fieldError(
			"diagnostics.commands",
			s.Provenance,
			"must contain at most 32 file-extension commands",
		)
	}
	for _, extension := range extensions {
		command := s.Config.Diagnostics.Commands[extension]
		nameField := fieldDiagnosticCommandName(extension)
		argsField := fieldDiagnosticCommandArgs(extension)
		if extension != strings.ToLower(extension) ||
			!diagnosticExtensionPattern.MatchString(extension) {
			return fieldError(
				nameField,
				s.Provenance,
				"extension must be a lowercase suffix such as .md",
			)
		}
		if !diagnosticCommandPattern.MatchString(command.Name) ||
			strings.ContainsAny(command.Name, `/\`) {
			return fieldError(
				nameField,
				s.Provenance,
				"name must be a PATH-resolved executable without directory separators",
			)
		}
		if len(command.Args) == 0 || len(command.Args) > 32 {
			return fieldError(argsField, s.Provenance, "args must contain between 1 and 32 entries")
		}
		hasPath := false
		totalBytes := 0
		for _, argument := range command.Args {
			totalBytes += len(argument)
			if strings.ContainsRune(argument, 0) || len(argument) > 4096 {
				return fieldError(argsField, s.Provenance, "arguments must be bounded text without NUL")
			}
			if strings.Contains(argument, "{path}") {
				hasPath = true
			}
		}
		if totalBytes > 16<<10 {
			return fieldError(argsField, s.Provenance, "arguments exceed 16384 bytes")
		}
		if !hasPath {
			return fieldError(argsField, s.Provenance, "one argument must contain {path}")
		}
	}
	return nil
}

// validateSubagent rejects a child configuration that cannot be honored, rather
// than letting a writing child discover at spawn time that it has nowhere
// isolated to write.
func (s Snapshot) validateSubagent() error {
	child := s.Config.Execution.Subagent
	switch child.Workspace {
	case SubagentWorkspaceAuto, SubagentWorkspaceReadOnly, SubagentWorkspaceWorktree,
		SubagentWorkspaceSerialized:
	default:
		return fieldError(fieldSubagentWorkspace, s.Provenance,
			"must be auto, read_only, worktree, or same_workspace_serialized")
	}
	if child.MaxDepth < 1 {
		return fieldError(fieldSubagentMaxDepth, s.Provenance, "must be positive")
	}
	if child.MaxParallel < 1 {
		return fieldError(fieldSubagentMaxParallel, s.Provenance, "must be positive")
	}
	if child.MaxSteps < 1 {
		return fieldError(fieldSubagentMaxSteps, s.Provenance, "must be positive")
	}
	if child.MaxCostUSD < 0 {
		return fieldError(fieldSubagentMaxCostUSD, s.Provenance, "must be non-negative")
	}
	if child.WallTime <= 0 {
		return fieldError(fieldSubagentWallTime, s.Provenance, "must be positive")
	}
	return nil
}

// validateWorker rejects a scheduler configuration that would either spin
// (intervals at or below zero) or lose work: a lease shorter than the interval
// at which it is renewed expires under a healthy worker, and the task is then
// taken away from a process that is still running it.
func (s Snapshot) validateWorker() error {
	worker := s.Config.Execution.Worker
	if worker.MaxParallel < 1 {
		return fieldError(fieldWorkerMaxParallel, s.Provenance, "must be positive")
	}
	if worker.MaxAttempts < 1 {
		return fieldError(fieldWorkerMaxAttempts, s.Provenance, "must be positive")
	}
	if worker.ClaimInterval <= 0 {
		return fieldError(fieldWorkerClaimInterval, s.Provenance, "must be positive")
	}
	if worker.AutomationInterval <= 0 {
		return fieldError(fieldWorkerAutomationTick, s.Provenance, "must be positive")
	}
	if worker.Lease <= 0 {
		return fieldError(fieldWorkerLease, s.Provenance, "must be positive")
	}
	if worker.Lease <= worker.ClaimInterval {
		return fieldError(fieldWorkerLease, s.Provenance,
			"must exceed execution.worker.claim_interval so a live worker keeps its lease")
	}
	if worker.RetryBackoff < 0 {
		return fieldError(fieldWorkerRetryBackoff, s.Provenance, "must be non-negative")
	}
	if worker.RetryBackoffMax < worker.RetryBackoff {
		return fieldError(fieldWorkerRetryBackoffMax, s.Provenance,
			"must be at least execution.worker.retry_backoff")
	}
	if worker.MaxCostUSD < 0 {
		return fieldError(fieldWorkerMaxCostUSD, s.Provenance, "must be non-negative")
	}
	return nil
}

// validateVerify is fail-closed: the two values the roadmap names but that have
// no implementation yet (affected scope, ask on failure) are rejected at load
// time with a pointer at the missing work, instead of silently degrading into a
// different meaning.
func (s Snapshot) validateVerify() error {
	verify := s.Config.Execution.Verify
	switch verify.Mode {
	case "off", "soft", "hard":
	default:
		return fieldError(fieldVerifyMode, s.Provenance, "must be off, soft, or hard")
	}
	switch verify.Scope {
	case "diagnostics", "repository", "affected":
	default:
		return fieldError(fieldVerifyScope, s.Provenance,
			"must be diagnostics, repository, or affected")
	}
	switch verify.OnFailure {
	case "fail", "revert":
	case "ask":
		return fieldError(fieldVerifyOnFailure, s.Provenance,
			"ask needs an interactive input request every host can render; use fail or revert")
	default:
		return fieldError(fieldVerifyOnFailure, s.Provenance, "must be fail or revert")
	}
	if strings.TrimSpace(verify.Command) != "" &&
		verify.Scope != "repository" && verify.Scope != "affected" {
		return fieldError(fieldVerifyCommand, s.Provenance,
			"only the repository and affected scopes run commands; set scope = \"repository\"")
	}
	if verify.MaxRepairSteps < 0 || verify.MaxRepairSteps > 8 {
		return fieldError(fieldVerifyRepair, s.Provenance, "must be between 0 and 8")
	}
	if verify.Timeout <= 0 {
		return fieldError(fieldVerifyTimeout, s.Provenance, "must be positive")
	}
	return nil
}

func (s Snapshot) validateVision() error {
	vision := s.Config.Vision
	if !vision.Enabled {
		return nil
	}
	if strings.TrimSpace(vision.Provider) == "" {
		return fieldError(fieldVisionProvider, s.Provenance, "required when vision.enabled is true")
	}
	if strings.TrimSpace(vision.Model) == "" {
		return fieldError(fieldVisionModel, s.Provenance, "required when vision.enabled is true")
	}
	return nil
}

// routeSlotPurposes are the purposes a slot may be configured for, in the order
// they are reported. It matches routeFileConfig: the purposes nothing samples on
// yet have no field there and no entry here.
var routeSlotPurposes = []string{"plan", "vision", "subquery"}

// validateRoute checks the slots configuration named. A half-named slot is the
// error worth catching here: a provider without a model resolves to nothing, and
// falling back to act silently would look like the slot had been honored.
func (s Snapshot) validateRoute() error {
	for _, purpose := range routeSlotPurposes {
		slot, configured := s.Config.Route.Slots[purpose]
		if !configured {
			continue
		}
		if strings.TrimSpace(slot.Provider) == "" {
			return fieldError(
				fieldRouteProvider(purpose), s.Provenance,
				"required when the "+purpose+" route is configured",
			)
		}
		if strings.TrimSpace(slot.Model) == "" {
			return fieldError(
				fieldRouteModel(purpose), s.Provenance,
				"required when the "+purpose+" route is configured",
			)
		}
	}
	return nil
}

func (s Snapshot) validateWeb() error {
	backend := strings.ToLower(strings.TrimSpace(s.Config.Web.SearchBackend))
	switch backend {
	case "", "duckduckgo", "bing", "tavily", "searxng", "bocha", "custom":
		return nil
	default:
		return fieldError(fieldWebSearchBackend, s.Provenance, "must be duckduckgo, bing, tavily, searxng, bocha, custom, or empty")
	}
}

func fieldError(field string, provenance map[string]Source, reason string) error {
	source, exists := provenance[field]
	if !exists {
		source = SourceDefault
	}
	return &FieldError{Field: field, Source: source, Reason: reason}
}

func IsFieldError(err error) bool {
	var target *FieldError
	return errors.As(err, &target)
}
