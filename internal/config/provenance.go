package config

const (
	SourceDefault Source = "default"
	SourceFile    Source = "file"
	SourceRepo    Source = "repo"
	SourceEnv     Source = "env"
	SourceStartup Source = "startup"
)

const (
	fieldOperationBuffer      = "runtime.operation_buffer"
	fieldEventHistory         = "runtime.event_history"
	fieldSubscriberBuffer     = "runtime.subscriber_buffer"
	fieldStateDataDir         = "state.data_dir"
	fieldStateBusyTimeout     = "state.busy_timeout"
	fieldStateRetention       = "state.event_retention"
	fieldMemoryEnabled        = "memory.enabled"
	fieldMemoryPath           = "memory.path"
	fieldMemoryMaxCandidates  = "memory.max_candidates"
	fieldMemoryMaxPromptBytes = "memory.max_prompt_bytes"
	fieldMemorySemanticRerank = "memory.semantic_rerank"
	fieldIndexEnabled         = "context.index.enabled"
	fieldIndexMaxBytes        = "context.index.max_file_bytes"
	fieldIndexMaxFiles        = "context.index.max_files"

	fieldRepoMapEnabled        = "context.repo_map.enabled"
	fieldRepoMapMaxBytes       = "context.repo_map.max_bytes"
	fieldRepoMapMaxDirectories = "context.repo_map.max_directories"
	fieldWorkingSetEnabled     = "context.working_set.enabled"
	fieldWorkingSetMaxEntries  = "context.working_set.max_entries"
	fieldWorkingSetMaxBytes    = "context.working_set.max_bytes"
	fieldEvidenceEnabled       = "context.evidence.enabled"
	fieldEvidenceMaxEntries    = "context.evidence.max_entries"
	fieldEvidenceMaxBytes      = "context.evidence.max_bytes"
	fieldCodingPolicyEnabled   = "context.coding_policy.enabled"

	fieldCompactAutoTokens                       = "context.compact.auto_compact_tokens"
	fieldCompactPrepareTokens                    = "context.compact.prepare_tokens"
	fieldCompactEmergencyTokens                  = "context.compact.emergency_tokens"
	fieldCompactScope                            = "context.compact.scope"
	fieldCompactSummaryMax                       = "context.compact.summary_max_bytes"
	fieldCompactMaxDigest                        = "context.compact.max_digest_entries"
	fieldCompactTruthMaxBytes                    = "context.compact.truth_max_bytes"
	fieldCompactTruthMaxEntities                 = "context.compact.truth_max_entities"
	fieldCompactMandatoryMaxEntities             = "context.compact.mandatory_max_entities"
	fieldCompactFactMaxEntities                  = "context.compact.fact_max_entities"
	fieldCompactVerifiedChangeRetentionTurns     = "context.compact.verified_change_retention_turns"
	fieldCompactFailureMaxEntities               = "context.compact.failure_max_entities"
	fieldCompactHandleMaxEntities                = "context.compact.handle_max_entities"
	fieldCompactOmissionSampleMaxEntities        = "context.compact.omission_sample_max_entities"
	fieldCompactRecentTailTurns                  = "context.compact.recent_tail_turns"
	fieldCompactRecentTailMaxTokens              = "context.compact.recent_tail_max_tokens"
	fieldCompactSemanticNarrative                = "context.compact.semantic_narrative"
	fieldCompactSemanticNarrativeMaxInputTokens  = "context.compact.semantic_narrative_max_input_tokens"
	fieldCompactSemanticNarrativeMaxOutputTokens = "context.compact.semantic_narrative_max_output_tokens"
	fieldCompactSemanticNarrativeMaxItems        = "context.compact.semantic_narrative_max_items"
	fieldCompactSemanticNarrativeItemMaxBytes    = "context.compact.semantic_narrative_item_max_bytes"
	fieldCompactSemanticNarrativeTimeout         = "context.compact.semantic_narrative_timeout"
	fieldCompactSemanticNarrativeRetryLimit      = "context.compact.semantic_narrative_retry_limit"
	fieldCompactOwnerDeltaMaxSegments            = "context.compact.owner_delta_max_segments"
	fieldCompactOwnerDeltaMaxBytes               = "context.compact.owner_delta_max_bytes"

	fieldLogLevel              = "telemetry.log_level"
	fieldCredentialKind        = "credential.kind"
	fieldCredentialName        = "credential.name"
	fieldProvider              = "execution.provider"
	fieldModel                 = "execution.model"
	fieldProtocol              = "execution.protocol"
	fieldMode                  = "execution.mode"
	fieldWorkspace             = "execution.workspace"
	fieldTools                 = "execution.tools"
	fieldMaxOutputTokens       = "execution.max_output_tokens"
	fieldMaxSteps              = "execution.max_steps"
	fieldTimeout               = "execution.timeout"
	fieldConnectionTimeout     = "execution.connection_timeout"
	fieldTLSHandshakeTimeout   = "execution.tls_handshake_timeout"
	fieldResponseHeaderTimeout = "execution.response_header_timeout"
	fieldIdleTimeout           = "execution.idle_timeout"
	fieldMaxConcurrent         = "execution.max_concurrent"
	fieldRateLimit             = "execution.rate_limit"
	fieldBudgetTokens          = "execution.budget_tokens"
	fieldTurnBudgetTokens      = "execution.turn_budget_tokens"
	fieldBudgetUSD             = "execution.budget_usd"
	fieldReasoning             = "execution.reasoning_effort"
	fieldNativeSearch          = "execution.native_search"
	fieldVerifyMode            = "execution.verify.mode"
	fieldVerifyScope           = "execution.verify.scope"
	fieldVerifyOnFailure       = "execution.verify.on_failure"
	fieldVerifyCommand         = "execution.verify.command"
	fieldVerifyRepair          = "execution.verify.max_repair_steps"
	fieldVerifyTimeout         = "execution.verify.timeout"

	fieldSubagentDelegation  = "execution.subagent.delegation"
	fieldSubagentMaxDepth    = "execution.subagent.max_depth"
	fieldSubagentMaxParallel = "execution.subagent.max_parallel"
	fieldSubagentMaxResident = "execution.subagent.max_resident"
	fieldSubagentMaxTotal    = "execution.subagent.max_total"
	fieldSubagentMaxSteps    = "execution.subagent.max_steps"
	fieldSubagentMaxTokens   = "execution.subagent.max_tokens"
	fieldSubagentMaxCostUSD  = "execution.subagent.max_cost_usd"
	fieldSubagentWallTime    = "execution.subagent.wall_time"
	fieldSubagentWorkspace   = "execution.subagent.workspace"

	fieldWorkerEnabled         = "execution.worker.enabled"
	fieldWorkerMaxParallel     = "execution.worker.max_parallel"
	fieldWorkerMaxAttempts     = "execution.worker.max_attempts"
	fieldWorkerLease           = "execution.worker.lease"
	fieldWorkerClaimInterval   = "execution.worker.claim_interval"
	fieldWorkerAutomationTick  = "execution.worker.automation_interval"
	fieldWorkerRetryBackoff    = "execution.worker.retry_backoff"
	fieldWorkerRetryBackoffMax = "execution.worker.retry_backoff_max"
	fieldWorkerMaxTokens       = "execution.worker.max_tokens"
	fieldWorkerMaxCostUSD      = "execution.worker.max_cost_usd"

	fieldJournalDurable        = "execution.journal.durable"
	fieldJournalRecoverOnStart = "execution.journal.recover_on_start"

	fieldVisionEnabled    = "vision.enabled"
	fieldVisionProvider   = "vision.provider"
	fieldVisionModel      = "vision.model"
	fieldWebSearchBackend = "web.search_backend"

	fieldRouteLock = "route.lock"
)

