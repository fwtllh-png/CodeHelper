package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type executionFileConfig struct {
	Provider              *string  `toml:"provider"`
	Model                 *string  `toml:"model"`
	Protocol              *string  `toml:"protocol"`
	Mode                  *string  `toml:"mode"`
	Workspace             *string  `toml:"workspace"`
	Tools                 *bool    `toml:"tools"`
	MaxOutputTokens       *uint64  `toml:"max_output_tokens"`
	MaxSteps              *int     `toml:"max_steps"`
	Timeout               *string  `toml:"timeout"`
	ConnectionTimeout     *string  `toml:"connection_timeout"`
	TLSHandshakeTimeout   *string  `toml:"tls_handshake_timeout"`
	ResponseHeaderTimeout *string  `toml:"response_header_timeout"`
	IdleTimeout           *string  `toml:"idle_timeout"`
	MaxConcurrent         *int     `toml:"max_concurrent"`
	RateLimit             *float64 `toml:"rate_limit"`
	BudgetTokens          *uint64  `toml:"budget_tokens"`
	BudgetUSD             *float64 `toml:"budget_usd"`
	ReasoningEffort       *string  `toml:"reasoning_effort"`
	NativeSearch          *bool    `toml:"native_search"`
	Verify                struct {
		Mode           *string `toml:"mode"`
		Scope          *string `toml:"scope"`
		OnFailure      *string `toml:"on_failure"`
		Command        *string `toml:"command"`
		MaxRepairSteps *int    `toml:"max_repair_steps"`
		Timeout        *string `toml:"timeout"`
	} `toml:"verify"`
	Subagent struct {
		Delegation  *string  `toml:"delegation"`
		MaxDepth    *int     `toml:"max_depth"`
		MaxParallel *int     `toml:"max_parallel"`
		MaxResident *int     `toml:"max_resident"`
		MaxTotal    *int     `toml:"max_total"`
		MaxSteps    *int     `toml:"max_steps"`
		MaxTokens   *uint64  `toml:"max_tokens"`
		MaxCostUSD  *float64 `toml:"max_cost_usd"`
		WallTime    *string  `toml:"wall_time"`
		Workspace   *string  `toml:"workspace"`
	} `toml:"subagent"`
	Worker struct {
		Enabled            *bool    `toml:"enabled"`
		MaxParallel        *int     `toml:"max_parallel"`
		MaxAttempts        *int     `toml:"max_attempts"`
		Lease              *string  `toml:"lease"`
		ClaimInterval      *string  `toml:"claim_interval"`
		AutomationInterval *string  `toml:"automation_interval"`
		RetryBackoff       *string  `toml:"retry_backoff"`
		RetryBackoffMax    *string  `toml:"retry_backoff_max"`
		MaxTokens          *uint64  `toml:"max_tokens"`
		MaxCostUSD         *float64 `toml:"max_cost_usd"`
	} `toml:"worker"`
	Journal struct {
		Durable        *bool `toml:"durable"`
		RecoverOnStart *bool `toml:"recover_on_start"`
	} `toml:"journal"`
}

// routeFileConfig spells out one field per purpose instead of decoding a map.
// The decoder rejects unknown fields, so a misspelled purpose is refused at load
// time rather than accepted as a slot nothing reads. The purposes that nothing
// samples on yet (summary, judge) are absent for the same reason: a table that
// accepts them would look like it took effect.
type routeFileConfig struct {
	Lock     *bool                `toml:"lock"`
	Plan     *routeSlotFileConfig `toml:"plan"`
	Vision   *routeSlotFileConfig `toml:"vision"`
	Subquery *routeSlotFileConfig `toml:"subquery"`
}

type routeSlotFileConfig struct {
	Provider *string `toml:"provider"`
	Model    *string `toml:"model"`
}

type diagnosticCommandFileConfig struct {
	Name *string   `toml:"name"`
	Args *[]string `toml:"args"`
}

