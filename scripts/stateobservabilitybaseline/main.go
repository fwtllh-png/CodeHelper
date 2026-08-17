// Command stateobservabilitybaseline records and checks the SO0 state and
// observability surface without changing production runtime behavior.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/kernel"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/model"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state/eventlog"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const (
	schemaVersion = 1
	stageSO0      = "SO0"
	codexCommit   = "3bbf1fe75701c97fb190e0867002ba2d9dbda5db"
)

type report struct {
	SchemaVersion  int                  `json:"schema_version"`
	Stage          string               `json:"stage"`
	Status         string               `json:"status"`
	BaseCommit     string               `json:"base_commit"`
	Reference      reference            `json:"reference"`
	Authorities    []authority          `json:"authorities"`
	Events         eventAudit           `json:"events"`
	Contracts      contracts            `json:"contracts"`
	IdentityLayers []string             `json:"identity_layers"`
	Surfaces       []surface            `json:"observation_surfaces"`
	Storage        storageMetrics       `json:"representative_storage_bytes"`
	Performance    []performanceMetric  `json:"performance_baseline"`
	SessionMatrix  []scenario           `json:"session_matrix"`
	FailureMatrix  []failureCase        `json:"failure_matrix"`
	Reconstruction reconstructionMetric `json:"manual_reconstruction"`
	KnownGaps      []string             `json:"known_gaps"`
	Commands       map[string]string    `json:"commands"`
}

type reference struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
}

type authority struct {
	Name            string `json:"name"`
	Owner           string `json:"owner"`
	CanonicalStore  string `json:"canonical_store"`
	QueryProjection string `json:"query_projection,omitempty"`
}

type eventAudit struct {
	TotalKinds        int            `json:"total_kinds"`
	TraitsCovered     int            `json:"traits_covered"`
	Declared          map[string]int `json:"declared_durability"`
	ActuallyPersisted int            `json:"actually_persisted"`
	ActuallyDropped   int            `json:"actually_dropped"`
	Mismatches        []policyDrift  `json:"policy_mismatches"`
}

type policyDrift struct {
	Kind            string `json:"kind"`
	Declared        string `json:"declared"`
	ActualPersisted bool   `json:"actual_persisted"`
	Expected        bool   `json:"expected_persisted"`
}

// Every field is monotonic: true may not regress to false.
type contracts struct {
	TurnKernelDigestAuthority      bool `json:"turn_kernel_digest_authority"`
	AtomicTerminalEnvelope         bool `json:"atomic_terminal_envelope"`
	EventReservationReconciliation bool `json:"event_reservation_reconciliation"`
	UsageIdempotentProjection      bool `json:"usage_idempotent_projection"`
	ContextLedgerAuthority         bool `json:"context_ledger_authority"`
	WorkGraphFactReplay            bool `json:"workgraph_fact_replay"`
	ExtensionDiagnosticsBounded    bool `json:"extension_diagnostics_bounded"`
	PermissionDigestInReceipt      bool `json:"permission_digest_in_receipt"`
	EventPolicySingleSource        bool `json:"event_policy_single_source"`
	IncrementalTraceJournal        bool `json:"incremental_trace_journal"`
	TraceAttemptsPreserved         bool `json:"trace_attempts_preserved"`
	UnifiedObservationIdentity     bool `json:"unified_observation_identity"`
	ExplicitModelVisibility        bool `json:"explicit_model_visibility"`
	W3CTracePropagation            bool `json:"w3c_trace_propagation"`
	OpenTelemetryExport            bool `json:"opentelemetry_export"`
	TerminalMeasurementSnapshot    bool `json:"terminal_measurement_snapshot"`
	RawPayloadReferencePlane       bool `json:"raw_payload_reference_plane"`
	ObservationHealthModel         bool `json:"observation_health_model"`
}

type surface struct {
	Name               string `json:"name"`
	Owner              string `json:"owner"`
	Durability         string `json:"durability"`
	Capacity           string `json:"capacity,omitempty"`
	AttemptPreserving  bool   `json:"attempt_preserving"`
	CrossProcessTrace  bool   `json:"cross_process_trace"`
	ModelVisibility    bool   `json:"model_visibility"`
	CanonicalForAction bool   `json:"canonical_for_action"`
}