// fieldRouteProvider and fieldRouteModel name a purpose's slot for provenance
// and for errors. The purpose is part of the field name because "route.provider"
// would report the wrong thing once a second slot is configured.
func fieldRouteProvider(purpose string) string { return "route." + purpose + ".provider" }

func fieldRouteModel(purpose string) string { return "route." + purpose + ".model" }

func fieldDiagnosticCommandName(extension string) string {
	return "diagnostics.commands." + extension + ".name"
}

func fieldDiagnosticCommandArgs(extension string) string {
	return "diagnostics.commands." + extension + ".args"
}

type Source string

type Snapshot struct {
	Config     Config            `json:"config"`
	Provenance map[string]Source `json:"provenance"`
}

func defaultProvenance() map[string]Source {
	return map[string]Source{
		fieldOperationBuffer:      SourceDefault,
		fieldEventHistory:         SourceDefault,
		fieldSubscriberBuffer:     SourceDefault,
		fieldStateDataDir:         SourceDefault,
		fieldStateBusyTimeout:     SourceDefault,
		fieldStateRetention:       SourceDefault,
		fieldMemoryEnabled:        SourceDefault,
		fieldMemoryPath:           SourceDefault,
		fieldMemoryMaxCandidates:  SourceDefault,
		fieldMemoryMaxPromptBytes: SourceDefault,
		fieldMemorySemanticRerank: SourceDefault,
		fieldIndexEnabled:         SourceDefault,
		fieldIndexMaxBytes:        SourceDefault,
		fieldIndexMaxFiles:        SourceDefault,

		fieldRepoMapEnabled:                          SourceDefault,
		fieldRepoMapMaxBytes:                         SourceDefault,
		fieldRepoMapMaxDirectories:                   SourceDefault,
		fieldWorkingSetEnabled:                       SourceDefault,
		fieldWorkingSetMaxEntries:                    SourceDefault,
		fieldWorkingSetMaxBytes:                      SourceDefault,
		fieldEvidenceEnabled:                         SourceDefault,
		fieldEvidenceMaxEntries:                      SourceDefault,
		fieldEvidenceMaxBytes:                        SourceDefault,
		fieldCodingPolicyEnabled:                     SourceDefault,
		fieldCompactAutoTokens:                       SourceDefault,
		fieldCompactPrepareTokens:                    SourceDefault,
		fieldCompactEmergencyTokens:                  SourceDefault,
		fieldCompactScope:                            SourceDefault,
		fieldCompactSummaryMax:                       SourceDefault,
		fieldCompactMaxDigest:                        SourceDefault,
		fieldCompactTruthMaxBytes:                    SourceDefault,
		fieldCompactTruthMaxEntities:                 SourceDefault,
		fieldCompactMandatoryMaxEntities:             SourceDefault,
		fieldCompactFactMaxEntities:                  SourceDefault,
		fieldCompactVerifiedChangeRetentionTurns:     SourceDefault,
		fieldCompactFailureMaxEntities:               SourceDefault,
		fieldCompactHandleMaxEntities:                SourceDefault,
		fieldCompactOmissionSampleMaxEntities:        SourceDefault,
		fieldCompactRecentTailTurns:                  SourceDefault,
		fieldCompactRecentTailMaxTokens:              SourceDefault,
		fieldCompactSemanticNarrative:                SourceDefault,
		fieldCompactSemanticNarrativeMaxInputTokens:  SourceDefault,
		fieldCompactSemanticNarrativeMaxOutputTokens: SourceDefault,
		fieldCompactSemanticNarrativeMaxItems:        SourceDefault,
		fieldCompactSemanticNarrativeItemMaxBytes:    SourceDefault,
		fieldCompactSemanticNarrativeTimeout:         SourceDefault,
		fieldCompactSemanticNarrativeRetryLimit:      SourceDefault,
		fieldCompactOwnerDeltaMaxSegments:            SourceDefault,
		fieldCompactOwnerDeltaMaxBytes:               SourceDefault,

		fieldLogLevel:              SourceDefault,
		fieldCredentialKind:        SourceDefault,
		fieldCredentialName:        SourceDefault,
		fieldProvider:              SourceDefault,
		fieldModel:                 SourceDefault,
		fieldProtocol:              SourceDefault,
		fieldMode:                  SourceDefault,
		fieldWorkspace:             SourceDefault,
		fieldTools:                 SourceDefault,
		fieldMaxOutputTokens:       SourceDefault,
		fieldMaxSteps:              SourceDefault,
		fieldTimeout:               SourceDefault,
		fieldConnectionTimeout:     SourceDefault,
		fieldTLSHandshakeTimeout:   SourceDefault,
		fieldResponseHeaderTimeout: SourceDefault,
		fieldIdleTimeout:           SourceDefault,
		fieldMaxConcurrent:         SourceDefault,
		fieldRateLimit:             SourceDefault,
		fieldBudgetTokens:          SourceDefault,
		fieldTurnBudgetTokens:      SourceDefault,
		fieldBudgetUSD:             SourceDefault,
		fieldReasoning:             SourceDefault,
		fieldNativeSearch:          SourceDefault,
		fieldVerifyMode:            SourceDefault,
		fieldVerifyScope:           SourceDefault,
		fieldVerifyOnFailure:       SourceDefault,
		fieldVerifyCommand:         SourceDefault,
		fieldVerifyRepair:          SourceDefault,
		fieldVerifyTimeout:         SourceDefault,

		fieldSubagentDelegation:  SourceDefault,
		fieldSubagentMaxDepth:    SourceDefault,
		fieldSubagentMaxParallel: SourceDefault,
		fieldSubagentMaxResident: SourceDefault,
		fieldSubagentMaxTotal:    SourceDefault,
		fieldSubagentMaxSteps:    SourceDefault,
		fieldSubagentMaxTokens:   SourceDefault,
		fieldSubagentMaxCostUSD:  SourceDefault,
		fieldSubagentWallTime:    SourceDefault,
		fieldSubagentWorkspace:   SourceDefault,

		fieldJournalDurable:        SourceDefault,
		fieldJournalRecoverOnStart: SourceDefault,

		fieldWorkerEnabled:         SourceDefault,
		fieldWorkerMaxParallel:     SourceDefault,
		fieldWorkerMaxAttempts:     SourceDefault,
		fieldWorkerLease:           SourceDefault,
		fieldWorkerClaimInterval:   SourceDefault,
		fieldWorkerAutomationTick:  SourceDefault,
		fieldWorkerRetryBackoff:    SourceDefault,
		fieldWorkerRetryBackoffMax: SourceDefault,
		fieldWorkerMaxTokens:       SourceDefault,
		fieldWorkerMaxCostUSD:      SourceDefault,

		fieldVisionEnabled:    SourceDefault,
		fieldVisionProvider:   SourceDefault,
		fieldVisionModel:      SourceDefault,
		fieldWebSearchBackend: SourceDefault,
	}
}