type fileConfig struct {
	Runtime struct {
		OperationBuffer  *int `toml:"operation_buffer"`
		EventHistory     *int `toml:"event_history"`
		SubscriberBuffer *int `toml:"subscriber_buffer"`
	} `toml:"runtime"`
	State struct {
		DataDir        *string `toml:"data_dir"`
		BusyTimeout    *string `toml:"busy_timeout"`
		EventRetention *int    `toml:"event_retention"`
	} `toml:"state"`
	Memory struct {
		Enabled *bool   `toml:"enabled"`
		Path    *string `toml:"path"`
	} `toml:"memory"`
	Context struct {
		Index struct {
			Enabled      *bool  `toml:"enabled"`
			MaxFileBytes *int64 `toml:"max_file_bytes"`
			MaxFiles     *int   `toml:"max_files"`
		} `toml:"index"`
		RepoMap struct {
			Enabled        *bool `toml:"enabled"`
			MaxBytes       *int  `toml:"max_bytes"`
			MaxDirectories *int  `toml:"max_directories"`
		} `toml:"repo_map"`
		WorkingSet struct {
			Enabled    *bool `toml:"enabled"`
			MaxEntries *int  `toml:"max_entries"`
			MaxBytes   *int  `toml:"max_bytes"`
		} `toml:"working_set"`
		Evidence struct {
			Enabled    *bool `toml:"enabled"`
			MaxEntries *int  `toml:"max_entries"`
			MaxBytes   *int  `toml:"max_bytes"`
		} `toml:"evidence"`
		CodingPolicy struct {
			Enabled *bool `toml:"enabled"`
		} `toml:"coding_policy"`
		Compact struct {
			AutoCompactTokens *int    `toml:"auto_compact_tokens"`
			Scope             *string `toml:"scope"`
			SummaryMaxBytes   *int    `toml:"summary_max_bytes"`
			MaxDigestEntries  *int    `toml:"max_digest_entries"`
		} `toml:"compact"`
	} `toml:"context"`
	Telemetry struct {
		LogLevel *string `toml:"log_level"`
	} `toml:"telemetry"`
	Credential struct {
		Kind *string `toml:"kind"`
		Name *string `toml:"name"`
	} `toml:"credential"`
	Execution executionFileConfig `toml:"execution"`
	Route     routeFileConfig     `toml:"route"`
	Vision    struct {
		Enabled  *bool   `toml:"enabled"`
		Provider *string `toml:"provider"`
		Model    *string `toml:"model"`
	} `toml:"vision"`
	Web struct {
		SearchBackend *string `toml:"search_backend"`
	} `toml:"web"`
	Diagnostics struct {
		Commands map[string]diagnosticCommandFileConfig `toml:"commands"`
	} `toml:"diagnostics"`
}