type storageMetrics struct {
	TurnState          int `json:"turn_state"`
	DomainFact         int `json:"domain_fact"`
	TraceSpan          int `json:"trace_span"`
	ToolAttemptReceipt int `json:"tool_attempt_receipt"`
	WorkGraphFact      int `json:"workgraph_fact"`
	ExtensionTrace     int `json:"extension_trace"`
}

type performanceMetric struct {
	Name         string  `json:"name"`
	Runs         int     `json:"runs"`
	MedianNS     int64   `json:"median_ns"`
	P95NS        int64   `json:"p95_ns"`
	AllocsPerRun float64 `json:"allocs_per_run"`
}

type scenario struct {
	ID          string   `json:"id"`
	Package     string   `json:"package"`
	File        string   `json:"file"`
	Test        string   `json:"test"`
	Authorities []string `json:"authorities"`
	Measured    bool     `json:"measured"`
	Hermetic    bool     `json:"hermetic"`
}

type failureCase struct {
	ID                 string `json:"id"`
	Component          string `json:"component"`
	Injection          string `json:"injection"`
	Package            string `json:"package"`
	File               string `json:"file"`
	Test               string `json:"test"`
	AuthorityOutcome   string `json:"authority_outcome"`
	ObservationOutcome string `json:"observation_outcome"`
	TargetStage        string `json:"target_stage"`
	Measured           bool   `json:"measured"`
}

type reconstructionMetric struct {
	Scenario           string   `json:"scenario"`
	RequiredSources    []string `json:"required_sources"`
	MinimumManualJoins int      `json:"minimum_manual_joins"`
	AutomatedToday     bool     `json:"automated_today"`
	TargetStage        string   `json:"target_stage"`
}

var gapNames = map[string]string{
	"EventPolicySingleSource":     "event_durability_has_two_policy_sources",
	"IncrementalTraceJournal":     "turn_trace_is_written_only_after_turn_end",
	"TraceAttemptsPreserved":      "recovered_turn_trace_replaces_prior_attempt",
	"UnifiedObservationIdentity":  "observation_identity_is_fragmented",
	"ExplicitModelVisibility":     "runtime_evidence_is_not_linked_to_model_visibility",
	"W3CTracePropagation":         "trace_context_stops_at_process_boundaries",
	"OpenTelemetryExport":         "metrics_and_traces_have_no_standard_export",
	"TerminalMeasurementSnapshot": "receipt_and_trace_freeze_at_different_times",
	"RawPayloadReferencePlane":    "diagnostic_payloads_have_no_common_reference_plane",
	"ObservationHealthModel":      "observation_failures_share_generic_error_counters",
}

