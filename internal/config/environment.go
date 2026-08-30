package config

import (
	"fmt"
	"strconv"
	"time"
)

func applyEnvironment(lookup func(string) (string, bool), config *Config, provenance map[string]Source) error {
	integerFields := []struct {
		env    string
		field  string
		target *int
	}{
		{"CODEHELPER_RUNTIME_OPERATION_BUFFER", fieldOperationBuffer, &config.Runtime.OperationBuffer},
		{"CODEHELPER_RUNTIME_EVENT_HISTORY", fieldEventHistory, &config.Runtime.EventHistory},
		{"CODEHELPER_RUNTIME_SUBSCRIBER_BUFFER", fieldSubscriberBuffer, &config.Runtime.SubscriberBuffer},
	}
	for _, item := range integerFields {
		value, exists := lookup(item.env)
		if !exists {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return &FieldError{Field: item.field, Source: SourceEnv, Reason: fmt.Sprintf("%s must be an integer", item.env)}
		}
		*item.target = parsed
		provenance[item.field] = SourceEnv
	}
	applyEnvString(lookup, "CODEHELPER_LOG_LEVEL", fieldLogLevel, &config.Telemetry.LogLevel, provenance)
	applyEnvString(lookup, "CODEHELPER_CREDENTIAL_KIND", fieldCredentialKind, &config.Credential.Kind, provenance)
	applyEnvString(lookup, "CODEHELPER_CREDENTIAL_NAME", fieldCredentialName, &config.Credential.Name, provenance)
	applyEnvString(lookup, "CODEHELPER_STATE_DATA_DIR", fieldStateDataDir, &config.State.DataDir, provenance)
	if err := applyEnvDuration(lookup, "CODEHELPER_STATE_BUSY_TIMEOUT", fieldStateBusyTimeout, &config.State.BusyTimeout, provenance); err != nil {
		return err
	}
	if err := applyEnvInt(lookup, "CODEHELPER_STATE_EVENT_RETENTION", fieldStateRetention, &config.State.EventRetention, provenance); err != nil {
		return err
	}
	if err := applyEnvBool(lookup, "CODEHELPER_MEMORY_ENABLED", fieldMemoryEnabled, &config.Memory.Enabled, provenance); err != nil {
		return err
	}
	applyEnvString(lookup, "CODEHELPER_MEMORY_PATH", fieldMemoryPath, &config.Memory.Path, provenance)
	if err := applyEnvInt(lookup, "CODEHELPER_MEMORY_MAX_CANDIDATES", fieldMemoryMaxCandidates, &config.Memory.MaxCandidates, provenance); err != nil {
		return err
	}
	if err := applyEnvInt(lookup, "CODEHELPER_MEMORY_MAX_PROMPT_BYTES", fieldMemoryMaxPromptBytes, &config.Memory.MaxPromptBytes, provenance); err != nil {
		return err
	}
	if err := applyEnvBool(lookup, "CODEHELPER_MEMORY_SEMANTIC_RERANK", fieldMemorySemanticRerank, &config.Memory.SemanticRerank, provenance); err != nil {
		return err
	}
	index := &config.Context.Index
	if err := applyEnvBool(lookup, "CODEHELPER_INDEX_ENABLED", fieldIndexEnabled, &index.Enabled, provenance); err != nil {
		return err
	}
	if err := applyEnvInt64(
		lookup, "CODEHELPER_INDEX_MAX_FILE_BYTES", fieldIndexMaxBytes, &index.MaxFileBytes, provenance,
	); err != nil {
		return err
	}
	if err := applyEnvInt(
		lookup, "CODEHELPER_INDEX_MAX_FILES", fieldIndexMaxFiles, &index.MaxFiles, provenance,
	); err != nil {
		return err
	}
	repoMap := &config.Context.RepoMap
	if err := applyEnvBool(
		lookup, "CODEHELPER_REPO_MAP_ENABLED", fieldRepoMapEnabled, &repoMap.Enabled, provenance,
	); err != nil {
		return err
	}
	if err := applyEnvInt(
		lookup, "CODEHELPER_REPO_MAP_MAX_BYTES", fieldRepoMapMaxBytes, &repoMap.MaxBytes, provenance,
	); err != nil {
		return err
	}
	if err := applyEnvInt(
		lookup, "CODEHELPER_REPO_MAP_MAX_DIRECTORIES", fieldRepoMapMaxDirectories,
		&repoMap.MaxDirectories, provenance,
	); err != nil {
		return err
	}
	workingSet := &config.Context.WorkingSet
	if err := applyEnvBool(
		lookup, "CODEHELPER_WORKING_SET_ENABLED", fieldWorkingSetEnabled, &workingSet.Enabled, provenance,
	); err != nil {
		return err
	}
	if err := applyEnvInt(
		lookup, "CODEHELPER_WORKING_SET_MAX_ENTRIES", fieldWorkingSetMaxEntries,
		&workingSet.MaxEntries, provenance,
	); err != nil {
		return err
	}
	if err := applyEnvInt(
		lookup, "CODEHELPER_WORKING_SET_MAX_BYTES", fieldWorkingSetMaxBytes,
		&workingSet.MaxBytes, provenance,
	); err != nil {
		return err
	}
	evidence := &config.Context.Evidence
	if err := applyEnvBool(
		lookup, "CODEHELPER_EVIDENCE_ENABLED", fieldEvidenceEnabled, &evidence.Enabled, provenance,
	); err != nil {
		return err
	}
	if err := applyEnvInt(
		lookup, "CODEHELPER_EVIDENCE_MAX_ENTRIES", fieldEvidenceMaxEntries,
		&evidence.MaxEntries, provenance,
	); err != nil {
		return err
	}
	if err := applyEnvInt(
		lookup, "CODEHELPER_EVIDENCE_MAX_BYTES", fieldEvidenceMaxBytes,
		&evidence.MaxBytes, provenance,
	); err != nil {
		return err
	}
	if err := applyEnvBool(
		lookup, "CODEHELPER_CODING_POLICY_ENABLED", fieldCodingPolicyEnabled,
		&config.Context.CodingPolicy.Enabled, provenance,
	); err != nil {
		return err
	}
	compaction := &config.Context.Compact
	if err := applyEnvInt(
		lookup, "CODEHELPER_COMPACT_PREPARE_TOKENS", fieldCompactPrepareTokens,
		&compaction.PrepareTokens, provenance,
	); err != nil {
		return err
	}
	if err := applyEnvInt(
		lookup, "CODEHELPER_COMPACT_AUTO_TOKENS", fieldCompactAutoTokens,
		&compaction.AutoCompactTokens, provenance,
	); err != nil {
		return err
	}
	if err := applyEnvInt(
		lookup, "CODEHELPER_COMPACT_EMERGENCY_TOKENS", fieldCompactEmergencyTokens,
		&compaction.EmergencyTokens, provenance,
	); err != nil {
		return err
	}
	applyEnvString(
		lookup, "CODEHELPER_COMPACT_SCOPE", fieldCompactScope,
		&compaction.Scope, provenance,
	)
	if err := applyEnvInt(
		lookup, "CODEHELPER_COMPACT_SUMMARY_MAX_BYTES", fieldCompactSummaryMax,
		&compaction.SummaryMaxBytes, provenance,
	); err != nil {
		return err
	}
	if err := applyEnvInt(
		lookup, "CODEHELPER_COMPACT_MAX_DIGEST_ENTRIES", fieldCompactMaxDigest,
		&compaction.MaxDigestEntries, provenance,
	); err != nil {
		return err
	}
	for _, value := range []struct {
		env    string
		field  string
		target *int
	}{
		{"CODEHELPER_COMPACT_TRUTH_MAX_BYTES", fieldCompactTruthMaxBytes, &compaction.TruthMaxBytes},
		{"CODEHELPER_COMPACT_TRUTH_MAX_ENTITIES", fieldCompactTruthMaxEntities, &compaction.TruthMaxEntities},
		{"CODEHELPER_COMPACT_MANDATORY_MAX_ENTITIES", fieldCompactMandatoryMaxEntities, &compaction.MandatoryMaxEntities},
		{"CODEHELPER_COMPACT_FACT_MAX_ENTITIES", fieldCompactFactMaxEntities, &compaction.FactMaxEntities},
		{"CODEHELPER_COMPACT_VERIFIED_CHANGE_RETENTION_TURNS", fieldCompactVerifiedChangeRetentionTurns, &compaction.VerifiedChangeRetentionTurns},
		{"CODEHELPER_COMPACT_FAILURE_MAX_ENTITIES", fieldCompactFailureMaxEntities, &compaction.FailureMaxEntities},
		{"CODEHELPER_COMPACT_HANDLE_MAX_ENTITIES", fieldCompactHandleMaxEntities, &compaction.HandleMaxEntities},
		{"CODEHELPER_COMPACT_OMISSION_SAMPLE_MAX_ENTITIES", fieldCompactOmissionSampleMaxEntities, &compaction.OmissionSampleMaxEntities},
		{"CODEHELPER_COMPACT_RECENT_TAIL_TURNS", fieldCompactRecentTailTurns, &compaction.RecentTailTurns},
		{"CODEHELPER_COMPACT_RECENT_TAIL_MAX_TOKENS", fieldCompactRecentTailMaxTokens, &compaction.RecentTailMaxTokens},
		{"CODEHELPER_COMPACT_SEMANTIC_NARRATIVE_MAX_INPUT_TOKENS", fieldCompactSemanticNarrativeMaxInputTokens, &compaction.SemanticNarrativeMaxInputTokens},
		{"CODEHELPER_COMPACT_SEMANTIC_NARRATIVE_MAX_OUTPUT_TOKENS", fieldCompactSemanticNarrativeMaxOutputTokens, &compaction.SemanticNarrativeMaxOutputTokens},
		{"CODEHELPER_COMPACT_SEMANTIC_NARRATIVE_MAX_ITEMS", fieldCompactSemanticNarrativeMaxItems, &compaction.SemanticNarrativeMaxItems},
		{"CODEHELPER_COMPACT_SEMANTIC_NARRATIVE_ITEM_MAX_BYTES", fieldCompactSemanticNarrativeItemMaxBytes, &compaction.SemanticNarrativeItemMaxBytes},
		{"CODEHELPER_COMPACT_SEMANTIC_NARRATIVE_RETRY_LIMIT", fieldCompactSemanticNarrativeRetryLimit, &compaction.SemanticNarrativeRetryLimit},
		{"CODEHELPER_COMPACT_OWNER_DELTA_MAX_SEGMENTS", fieldCompactOwnerDeltaMaxSegments, &compaction.OwnerDeltaMaxSegments},
		{"CODEHELPER_COMPACT_OWNER_DELTA_MAX_BYTES", fieldCompactOwnerDeltaMaxBytes, &compaction.OwnerDeltaMaxBytes},
	} {
		if err := applyEnvInt(lookup, value.env, value.field, value.target, provenance); err != nil {
			return err
		}
	}
	applyEnvString(
		lookup,
		"CODEHELPER_COMPACT_SEMANTIC_NARRATIVE",
		fieldCompactSemanticNarrative,
		&compaction.SemanticNarrative,
		provenance,
	)
	if err := applyEnvDuration(
		lookup,
		"CODEHELPER_COMPACT_SEMANTIC_NARRATIVE_TIMEOUT",
		fieldCompactSemanticNarrativeTimeout,
		&compaction.SemanticNarrativeTimeout,
		provenance,
	); err != nil {
		return err
	}
	execution := &config.Execution
	applyEnvString(lookup, "CODEHELPER_PROVIDER", fieldProvider, &execution.Provider, provenance)
	applyEnvString(lookup, "CODEHELPER_MODEL", fieldModel, &execution.Model, provenance)
	applyEnvString(lookup, "CODEHELPER_PROTOCOL", fieldProtocol, &execution.Protocol, provenance)
	applyEnvString(lookup, "CODEHELPER_MODE", fieldMode, &execution.Mode, provenance)
	applyEnvString(lookup, "CODEHELPER_WORKSPACE", fieldWorkspace, &execution.Workspace, provenance)
	if err := applyEnvBool(lookup, "CODEHELPER_TOOLS", fieldTools, &execution.Tools, provenance); err != nil {
		return err
	}
	if err := applyEnvUint64(lookup, "CODEHELPER_MAX_OUTPUT_TOKENS", fieldMaxOutputTokens, &execution.MaxOutputTokens, provenance); err != nil {
		return err
	}
	if err := applyEnvInt(lookup, "CODEHELPER_MAX_STEPS", fieldMaxSteps, &execution.MaxSteps, provenance); err != nil {
		return err
	}
	if err := applyEnvDuration(lookup, "CODEHELPER_TIMEOUT", fieldTimeout, &execution.Timeout, provenance); err != nil {
		return err
	}
	if err := applyEnvDuration(lookup, "CODEHELPER_LEASE_TIMEOUT", fieldLeaseTimeout, &execution.LeaseTimeout, provenance); err != nil {
		return err
	}
	if err := applyEnvDuration(lookup, "CODEHELPER_APPROVAL_TIMEOUT", fieldApprovalTimeout, &execution.ApprovalTimeout, provenance); err != nil {
		return err
	}
	if err := applyEnvDuration(lookup, "CODEHELPER_CONNECTION_TIMEOUT", fieldConnectionTimeout, &execution.ConnectionTimeout, provenance); err != nil {
		return err
	}
	if err := applyEnvDuration(lookup, "CODEHELPER_TLS_HANDSHAKE_TIMEOUT", fieldTLSHandshakeTimeout, &execution.TLSHandshakeTimeout, provenance); err != nil {
		return err
	}
	if err := applyEnvDuration(lookup, "CODEHELPER_RESPONSE_HEADER_TIMEOUT", fieldResponseHeaderTimeout, &execution.ResponseHeaderTimeout, provenance); err != nil {
		return err
	}
	if err := applyEnvDuration(lookup, "CODEHELPER_IDLE_TIMEOUT", fieldIdleTimeout, &execution.IdleTimeout, provenance); err != nil {
		return err
	}
	if err := applyEnvInt(lookup, "CODEHELPER_MAX_CONCURRENT", fieldMaxConcurrent, &execution.MaxConcurrent, provenance); err != nil {
		return err
	}
	if err := applyEnvFloat64(lookup, "CODEHELPER_RATE_LIMIT", fieldRateLimit, &execution.RateLimit, provenance); err != nil {
		return err
	}
	if err := applyEnvInt(lookup, "CODEHELPER_PROVIDER_RETRY_LIMIT", fieldProviderRetryLimit, &execution.ProviderRetryLimit, provenance); err != nil {
		return err
	}
	if err := applyEnvUint64(lookup, "CODEHELPER_BUDGET_TOKENS", fieldBudgetTokens, &execution.BudgetTokens, provenance); err != nil {
		return err
	}
	if err := applyEnvUint64(lookup, "CODEHELPER_TURN_BUDGET_TOKENS", fieldTurnBudgetTokens, &execution.TurnBudgetTokens, provenance); err != nil {
		return err
	}
	if err := applyEnvFloat64(lookup, "CODEHELPER_BUDGET_USD", fieldBudgetUSD, &execution.BudgetUSD, provenance); err != nil {
		return err
	}
	applyEnvString(lookup, "CODEHELPER_REASONING_EFFORT", fieldReasoning, &execution.ReasoningEffort, provenance)
	if err := applyEnvBool(lookup, "CODEHELPER_NATIVE_SEARCH", fieldNativeSearch, &execution.NativeSearch, provenance); err != nil {
		return err
	}
	applyEnvString(
		lookup,
		"CODEHELPER_SUBAGENT_DELEGATION",
		fieldSubagentDelegation,
		&execution.Subagent.Delegation,
		provenance,
	)
	if err := applyEnvInt(
		lookup, "CODEHELPER_SUBAGENT_MAX_DEPTH",
		fieldSubagentMaxDepth, &execution.Subagent.MaxDepth, provenance,
	); err != nil {
		return err
	}
	if err := applyEnvInt(
		lookup, "CODEHELPER_SUBAGENT_MAX_PARALLEL",
		fieldSubagentMaxParallel, &execution.Subagent.MaxParallel, provenance,
	); err != nil {
		return err
	}
	if err := applyEnvInt(
		lookup, "CODEHELPER_SUBAGENT_MAX_RESIDENT",
		fieldSubagentMaxResident, &execution.Subagent.MaxResident, provenance,
	); err != nil {
		return err
	}
	if err := applyEnvInt(
		lookup, "CODEHELPER_SUBAGENT_MAX_TOTAL",
		fieldSubagentMaxTotal, &execution.Subagent.MaxTotal, provenance,
	); err != nil {
		return err
	}
	if err := applyEnvInt(
		lookup, "CODEHELPER_SUBAGENT_MAX_STEPS",
		fieldSubagentMaxSteps, &execution.Subagent.MaxSteps, provenance,
	); err != nil {
		return err
	}
	if err := applyEnvUint64(
		lookup, "CODEHELPER_SUBAGENT_MAX_TOKENS",
		fieldSubagentMaxTokens, &execution.Subagent.MaxTokens, provenance,
	); err != nil {
		return err
	}
	if err := applyEnvFloat64(
		lookup, "CODEHELPER_SUBAGENT_MAX_COST_USD",
		fieldSubagentMaxCostUSD, &execution.Subagent.MaxCostUSD, provenance,
	); err != nil {
		return err
	}
	if err := applyEnvDuration(
		lookup, "CODEHELPER_SUBAGENT_WALL_TIME",
		fieldSubagentWallTime, &execution.Subagent.WallTime, provenance,
	); err != nil {
		return err
	}
	applyEnvString(
		lookup, "CODEHELPER_SUBAGENT_WORKSPACE",
		fieldSubagentWorkspace, &execution.Subagent.Workspace, provenance,
	)
	verify := &execution.Verify
	applyEnvString(lookup, "CODEHELPER_VERIFY_MODE", fieldVerifyMode, &verify.Mode, provenance)
	applyEnvString(lookup, "CODEHELPER_VERIFY_SCOPE", fieldVerifyScope, &verify.Scope, provenance)
	applyEnvString(lookup, "CODEHELPER_VERIFY_ON_FAILURE", fieldVerifyOnFailure, &verify.OnFailure, provenance)
	applyEnvString(lookup, "CODEHELPER_VERIFY_COMMAND", fieldVerifyCommand, &verify.Command, provenance)
	if err := applyEnvInt(
		lookup, "CODEHELPER_VERIFY_MAX_REPAIR_STEPS", fieldVerifyRepair, &verify.MaxRepairSteps, provenance,
	); err != nil {
		return err
	}
	if err := applyEnvDuration(
		lookup, "CODEHELPER_VERIFY_TIMEOUT", fieldVerifyTimeout, &verify.Timeout, provenance,
	); err != nil {
		return err
	}
	if err := applyEnvBool(lookup, "CODEHELPER_VISION_ENABLED", fieldVisionEnabled, &config.Vision.Enabled, provenance); err != nil {
		return err
	}
	applyEnvString(lookup, "CODEHELPER_VISION_PROVIDER", fieldVisionProvider, &config.Vision.Provider, provenance)
	applyEnvString(lookup, "CODEHELPER_VISION_MODEL", fieldVisionModel, &config.Vision.Model, provenance)
	applyEnvString(lookup, "CODEHELPER_WEB_SEARCH_BACKEND", fieldWebSearchBackend, &config.Web.SearchBackend, provenance)
	return nil
}