func applyFile(
	path string, config *Config, provenance map[string]Source, source Source, trusted bool,
) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %q: %w", path, err)
	}
	var input fileConfig
	decoder := toml.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return fmt.Errorf("decode config %q: %w", path, err)
	}
	applyInt(input.Runtime.OperationBuffer, &config.Runtime.OperationBuffer, fieldOperationBuffer, source, provenance)
	applyInt(input.Runtime.EventHistory, &config.Runtime.EventHistory, fieldEventHistory, source, provenance)
	applyInt(input.Runtime.SubscriberBuffer, &config.Runtime.SubscriberBuffer, fieldSubscriberBuffer, source, provenance)
	applyString(input.State.DataDir, &config.State.DataDir, fieldStateDataDir, source, provenance)
	applyDurationString(input.State.BusyTimeout, &config.State.BusyTimeout, fieldStateBusyTimeout, source, provenance)
	applyInt(input.State.EventRetention, &config.State.EventRetention, fieldStateRetention, source, provenance)
	applyBool(input.Memory.Enabled, &config.Memory.Enabled, fieldMemoryEnabled, source, provenance)
	applyString(input.Memory.Path, &config.Memory.Path, fieldMemoryPath, source, provenance)
	index := &config.Context.Index
	applyBool(input.Context.Index.Enabled, &index.Enabled, fieldIndexEnabled, source, provenance)
	applyInt64(input.Context.Index.MaxFileBytes, &index.MaxFileBytes, fieldIndexMaxBytes, source, provenance)
	applyInt(input.Context.Index.MaxFiles, &index.MaxFiles, fieldIndexMaxFiles, source, provenance)
	repoMap := &config.Context.RepoMap
	applyBool(input.Context.RepoMap.Enabled, &repoMap.Enabled, fieldRepoMapEnabled, source, provenance)
	applyInt(input.Context.RepoMap.MaxBytes, &repoMap.MaxBytes, fieldRepoMapMaxBytes, source, provenance)
	applyInt(
		input.Context.RepoMap.MaxDirectories, &repoMap.MaxDirectories,
		fieldRepoMapMaxDirectories, source, provenance,
	)
	workingSet := &config.Context.WorkingSet
	applyBool(
		input.Context.WorkingSet.Enabled, &workingSet.Enabled,
		fieldWorkingSetEnabled, source, provenance,
	)
	applyInt(
		input.Context.WorkingSet.MaxEntries, &workingSet.MaxEntries,
		fieldWorkingSetMaxEntries, source, provenance,
	)
	applyInt(
		input.Context.WorkingSet.MaxBytes, &workingSet.MaxBytes,
		fieldWorkingSetMaxBytes, source, provenance,
	)
	evidence := &config.Context.Evidence
	applyBool(
		input.Context.Evidence.Enabled, &evidence.Enabled,
		fieldEvidenceEnabled, source, provenance,
	)
	applyInt(
		input.Context.Evidence.MaxEntries, &evidence.MaxEntries,
		fieldEvidenceMaxEntries, source, provenance,
	)
	applyInt(
		input.Context.Evidence.MaxBytes, &evidence.MaxBytes,
		fieldEvidenceMaxBytes, source, provenance,
	)
	applyBool(
		input.Context.CodingPolicy.Enabled, &config.Context.CodingPolicy.Enabled,
		fieldCodingPolicyEnabled, source, provenance,
	)
	compaction := &config.Context.Compact
	applyInt(
		input.Context.Compact.AutoCompactTokens, &compaction.AutoCompactTokens,
		fieldCompactAutoTokens, source, provenance,
	)
	applyString(
		input.Context.Compact.Scope, &compaction.Scope,
		fieldCompactScope, source, provenance,
	)
	applyInt(
		input.Context.Compact.SummaryMaxBytes, &compaction.SummaryMaxBytes,
		fieldCompactSummaryMax, source, provenance,
	)
	applyInt(
		input.Context.Compact.MaxDigestEntries, &compaction.MaxDigestEntries,
		fieldCompactMaxDigest, source, provenance,
	)
	applyString(input.Telemetry.LogLevel, &config.Telemetry.LogLevel, fieldLogLevel, source, provenance)
	if trusted {
		applyString(input.Credential.Kind, &config.Credential.Kind, fieldCredentialKind, source, provenance)
		applyString(input.Credential.Name, &config.Credential.Name, fieldCredentialName, source, provenance)
	}
	applyExecutionFile(input.Execution, config, provenance, source, trusted)
	applyRouteFile(input.Route, config, provenance, source, trusted)
	applyBool(input.Vision.Enabled, &config.Vision.Enabled, fieldVisionEnabled, source, provenance)
	applyString(input.Vision.Provider, &config.Vision.Provider, fieldVisionProvider, source, provenance)
	applyString(input.Vision.Model, &config.Vision.Model, fieldVisionModel, source, provenance)
	applyString(input.Web.SearchBackend, &config.Web.SearchBackend, fieldWebSearchBackend, source, provenance)
	applyDiagnosticsFile(input.Diagnostics.Commands, config, provenance, source, trusted)
	return nil
}