func main() {
	root := flag.String("root", ".", "repository root")
	baselinePath := flag.String(
		"baseline",
		"docs/state-observability-so0-baseline.json",
		"SO0 baseline JSON",
	)
	reportPath := flag.String("report", "", "optional measured report path")
	writeBaseline := flag.Bool("write-baseline", false, "replace baseline with current metrics")
	baseCommit := flag.String("base-commit", "", "baseline source commit")
	flag.Parse()

	measured, err := measure(*root, *baseCommit)
	if err == nil && *writeBaseline {
		err = writeJSON(filepath.Join(*root, *baselinePath), measured)
	} else if err == nil {
		var baseline report
		baseline, err = readReport(filepath.Join(*root, *baselinePath))
		if err == nil {
			err = validateCandidate(baseline, measured)
		}
	}
	if reportErr := writeOptionalReport(*root, *reportPath, measured); err == nil {
		err = reportErr
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf(
		"state observability baseline passed: events=%d drifts=%d scenarios=%d failures=%d gaps=%d\n",
		measured.Events.TotalKinds,
		len(measured.Events.Mismatches),
		len(measured.SessionMatrix),
		len(measured.FailureMatrix),
		len(measured.KnownGaps),
	)
}

func measure(root, baseCommit string) (report, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return report{}, err
	}
	if baseCommit == "" {
		baseCommit = gitCommit(absolute)
	}
	events, err := measureEvents()
	if err != nil {
		return report{}, err
	}
	contractValues, err := measureContracts(absolute, events)
	if err != nil {
		return report{}, err
	}
	sessions := standardSessions()
	failures := failureCases()
	if evidenceErr := resolveEvidence(absolute, sessions, failures); evidenceErr != nil {
		return report{}, evidenceErr
	}
	storage, state, err := measureStorage()
	if err != nil {
		return report{}, err
	}
	return report{
		SchemaVersion: schemaVersion,
		Stage:         stageSO0,
		Status:        "baseline_frozen",
		BaseCommit:    baseCommit,
		Reference: reference{
			Repository: "../codex",
			Commit:     codexCommit,
		},
		Authorities: authorityInventory(),
		Events:      events,
		Contracts:   contractValues,
		IdentityLayers: []string{
			"runtime_id", "session_id", "thread_id", "turn_id", "operation_id",
			"event_id", "event_cursor", "domain_fact_sequence", "run_id", "node_id",
			"attempt_id", "effect_id", "call_id", "sample_id", "extension_operation_id",
		},
		Surfaces:      observationSurfaces(),
		Storage:       storage,
		Performance:   measurePerformance(state),
		SessionMatrix: sessions,
		FailureMatrix: failures,
		Reconstruction: reconstructionMetric{
			Scenario: "provider retry followed by guarded tool failure and recovery",
			RequiredSources: []string{
				"turn_domain_facts", "terminal_envelope", "events-v1.jsonl",
				"spans", "usage", "tool_execution_receipt", "workspace_journal",
			},
			MinimumManualJoins: 6,
			AutomatedToday:     false,
			TargetStage:        "SO3",
		},
		KnownGaps: knownGaps(contractValues),
		Commands: map[string]string{
			"baseline": "make state-observability-so0",
			"update":   "make state-observability-so0-update",
		},
	}, nil
}

func measureEvents() (eventAudit, error) {
	result := eventAudit{Declared: make(map[string]int)}
	for _, kind := range protocol.EventKinds() {
		result.TotalKinds++
		traits, ok := protocol.Traits(kind)
		if !ok {
			return eventAudit{}, fmt.Errorf("event %q has no traits", kind)
		}
		result.TraitsCovered++
		result.Declared[string(traits.Durability)]++
		actual := eventlog.ShouldPersist(kind)
		if actual {
			result.ActuallyPersisted++
		} else {
			result.ActuallyDropped++
		}
		expected := expectedPersistence(traits.Durability)
		if actual != expected {
			result.Mismatches = append(result.Mismatches, policyDrift{
				Kind: string(kind), Declared: string(traits.Durability),
				ActualPersisted: actual, Expected: expected,
			})
		}
	}
	sort.Slice(result.Mismatches, func(i, j int) bool {
		return result.Mismatches[i].Kind < result.Mismatches[j].Kind
	})
	return result, nil
}

func expectedPersistence(value protocol.Durability) bool {
	switch value {
	case "transient", "terminal_projection", "bounded":
		return false
	default:
		return true
	}
}