func applyEnvString(lookup func(string) (string, bool), env, field string, target *string, provenance map[string]Source) {
	if value, exists := lookup(env); exists {
		*target = value
		provenance[field] = SourceEnv
	}
}

func applyEnvInt(lookup func(string) (string, bool), env, field string, target *int, provenance map[string]Source) error {
	value, exists := lookup(env)
	if !exists {
		return nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return &FieldError{Field: field, Source: SourceEnv, Reason: env + " must be an integer"}
	}
	*target = parsed
	provenance[field] = SourceEnv
	return nil
}

func applyEnvInt64(lookup func(string) (string, bool), env, field string, target *int64, provenance map[string]Source) error {
	value, exists := lookup(env)
	if !exists {
		return nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return &FieldError{Field: field, Source: SourceEnv, Reason: env + " must be an integer"}
	}
	*target = parsed
	provenance[field] = SourceEnv
	return nil
}

func applyEnvUint64(lookup func(string) (string, bool), env, field string, target *uint64, provenance map[string]Source) error {
	value, exists := lookup(env)
	if !exists {
		return nil
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return &FieldError{Field: field, Source: SourceEnv, Reason: env + " must be an unsigned integer"}
	}
	*target = parsed
	provenance[field] = SourceEnv
	return nil
}

func applyEnvFloat64(lookup func(string) (string, bool), env, field string, target *float64, provenance map[string]Source) error {
	value, exists := lookup(env)
	if !exists {
		return nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return &FieldError{Field: field, Source: SourceEnv, Reason: env + " must be a number"}
	}
	*target = parsed
	provenance[field] = SourceEnv
	return nil
}

func applyEnvBool(lookup func(string) (string, bool), env, field string, target *bool, provenance map[string]Source) error {
	value, exists := lookup(env)
	if !exists {
		return nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return &FieldError{Field: field, Source: SourceEnv, Reason: env + " must be a boolean"}
	}
	*target = parsed
	provenance[field] = SourceEnv
	return nil
}

func applyEnvDuration(lookup func(string) (string, bool), env, field string, target *time.Duration, provenance map[string]Source) error {
	value, exists := lookup(env)
	if !exists {
		return nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return &FieldError{Field: field, Source: SourceEnv, Reason: env + " must be a duration"}
	}
	*target = parsed
	provenance[field] = SourceEnv
	return nil
}