func applyDiagnosticsFile(
	input map[string]diagnosticCommandFileConfig,
	config *Config,
	provenance map[string]Source,
	source Source,
	trusted bool,
) {
	if !trusted || len(input) == 0 {
		return
	}
	if config.Diagnostics.Commands == nil {
		config.Diagnostics.Commands = make(map[string]DiagnosticCommand)
	}
	for extension, value := range input {
		command := config.Diagnostics.Commands[extension]
		if value.Name != nil {
			command.Name = *value.Name
			provenance[fieldDiagnosticCommandName(extension)] = source
		}
		if value.Args != nil {
			command.Args = append([]string(nil), (*value.Args)...)
			provenance[fieldDiagnosticCommandArgs(extension)] = source
		}
		config.Diagnostics.Commands[extension] = command
	}
}

func applyExecutionFile(
	input executionFileConfig,
	config *Config,
	provenance map[string]Source,
	source Source,
	trusted bool,
) {
	execution := &config.Execution
	if trusted {
		applyString(input.Provider, &execution.Provider, fieldProvider, source, provenance)
		applyString(input.Model, &execution.Model, fieldModel, source, provenance)
		applyString(input.Protocol, &execution.Protocol, fieldProtocol, source, provenance)
	}
	applyString(input.Mode, &execution.Mode, fieldMode, source, provenance)
	applyString(input.Workspace, &execution.Workspace, fieldWorkspace, source, provenance)
	applyBool(input.Tools, &execution.Tools, fieldTools, source, provenance)
	applyUint64(input.MaxOutputTokens, &execution.MaxOutputTokens, fieldMaxOutputTokens, source, provenance)
	applyInt(input.MaxSteps, &execution.MaxSteps, fieldMaxSteps, source, provenance)
	applyDurationString(input.Timeout, &execution.Timeout, fieldTimeout, source, provenance)
	applyDurationString(input.ConnectionTimeout, &execution.ConnectionTimeout, fieldConnectionTimeout, source, provenance)
	applyDurationString(input.TLSHandshakeTimeout, &execution.TLSHandshakeTimeout, fieldTLSHandshakeTimeout, source, provenance)
	applyDurationString(input.ResponseHeaderTimeout, &execution.ResponseHeaderTimeout, fieldResponseHeaderTimeout, source, provenance)
	applyDurationString(input.IdleTimeout, &execution.IdleTimeout, fieldIdleTimeout, source, provenance)
	applyInt(input.MaxConcurrent, &execution.MaxConcurrent, fieldMaxConcurrent, source, provenance)
	applyFloat64(input.RateLimit, &execution.RateLimit, fieldRateLimit, source, provenance)
	applyUint64(input.BudgetTokens, &execution.BudgetTokens, fieldBudgetTokens, source, provenance)
	applyFloat64(input.BudgetUSD, &execution.BudgetUSD, fieldBudgetUSD, source, provenance)
	applyString(input.ReasoningEffort, &execution.ReasoningEffort, fieldReasoning, source, provenance)
	applyBool(input.NativeSearch, &execution.NativeSearch, fieldNativeSearch, source, provenance)
	verify := &execution.Verify
	applyString(input.Verify.Mode, &verify.Mode, fieldVerifyMode, source, provenance)
	applyString(input.Verify.Scope, &verify.Scope, fieldVerifyScope, source, provenance)
	applyString(input.Verify.OnFailure, &verify.OnFailure, fieldVerifyOnFailure, source, provenance)
	applyString(input.Verify.Command, &verify.Command, fieldVerifyCommand, source, provenance)
	applyInt(input.Verify.MaxRepairSteps, &verify.MaxRepairSteps, fieldVerifyRepair, source, provenance)
	applyDurationString(input.Verify.Timeout, &verify.Timeout, fieldVerifyTimeout, source, provenance)
	child := &execution.Subagent
	applyString(input.Subagent.Delegation, &child.Delegation, fieldSubagentDelegation, source, provenance)
	applyInt(input.Subagent.MaxDepth, &child.MaxDepth, fieldSubagentMaxDepth, source, provenance)
	applyInt(input.Subagent.MaxParallel, &child.MaxParallel, fieldSubagentMaxParallel, source, provenance)
	applyInt(input.Subagent.MaxResident, &child.MaxResident, fieldSubagentMaxResident, source, provenance)
	applyInt(input.Subagent.MaxTotal, &child.MaxTotal, fieldSubagentMaxTotal, source, provenance)
	applyInt(input.Subagent.MaxSteps, &child.MaxSteps, fieldSubagentMaxSteps, source, provenance)
	applyUint64(input.Subagent.MaxTokens, &child.MaxTokens, fieldSubagentMaxTokens, source, provenance)
	applyFloat64(input.Subagent.MaxCostUSD, &child.MaxCostUSD, fieldSubagentMaxCostUSD, source, provenance)
	applyDurationString(input.Subagent.WallTime, &child.WallTime, fieldSubagentWallTime, source, provenance)
	applyString(input.Subagent.Workspace, &child.Workspace, fieldSubagentWorkspace, source, provenance)
	worker := &execution.Worker
	applyBool(input.Worker.Enabled, &worker.Enabled, fieldWorkerEnabled, source, provenance)
	applyInt(input.Worker.MaxParallel, &worker.MaxParallel, fieldWorkerMaxParallel, source, provenance)
	applyInt(input.Worker.MaxAttempts, &worker.MaxAttempts, fieldWorkerMaxAttempts, source, provenance)
	applyDurationString(input.Worker.Lease, &worker.Lease, fieldWorkerLease, source, provenance)
	applyDurationString(
		input.Worker.ClaimInterval, &worker.ClaimInterval, fieldWorkerClaimInterval, source, provenance,
	)
	applyDurationString(
		input.Worker.AutomationInterval, &worker.AutomationInterval,
		fieldWorkerAutomationTick, source, provenance,
	)
	applyDurationString(
		input.Worker.RetryBackoff, &worker.RetryBackoff, fieldWorkerRetryBackoff, source, provenance,
	)
	applyDurationString(
		input.Worker.RetryBackoffMax, &worker.RetryBackoffMax,
		fieldWorkerRetryBackoffMax, source, provenance,
	)
	applyUint64(input.Worker.MaxTokens, &worker.MaxTokens, fieldWorkerMaxTokens, source, provenance)
	applyFloat64(input.Worker.MaxCostUSD, &worker.MaxCostUSD, fieldWorkerMaxCostUSD, source, provenance)
	journal := &execution.Journal
	applyBool(input.Journal.Durable, &journal.Durable, fieldJournalDurable, source, provenance)
	applyBool(
		input.Journal.RecoverOnStart, &journal.RecoverOnStart,
		fieldJournalRecoverOnStart, source, provenance,
	)
}