func measureContracts(root string, events eventAudit) (contracts, error) {
	var checks []sourceRequirement
	value := contracts{EventPolicySingleSource: len(events.Mismatches) == 0}
	checks = append(checks,
		sourceCheck(root, "internal/runtime/agent/turnkernel/coordinator.go",
			[]string{"AppendDomainFacts", "Digest(transition.State)", "dispatcher.Dispatch"}, &value.TurnKernelDigestAuthority),
		sourceCheck(root, "internal/runtime/agent/turnkernel/terminal_envelope.go",
			[]string{"type TerminalEnvelope struct", "CommitTerminal"}, &value.AtomicTerminalEnvelope),
		sourceCheck(root, "internal/persist/state/store.go",
			[]string{"reconcile", "Reservation"}, &value.EventReservationReconciliation),
		sourceCheck(root, "internal/observability/usage/repository.go",
			[]string{"ON CONFLICT(turn_id, sample)", "verifyReplay", "source_sequence"}, &value.UsageIdempotentProjection),
		sourceCheck(root, "internal/runtime/agent/contextstore/store.go",
			[]string{"sole owner of model-sample assembly", "type Ledger struct", "func (l *Ledger) Project"}, &value.ContextLedgerAuthority),
		sourceCheck(root, "internal/orchestration/store/store.go",
			[]string{"func replayGraph(", "ErrSnapshotDrift", "persistTransition"}, &value.WorkGraphFactReplay),
		sourceCheck(root, "internal/runtime/app/extension/observability.go",
			[]string{"maxExtensionTraces = 256", "subscriber_overflow"}, &value.ExtensionDiagnosticsBounded),
		sourceCheck(root, "internal/runtime/protocol/tool_execution_receipt.go",
			[]string{"PermissionDigest", "ToolAttemptReceipt"}, &value.PermissionDigestInReceipt),
	)
	for _, check := range checks {
		matched, err := sourceContainsAll(check.path, check.needles)
		if err != nil {
			return contracts{}, err
		}
		*check.target = matched
	}
	value.IncrementalTraceJournal = sourceExists(
		root, "internal/observability/journal/journal.go",
	)
	repository, err := os.ReadFile(filepath.Join(root, "internal/observability/trace/repository.go"))
	if err != nil {
		return contracts{}, err
	}
	value.TraceAttemptsPreserved = !strings.Contains(string(repository), "DELETE FROM spans WHERE turn_id")
	value.UnifiedObservationIdentity = sourceExists(
		root, "internal/observability/observation/envelope.go",
	)
	value.ExplicitModelVisibility = productionTreeContains(
		root, "internal", "type VisibilityEdge struct",
	)
	value.W3CTracePropagation = productionTreeContains(root, "internal", "traceparent")
	value.OpenTelemetryExport = productionTreeContains(root, "internal", "go.opentelemetry.io/")
	value.TerminalMeasurementSnapshot = productionTreeContains(
		root, "internal", "type TerminalMeasurementSnapshot struct",
	)
	value.RawPayloadReferencePlane = productionTreeContains(
		root, "internal/observability", "type PayloadRef struct",
	)
	value.ObservationHealthModel = productionTreeContains(
		root, "internal/observability", "type HealthSnapshot struct",
	)
	return value, nil
}

type sourceRequirement struct {
	path    string
	needles []string
	target  *bool
}

func sourceCheck(root, path string, needles []string, target *bool) sourceRequirement {
	return sourceRequirement{
		path: filepath.Join(root, filepath.FromSlash(path)), needles: needles, target: target,
	}
}

func authorityInventory() []authority {
	return []authority{
		{"turn_kernel", "internal/runtime/agent/turnkernel", "turn_domain_facts + turn_terminal_envelopes", "runtime events + receipt"},
		{"protocol_event", "internal/runtime/app/eventhub", "events-v1.jsonl", "event_index + host projections"},
		{"workspace_mutation", "internal/persist/workspacejournal", "workspace journal", "receipt workspace outcome"},
		{"model_visible_context", "internal/runtime/agent/contextstore", "ContextLedger snapshot + committed session delta", "provider request projection"},
		{"work_graph", "internal/orchestration/kernel", "work_facts + work_graphs + effect outbox", "run/node/attempt projections"},
		{"security_attempt", "internal/adapter/tool/guard", "tool execution receipt", "turn receipt permission digests"},
		{"extension_control", "internal/runtime/app/extension", "extension control/lifecycle stores", "bounded diagnostics snapshot"},
	}
}

func observationSurfaces() []surface {
	return []surface{
		{"domain_facts", "turn_kernel", "durable", "", true, false, false, true},
		{"protocol_events", "event_hub", "mixed", "", true, false, false, true},
		{"turn_trace", "engine", "incremental_journal", "one tree per turn", false, true, false, false},
		{"usage_projection", "usage projector", "durable", "one row per sample/call", true, false, false, false},
		{"context_ledger", "runtime agent", "turn snapshot + terminal session delta", "one revision per model projection", true, false, true, true},
		{"runtime_metrics", "telemetry", "memory", "process lifetime", false, false, false, false},
		{"otel_projection", "observation projector", "optional_remote", "bounded queue", true, true, false, false},
		{"extension_diagnostics", "extension runtime", "memory", "256 traces", false, false, false, false},
		{"workgraph_facts", "orchestration kernel", "durable", "", true, false, false, true},
		{"runtime_capture", "vscode host", "optional file", "configured session", true, false, false, false},
	}
}

