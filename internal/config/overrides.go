package config

func applyOverrides(overrides Overrides, config *Config, provenance map[string]Source) {
	applyInt(overrides.OperationBuffer, &config.Runtime.OperationBuffer, fieldOperationBuffer, SourceCLI, provenance)
	applyInt(overrides.EventHistory, &config.Runtime.EventHistory, fieldEventHistory, SourceCLI, provenance)
	applyInt(overrides.SubscriberBuffer, &config.Runtime.SubscriberBuffer, fieldSubscriberBuffer, SourceCLI, provenance)
	applyString(overrides.StateDataDir, &config.State.DataDir, fieldStateDataDir, SourceCLI, provenance)
	applyDuration(overrides.StateBusyTimeout, &config.State.BusyTimeout, fieldStateBusyTimeout, SourceCLI, provenance)
	applyInt(overrides.StateRetention, &config.State.EventRetention, fieldStateRetention, SourceCLI, provenance)
	applyBool(overrides.MemoryEnabled, &config.Memory.Enabled, fieldMemoryEnabled, SourceCLI, provenance)
	applyString(overrides.MemoryPath, &config.Memory.Path, fieldMemoryPath, SourceCLI, provenance)
	index := &config.Context.Index
	applyBool(overrides.IndexEnabled, &index.Enabled, fieldIndexEnabled, SourceCLI, provenance)
	applyInt64(overrides.IndexMaxBytes, &index.MaxFileBytes, fieldIndexMaxBytes, SourceCLI, provenance)
	applyInt(overrides.IndexMaxFiles, &index.MaxFiles, fieldIndexMaxFiles, SourceCLI, provenance)
	repoMap := &config.Context.RepoMap
	applyBool(overrides.RepoMapEnabled, &repoMap.Enabled, fieldRepoMapEnabled, SourceCLI, provenance)
	applyInt(overrides.RepoMapMaxBytes, &repoMap.MaxBytes, fieldRepoMapMaxBytes, SourceCLI, provenance)
	applyInt(
		overrides.RepoMapMaxDirectories, &repoMap.MaxDirectories,
		fieldRepoMapMaxDirectories, SourceCLI, provenance,
	)
	workingSet := &config.Context.WorkingSet
	applyBool(
		overrides.WorkingSetEnabled, &workingSet.Enabled,
		fieldWorkingSetEnabled, SourceCLI, provenance,
	)
	applyInt(
		overrides.WorkingSetMaxEntries, &workingSet.MaxEntries,
		fieldWorkingSetMaxEntries, SourceCLI, provenance,
	)
	applyInt(
		overrides.WorkingSetMaxBytes, &workingSet.MaxBytes,
		fieldWorkingSetMaxBytes, SourceCLI, provenance,
	)
	evidence := &config.Context.Evidence
	applyBool(overrides.EvidenceEnabled, &evidence.Enabled, fieldEvidenceEnabled, SourceCLI, provenance)
	applyInt(
		overrides.EvidenceMaxEntries, &evidence.MaxEntries,
		fieldEvidenceMaxEntries, SourceCLI, provenance,
	)
	applyInt(overrides.EvidenceMaxBytes, &evidence.MaxBytes, fieldEvidenceMaxBytes, SourceCLI, provenance)
	applyBool(
		overrides.CodingPolicyEnabled, &config.Context.CodingPolicy.Enabled,
		fieldCodingPolicyEnabled, SourceCLI, provenance,
	)
	compaction := &config.Context.Compact
	applyInt(
		overrides.CompactAutoTokens, &compaction.AutoCompactTokens,
		fieldCompactAutoTokens, SourceCLI, provenance,
	)
	applyString(
		overrides.CompactScope, &compaction.Scope,
		fieldCompactScope, SourceCLI, provenance,
	)
	applyInt(
		overrides.CompactSummaryMax, &compaction.SummaryMaxBytes,
		fieldCompactSummaryMax, SourceCLI, provenance,
	)
	applyInt(
		overrides.CompactMaxDigest, &compaction.MaxDigestEntries,
		fieldCompactMaxDigest, SourceCLI, provenance,
	)
	applyString(overrides.LogLevel, &config.Telemetry.LogLevel, fieldLogLevel, SourceCLI, provenance)
	applyString(overrides.CredentialKind, &config.Credential.Kind, fieldCredentialKind, SourceCLI, provenance)
	applyString(overrides.CredentialName, &config.Credential.Name, fieldCredentialName, SourceCLI, provenance)
	execution := &config.Execution
	applyString(overrides.Provider, &execution.Provider, fieldProvider, SourceCLI, provenance)
	applyString(overrides.Model, &execution.Model, fieldModel, SourceCLI, provenance)
	applyString(overrides.Protocol, &execution.Protocol, fieldProtocol, SourceCLI, provenance)
	applyString(overrides.Mode, &execution.Mode, fieldMode, SourceCLI, provenance)
	applyString(overrides.Workspace, &execution.Workspace, fieldWorkspace, SourceCLI, provenance)
	applyBool(overrides.Tools, &execution.Tools, fieldTools, SourceCLI, provenance)
	applyUint64(overrides.MaxOutputTokens, &execution.MaxOutputTokens, fieldMaxOutputTokens, SourceCLI, provenance)
	applyInt(overrides.MaxSteps, &execution.MaxSteps, fieldMaxSteps, SourceCLI, provenance)
	applyDuration(overrides.Timeout, &execution.Timeout, fieldTimeout, SourceCLI, provenance)
	applyDuration(overrides.IdleTimeout, &execution.IdleTimeout, fieldIdleTimeout, SourceCLI, provenance)
	applyInt(overrides.MaxConcurrent, &execution.MaxConcurrent, fieldMaxConcurrent, SourceCLI, provenance)
	applyFloat64(overrides.RateLimit, &execution.RateLimit, fieldRateLimit, SourceCLI, provenance)
	applyUint64(overrides.BudgetTokens, &execution.BudgetTokens, fieldBudgetTokens, SourceCLI, provenance)
	applyFloat64(overrides.BudgetUSD, &execution.BudgetUSD, fieldBudgetUSD, SourceCLI, provenance)
	applyString(overrides.ReasoningEffort, &execution.ReasoningEffort, fieldReasoning, SourceCLI, provenance)
	applyBool(overrides.NativeSearch, &execution.NativeSearch, fieldNativeSearch, SourceCLI, provenance)
	verify := &execution.Verify
	applyString(overrides.VerifyMode, &verify.Mode, fieldVerifyMode, SourceCLI, provenance)
	applyString(overrides.VerifyScope, &verify.Scope, fieldVerifyScope, SourceCLI, provenance)
	applyString(overrides.VerifyOnFailure, &verify.OnFailure, fieldVerifyOnFailure, SourceCLI, provenance)
	applyString(overrides.VerifyCommand, &verify.Command, fieldVerifyCommand, SourceCLI, provenance)
	applyInt(overrides.VerifyRepair, &verify.MaxRepairSteps, fieldVerifyRepair, SourceCLI, provenance)
	applyDuration(overrides.VerifyTimeout, &verify.Timeout, fieldVerifyTimeout, SourceCLI, provenance)
	child := &execution.Subagent
	applyString(overrides.SubagentDelegation, &child.Delegation, fieldSubagentDelegation, SourceCLI, provenance)
	applyInt(overrides.SubagentMaxDepth, &child.MaxDepth, fieldSubagentMaxDepth, SourceCLI, provenance)
	applyInt(overrides.SubagentMaxParallel, &child.MaxParallel, fieldSubagentMaxParallel, SourceCLI, provenance)
	applyInt(overrides.SubagentMaxResident, &child.MaxResident, fieldSubagentMaxResident, SourceCLI, provenance)
	applyInt(overrides.SubagentMaxTotal, &child.MaxTotal, fieldSubagentMaxTotal, SourceCLI, provenance)
	applyInt(overrides.SubagentMaxSteps, &child.MaxSteps, fieldSubagentMaxSteps, SourceCLI, provenance)
	applyUint64(overrides.SubagentMaxTokens, &child.MaxTokens, fieldSubagentMaxTokens, SourceCLI, provenance)
	applyFloat64(overrides.SubagentMaxCostUSD, &child.MaxCostUSD, fieldSubagentMaxCostUSD, SourceCLI, provenance)
	applyDuration(overrides.SubagentWallTime, &child.WallTime, fieldSubagentWallTime, SourceCLI, provenance)
	applyString(overrides.SubagentWorkspace, &child.Workspace, fieldSubagentWorkspace, SourceCLI, provenance)
	worker := &execution.Worker
	applyBool(overrides.WorkerEnabled, &worker.Enabled, fieldWorkerEnabled, SourceCLI, provenance)
	applyInt(overrides.WorkerMaxParallel, &worker.MaxParallel, fieldWorkerMaxParallel, SourceCLI, provenance)
	applyInt(overrides.WorkerMaxAttempts, &worker.MaxAttempts, fieldWorkerMaxAttempts, SourceCLI, provenance)
	applyDuration(overrides.WorkerLease, &worker.Lease, fieldWorkerLease, SourceCLI, provenance)
	applyDuration(
		overrides.WorkerClaimInterval, &worker.ClaimInterval, fieldWorkerClaimInterval, SourceCLI, provenance,
	)
	applyDuration(
		overrides.WorkerAutomationInterval, &worker.AutomationInterval,
		fieldWorkerAutomationTick, SourceCLI, provenance,
	)
	applyDuration(
		overrides.WorkerRetryBackoff, &worker.RetryBackoff, fieldWorkerRetryBackoff, SourceCLI, provenance,
	)
	applyDuration(
		overrides.WorkerRetryBackoffMax, &worker.RetryBackoffMax,
		fieldWorkerRetryBackoffMax, SourceCLI, provenance,
	)
	applyUint64(overrides.WorkerMaxTokens, &worker.MaxTokens, fieldWorkerMaxTokens, SourceCLI, provenance)
	applyFloat64(overrides.WorkerMaxCostUSD, &worker.MaxCostUSD, fieldWorkerMaxCostUSD, SourceCLI, provenance)
	applyBool(overrides.VisionEnabled, &config.Vision.Enabled, fieldVisionEnabled, SourceCLI, provenance)
	applyString(overrides.VisionProvider, &config.Vision.Provider, fieldVisionProvider, SourceCLI, provenance)
	applyString(overrides.VisionModel, &config.Vision.Model, fieldVisionModel, SourceCLI, provenance)
	applyString(overrides.WebSearchBackend, &config.Web.SearchBackend, fieldWebSearchBackend, SourceCLI, provenance)
	applyBool(overrides.RouteLock, &config.Route.Lock, fieldRouteLock, SourceCLI, provenance)
}