// applyRouteFile folds the [route] table in.
//
// The whole section is trusted-only, for the same reason execution.provider is:
// a slot names an endpoint and a credential, so a workspace-local file that
// could set one could redirect a session's traffic.
func applyRouteFile(
	input routeFileConfig,
	config *Config,
	provenance map[string]Source,
	source Source,
	trusted bool,
) {
	if !trusted {
		return
	}
	applyBool(input.Lock, &config.Route.Lock, fieldRouteLock, source, provenance)
	slots := []struct {
		purpose string
		input   *routeSlotFileConfig
	}{
		{purpose: "plan", input: input.Plan},
		{purpose: "vision", input: input.Vision},
		{purpose: "subquery", input: input.Subquery},
	}
	for _, slot := range slots {
		if slot.input == nil {
			continue
		}
		existing := config.Route.Slots[slot.purpose]
		applyString(
			slot.input.Provider, &existing.Provider,
			fieldRouteProvider(slot.purpose), source, provenance,
		)
		applyString(
			slot.input.Model, &existing.Model,
			fieldRouteModel(slot.purpose), source, provenance,
		)
		if existing.Empty() {
			continue
		}
		if config.Route.Slots == nil {
			config.Route.Slots = make(map[string]RouteSlot)
		}
		config.Route.Slots[slot.purpose] = existing
	}
}