func measureStorage() (storageMetrics, turnkernel.State, error) {
	state := representativeState()
	fact := turnkernel.DomainFact{
		TurnID: "turn-so0", Sequence: 7, Command: "ToolFinished",
		State: state, StateDigest: strings.Repeat("a", 64),
	}
	span := trace.Record{
		ID: 3, ParentID: 1, Name: trace.NameTool,
		Started: time.Unix(1, 0).UTC(), Ended: time.Unix(2, 0).UTC(),
		Status:     trace.StatusError,
		Attributes: map[string]any{"call_id": "call-1", "tool": "exec_command"},
	}
	attempt := protocol.ToolAttemptReceipt{
		Sequence: 1, Sandbox: "strong", Status: "failed",
		TerminalOwner: "executor", PermissionSchemaVersion: 1,
		PermissionRevision: 9, PermissionDigest: strings.Repeat("b", 64),
		PermissionCapability: "process", PermissionAccess: "write",
		Enforcement: "enforced", Backend: "seatbelt",
		StartedAt: time.Unix(1, 0).UTC(), CompletedAt: time.Unix(2, 0).UTC(),
		DurationMS: 1000,
	}
	run := model.Run{
		ID: "run-so0", Kind: model.RunKindAgentTask, Source: "baseline",
		SessionID: "session-so0", RootThreadID: "thread-so0",
		State: protocol.RunStateActive, Revision: 2,
		CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC(),
	}
	workFact := kernel.Fact{
		Sequence: 2, Revision: 2, Kind: kernel.FactRunStatus,
		At: time.Unix(2, 0).UTC(), Run: &run,
	}
	extensionTrace := protocol.ExtensionControlTrace{
		OperationID: "extop-so0", Action: protocol.ExtensionActionEnable,
		Kind: protocol.ExtensionControlPlugin, Status: "committed",
		DurationMS: 12, OccurredAt: time.Unix(2, 0).UTC(),
	}
	values := []any{state, fact, span, attempt, workFact, extensionTrace}
	sizes := make([]int, len(values))
	for index, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			return storageMetrics{}, turnkernel.State{}, err
		}
		sizes[index] = len(encoded)
	}
	return storageMetrics{
		TurnState: sizes[0], DomainFact: sizes[1], TraceSpan: sizes[2],
		ToolAttemptReceipt: sizes[3], WorkGraphFact: sizes[4],
		ExtensionTrace: sizes[5],
	}, state, nil
}

func representativeState() turnkernel.State {
	state := turnkernel.NewState(protocol.TurnIntentWorkspaceChange, "act", 7)
	state.Phase = turnkernel.PhaseExecutingTools
	state.MutationRevision = 2
	state.OpenCalls["call-1"] = turnkernel.ToolCallState{
		ID: "call-1", Name: "exec_command", Arguments: `{"cmd":"go test ./..."}`,
		CatalogID: "catalog-so0", CatalogGeneration: 3, CatalogRevision: 8,
	}
	state.ClosedCalls["call-0"] = turnkernel.ToolResultState{
		ID: "call-0", Name: "file_read",
	}
	state.Changes = []turnkernel.ObservedChange{{Path: "internal/example.go", Kind: "modified"}}
	state.SampleLedger["sample-1"] = turnkernel.ModelSampleState{
		ID: "sample-1", Attempt: 2, Status: turnkernel.SampleCompleted,
		ProviderRetries: 1,
	}
	state.Usage = turnkernel.UsageState{
		InputTokens: 1200, OutputTokens: 300, CachedTokens: 900,
		CostMicrounits: 42, CostKnown: true,
	}
	state.Context = turnkernel.ContextState{
		Digest: strings.Repeat("c", 64), HistoryBytes: 8192,
		MaxBytes: 32768, Compactions: 1,
	}
	return state
}

func measurePerformance(state turnkernel.State) []performanceMetric {
	kinds := protocol.EventKinds()
	return []performanceMetric{
		measureOperation("event_trait_and_policy_audit", func() {
			for _, kind := range kinds {
				_, _ = protocol.Traits(kind)
				_ = eventlog.ShouldPersist(kind)
			}
		}),
		measureOperation("representative_turn_state_json", func() {
			_, _ = json.Marshal(state)
		}),
		measureOperation("trace_snapshot_1000_spans", func() {
			recorder := trace.NewRecorder(nil)
			root := recorder.Start(trace.NameTurn, 0, nil)
			for index := 0; index < 1000; index++ {
				span := recorder.Start(trace.NameTool, root.ID(), nil)
				span.End(trace.StatusOK)
			}
			_ = recorder.Spans()
		}),
	}
}

func measureOperation(name string, operation func()) performanceMetric {
	const runs = 9
	durations := make([]int64, 0, runs)
	for index := 0; index < runs; index++ {
		started := time.Now()
		operation()
		durations = append(durations, time.Since(started).Nanoseconds())
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	return performanceMetric{
		Name: name, Runs: runs, MedianNS: durations[runs/2],
		P95NS:        durations[runs-1],
		AllocsPerRun: testing.AllocsPerRun(5, operation),
	}
}

func standardSessions() []scenario {
	return []scenario{
		{"SO-SESSION-001", "./internal/runtime/app", "internal/runtime/app/application_e2e_test.go", "TestWorkspaceChangeReceiptMatchesTerminalOutcome", []string{"turn_kernel", "terminal_envelope", "workspace_journal"}, false, true},
		{"SO-SESSION-002", "./internal/runtime/agent/engine", "internal/runtime/agent/engine/engine_test.go", "TestEngineGuaranteesOneRetryForStructuredTransportFailure", []string{"turn_kernel", "provider_retry", "usage"}, false, true},
		{"SO-SESSION-003", "./internal/runtime/app", "internal/runtime/app/application_e2e_test.go", "TestRuntimeApprovalPauseResumeE2E", []string{"turn_kernel", "approval", "tool_receipt"}, false, true},
		{"SO-SESSION-004", "./internal/runtime/agent/engine", "internal/runtime/agent/engine/engine_test.go", "TestEngineBudgetAndFailedHistoryRollback", []string{"turn_kernel", "context_ledger", "usage"}, false, true},
		{"SO-SESSION-005", "./internal/runtime/app", "internal/runtime/app/runtime_terminal_recovery_test.go", "TestC5RuntimeRecoversTerminalOutboxWithoutDuplicateEvent", []string{"terminal_envelope", "event_log"}, false, true},
		{"SO-SESSION-006", "./internal/orchestration/store", "internal/orchestration/store/store_test.go", "TestAuditDetectsAndRepairsSnapshotDriftFromFacts", []string{"work_graph"}, false, true},
		{"SO-SESSION-007", "./internal/runtime/app", "internal/runtime/app/orchestration_test.go", "TestRuntimeWorkGraphEffectPublishIsStableAcrossAckFailure", []string{"work_graph", "event_log"}, false, true},
		{"SO-SESSION-008", "./internal/runtime/app/extension", "internal/runtime/app/extension/lifecycle_test.go", "TestLifecycleActivationFailureRollsBackEveryPriorEffect", []string{"extension_control"}, false, true},
		{"SO-SESSION-009", "./internal/observability/usage", "internal/observability/usage/repository_test.go", "TestUsageProjectionIsTransactionalAndIdempotent", []string{"usage"}, false, true},
		{"SO-SESSION-010", "./internal/adapter/tool/guard", "internal/adapter/tool/guard/pipeline_test.go", "TestRejectedAdditionalPermissionRetainsAuthorityEvidence", []string{"security_attempt", "tool_receipt"}, false, true},
	}
}

func failureCases() []failureCase {
	return []failureCase{
		{"SO-FAIL-001", "domain_fact", "fact store rejects transition", "./internal/runtime/agent/turnkernel", "internal/runtime/agent/turnkernel/coordinator_test.go", "TestPhase4R3PersistenceFailureCommitsNoStateOrEffect", "no state or effect committed", "typed test evidence; no incremental trace", "SO2", false},
		{"SO-FAIL-002", "terminal_envelope", "terminal stage failure", "./internal/runtime/agent/turnkernel", "internal/runtime/agent/turnkernel/terminal_envelope_test.go", "TestPhase4R2TerminalEnvelopeFailureLeaksNoPartialFacts", "no partial terminal facts", "typed fault stage; no common observation identity", "SO2", false},
		{"SO-FAIL-003", "event_log", "torn JSONL tail", "./internal/persist/state/eventlog", "internal/persist/state/eventlog/fault_injection_test.go", "TestFaultInjectionTornJSONLFailsClosed", "corrupt event rejected", "event log evidence retained", "SO2", false},
		{"SO-FAIL-004", "event_projection", "projection without durable event", "./internal/persist/state", "internal/persist/state/store_test.go", "TestStoreRejectsCommittedProjectionWithoutDurableEvent", "projection rejected", "reservation evidence retained", "SO2", false},
		{"SO-FAIL-005", "provider", "retryable transport failure", "./internal/runtime/agent/engine", "internal/runtime/agent/engine/engine_test.go", "TestEngineGuaranteesOneRetryForStructuredTransportFailure", "one provider retry", "turn-end trace only", "SO2", false},
		{"SO-FAIL-006", "tool", "tool panic", "./internal/runtime/agent/engine", "internal/runtime/agent/engine/engine_test.go", "TestEngineContainsToolPanicAsFailedTurn", "failed terminal", "turn-end trace only", "SO2", false},
		{"SO-FAIL-007", "approval", "approval expires", "./internal/runtime/agent/engine", "internal/runtime/agent/engine/approval_expiry_test.go", "TestApprovalExpiryResolvesKernelBeforeToolResult", "kernel resolves before tool result", "approval wait not crash durable", "SO2", false},
		{"SO-FAIL-008", "process", "strong sandbox unavailable", "./internal/platform/process", "internal/platform/process/process_test.go", "TestRunFailsClosedWithoutStrongSandbox", "process denied", "ordinary user process receives no internal trace context", "SO5", false},
		{"SO-FAIL-009", "usage", "duplicate cumulative usage", "./internal/observability/usage", "internal/observability/usage/repository_test.go", "TestUsageReplacesCumulativeReportsWithinACall", "usage remains idempotent", "query projection retained", "SO3", false},
		{"SO-FAIL-010", "work_graph", "snapshot differs from fact replay", "./internal/orchestration/store", "internal/orchestration/store/store_test.go", "TestAuditDetectsAndRepairsSnapshotDriftFromFacts", "drift detected and repaired", "separate orchestration authority", "SO3", false},
		{"SO-FAIL-011", "extension", "activation effect fails", "./internal/runtime/app/extension", "internal/runtime/app/extension/lifecycle_test.go", "TestLifecycleActivationFailureRollsBackEveryPriorEffect", "prior effects rolled back", "bounded in-memory extension trace", "SO3", false},
		{"SO-FAIL-012", "terminal_publication", "terminal append fails", "./internal/runtime/app", "internal/runtime/app/runtime_durable_fault_test.go", "TestPhase4RBaselineTerminalAppendFailureLeavesDurableReceipt", "durable receipt remains recoverable", "trace write occurs only at turn end", "SO4", false},
		{"SO-FAIL-013", "security_attempt", "additional permission rejected", "./internal/adapter/tool/guard", "internal/adapter/tool/guard/pipeline_test.go", "TestRejectedAdditionalPermissionRetainsAuthorityEvidence", "permission evidence retained", "trace context remains observation-only and cannot change the denial", "SO5", false},
	}
}

func resolveEvidence(root string, sessions []scenario, failures []failureCase) error {
	for index := range sessions {
		found, err := testExists(root, sessions[index].File, sessions[index].Test)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("session evidence test %s is missing", sessions[index].Test)
		}
		sessions[index].Measured = true
	}
	for index := range failures {
		found, err := testExists(root, failures[index].File, failures[index].Test)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("failure evidence test %s is missing", failures[index].Test)
		}
		failures[index].Measured = true
	}
	return nil
}

func testExists(root, path, name string) (bool, error) {
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return false, err
	}
	return strings.Contains(string(content), "func "+name+"("), nil
}

func knownGaps(value contracts) []string {
	reflected := reflect.ValueOf(value)
	typ := reflected.Type()
	var result []string
	for index := 0; index < reflected.NumField(); index++ {
		if reflected.Field(index).Bool() {
			continue
		}
		if gap, ok := gapNames[typ.Field(index).Name]; ok {
			result = append(result, gap)
		}
	}
	sort.Strings(result)
	return result
}

func validateCandidate(baseline, candidate report) error {
	if baseline.SchemaVersion != schemaVersion || baseline.Stage != stageSO0 {
		return errors.New("state observability baseline has an unsupported schema or stage")
	}
	if candidate.SchemaVersion != schemaVersion || candidate.Stage != stageSO0 {
		return errors.New("state observability candidate has an unsupported schema or stage")
	}
	baselineContracts := reflect.ValueOf(baseline.Contracts)
	candidateContracts := reflect.ValueOf(candidate.Contracts)
	typ := baselineContracts.Type()
	for index := 0; index < baselineContracts.NumField(); index++ {
		if baselineContracts.Field(index).Bool() && !candidateContracts.Field(index).Bool() {
			return fmt.Errorf("state observability contract %s regressed", typ.Field(index).Name)
		}
	}
	if candidate.Events.TotalKinds < baseline.Events.TotalKinds ||
		candidate.Events.TraitsCovered != candidate.Events.TotalKinds {
		return errors.New("event kind or trait coverage regressed")
	}
	baselineDrifts := make(map[string]bool, len(baseline.Events.Mismatches))
	for _, drift := range baseline.Events.Mismatches {
		baselineDrifts[drift.Kind] = true
	}
	for _, drift := range candidate.Events.Mismatches {
		if !baselineDrifts[drift.Kind] {
			return fmt.Errorf("new event durability drift %q", drift.Kind)
		}
	}
	if len(candidate.Events.Mismatches) > len(baseline.Events.Mismatches) {
		return errors.New("event durability drift count increased")
	}
	if err := validateScenarioSet(baseline.SessionMatrix, candidate.SessionMatrix); err != nil {
		return err
	}
	if err := validateFailureSet(baseline.FailureMatrix, candidate.FailureMatrix); err != nil {
		return err
	}
	if candidate.Storage.TurnState == 0 || candidate.Storage.DomainFact == 0 ||
		candidate.Storage.TraceSpan == 0 {
		return errors.New("representative storage metrics are incomplete")
	}
	return nil
}

func validateScenarioSet(baseline, candidate []scenario) error {
	if len(candidate) != len(baseline) {
		return errors.New("session matrix size changed")
	}
	for index := range baseline {
		if baseline[index].ID != candidate[index].ID || !candidate[index].Measured ||
			!candidate[index].Hermetic {
			return fmt.Errorf("session matrix case %d regressed", index)
		}
	}
	return nil
}

func validateFailureSet(baseline, candidate []failureCase) error {
	if len(candidate) != len(baseline) || len(candidate) < 10 {
		return errors.New("failure matrix size changed or is below SO0 gate")
	}
	for index := range baseline {
		if baseline[index].ID != candidate[index].ID || !candidate[index].Measured {
			return fmt.Errorf("failure matrix case %d regressed", index)
		}
	}
	return nil
}

func sourceContainsAll(path string, needles []string) (bool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	text := string(content)
	for _, needle := range needles {
		if !strings.Contains(text, needle) {
			return false, nil
		}
	}
	return true, nil
}

func sourceExists(root, path string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
	return err == nil
}

func productionTreeContains(root, subtree, needle string) bool {
	found := false
	_ = filepath.WalkDir(
		filepath.Join(root, filepath.FromSlash(subtree)),
		func(path string, entry os.DirEntry, err error) error {
			if err != nil || found {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") ||
				strings.HasSuffix(path, "_test.go") {
				return nil
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			found = strings.Contains(string(content), needle)
			return nil
		},
	)
	return found
}

func gitCommit(root string) string {
	output, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(output))
}

func readReport(path string) (report, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return report{}, err
	}
	var value report
	if err := json.Unmarshal(content, &value); err != nil {
		return report{}, err
	}
	return value, nil
}

func writeOptionalReport(root, path string, value report) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	return writeJSON(filepath.Join(root, path), value)
}

func writeJSON(path string, value report) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, content, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
