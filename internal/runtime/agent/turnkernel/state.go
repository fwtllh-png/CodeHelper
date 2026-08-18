// Package turnkernel defines the deterministic Turn state machine.
//
// The package deliberately contains no provider, tool, persistence, clock, or
// host dependencies. It can therefore run in Coordinator and replay modes before it
// becomes authoritative for the Engine business loop.
package turnkernel

import (
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerassembly "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/assembly"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type Phase string

const (
	PhaseCreated          Phase = "created"
	PhasePreparing        Phase = "preparing"
	PhaseSampling         Phase = "sampling"
	PhaseExecutingTools   Phase = "executing_tools"
	PhaseAwaitingApproval Phase = "awaiting_approval"
	PhaseAwaitingInput    Phase = "awaiting_input"
	PhaseVerifying        Phase = "verifying"
	PhaseCommitting       Phase = "committing"
	PhaseCompleted        Phase = "completed"
	PhaseFailed           Phase = "failed"
	PhaseCanceled         Phase = "canceled"
)

func (p Phase) Terminal() bool {
	return p == PhaseCompleted || p == PhaseFailed || p == PhaseCanceled
}

type VerificationStatus string

const (
	VerificationNotEvaluated VerificationStatus = "not_evaluated"
	VerificationPassed       VerificationStatus = "passed"
	VerificationFailed       VerificationStatus = "failed"
	VerificationUnavailable  VerificationStatus = "unavailable"
)

type JournalStatus string

const (
	JournalNone       JournalStatus = "none"
	JournalOpen       JournalStatus = "open"
	JournalSuspended  JournalStatus = "suspended"
	JournalCommitted  JournalStatus = "committed"
	JournalRolledBack JournalStatus = "rolled_back"
)

type SampleStatus string

const (
	SampleRequested SampleStatus = "requested"
	SampleRunning   SampleStatus = "running"
	SampleCompleted SampleStatus = "completed"
	SampleFailed    SampleStatus = "failed"
)

type EffectLifecycleStatus string

const (
	EffectRequested EffectLifecycleStatus = "requested"
	EffectRunning   EffectLifecycleStatus = "running"
	EffectSucceeded EffectLifecycleStatus = "succeeded"
	EffectFailed    EffectLifecycleStatus = "failed"
)

type TerminalKind string

const (
	TerminalCompleted TerminalKind = "completed"
	TerminalFailed    TerminalKind = "failed"
	TerminalCanceled  TerminalKind = "canceled"
)

type RepairKind string

const (
	RepairCompletion   RepairKind = "completion"
	RepairWorkspace    RepairKind = "workspace"
	RepairDeclaration  RepairKind = "declaration"
	RepairVerification RepairKind = "verification"
)

type StepAction string

const (
	StepActionNone              StepAction = ""
	StepActionRepairToolFailure StepAction = "repair_tool_failure"
	StepActionRepairCompletion  StepAction = "repair_completion"
	StepActionRepairWorkspace   StepAction = "repair_workspace"
	StepActionRepairDeclaration StepAction = "repair_declaration"
	StepActionVerify            StepAction = "verify"
	StepActionFinalize          StepAction = "finalize"
	StepActionBlock             StepAction = "block"
	StepActionComplete          StepAction = "complete"
)

type ConvergenceCause string

const (
	ConvergenceOutputLimit  ConvergenceCause = "output_limit"
	ConvergenceNoProgress   ConvergenceCause = "no_progress"
	ConvergenceRepairBudget ConvergenceCause = "repair_budget"
	ConvergenceStepLimit    ConvergenceCause = "step_limit"
)

type VerificationAction string

const (
	VerificationActionPassed   VerificationAction = "passed"
	VerificationActionRepair   VerificationAction = "repair"
	VerificationActionReported VerificationAction = "reported"
	VerificationActionBlocked  VerificationAction = "blocked"
	VerificationActionFailed   VerificationAction = "failed"
	VerificationActionReverted VerificationAction = "reverted"
)

type RepairBudget struct {
	ProgressKey string `json:"progress_key"`
	Consecutive uint32 `json:"consecutive"`
	Steps       uint32 `json:"steps"`
}

type ProgressStage string

const (
	ProgressStageNone       ProgressStage = ""
	ProgressStageConverge   ProgressStage = "converge"
	ProgressStageFinishOnly ProgressStage = "finish_only"
	ProgressStageExhausted  ProgressStage = "exhausted"
)

type ProgressState struct {
	Signature         string        `json:"signature,omitempty"`
	ObservedSamples   uint32        `json:"observed_samples"`
	NoProgressSamples uint32        `json:"no_progress_samples"`
	Stage             ProgressStage `json:"stage,omitempty"`
}

type ConvergenceState struct {
	Cause                 ConvergenceCause `json:"cause"`
	Used                  uint32           `json:"used"`
	Limit                 uint32           `json:"limit"`
	RepairKind            RepairKind       `json:"repair_kind,omitempty"`
	FinalizationAttempted bool             `json:"finalization_attempted,omitempty"`
	Summary               string           `json:"summary,omitempty"`
	PendingActions        []string         `json:"pending_actions,omitempty"`
}

type ConvergencePolicy struct {
	ProgressConverge   uint32 `json:"progress_converge"`
	ProgressFinishOnly uint32 `json:"progress_finish_only"`
	ProgressLimit      uint32 `json:"progress_limit"`
	ResearchConverge   uint32 `json:"research_converge"`
	ResearchFinishOnly uint32 `json:"research_finish_only"`
	ResearchLimit      uint32 `json:"research_limit"`
}

type Policy struct {
	CompletionRequired         bool   `json:"completion_required"`
	StructuredTerminalRequired bool   `json:"structured_terminal_required"`
	VerificationRequired       bool   `json:"verification_required"`
	VerificationMustPass       bool   `json:"verification_must_pass"`
	VerificationMode           string `json:"verification_mode,omitempty"`
	VerificationOnFailure      string `json:"verification_on_failure,omitempty"`
	CompletionRepairLimit      uint32 `json:"completion_repair_limit"`
	WorkspaceRepairLimit       uint32 `json:"workspace_repair_limit"`
	DeclarationRepairLimit     uint32 `json:"declaration_repair_limit"`
	VerificationRepairLimit    uint32 `json:"verification_repair_limit"`
	// ExecutionStepLimit is an explicit caller budget. Zero leaves execution
	// bounded only by progress convergence and other explicit resource budgets.
	ExecutionStepLimit uint32            `json:"execution_step_limit,omitempty"`
	JournalRequired    bool              `json:"journal_required"`
	Convergence        ConvergencePolicy `json:"convergence"`
}

func DefaultConvergencePolicy() ConvergencePolicy {
	return ConvergencePolicy{
		ProgressConverge:   16,
		ProgressFinishOnly: 32,
		ProgressLimit:      48,
		ResearchConverge:   8,
		ResearchFinishOnly: 12,
		ResearchLimit:      16,
	}
}

func DefaultPolicy() Policy {
	return Policy{
		CompletionRequired:      true,
		VerificationRequired:    true,
		VerificationMustPass:    true,
		VerificationMode:        "hard",
		VerificationOnFailure:   "fail",
		CompletionRepairLimit:   2,
		WorkspaceRepairLimit:    1,
		DeclarationRepairLimit:  2,
		VerificationRepairLimit: 1,
		Convergence:             DefaultConvergencePolicy(),
	}
}

type ToolCallState struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Arguments         string `json:"arguments,omitempty"`
	CatalogID         string `json:"catalog_id,omitempty"`
	CatalogGeneration uint64 `json:"catalog_generation,omitempty"`
	CatalogRevision   uint64 `json:"catalog_revision,omitempty"`
	CatalogAuthority  uint64 `json:"catalog_authority,omitempty"`
}

type ToolResultState struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	IsError bool   `json:"is_error"`
}

type ObservedChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type ApprovalState struct {
	RequestID string `json:"request_id"`
	CallID    string `json:"call_id"`
}

type InputState struct {
	RequestID string `json:"request_id"`
}

type CompletionDecision struct {
	Accepted       bool     `json:"accepted"`
	Summary        string   `json:"summary,omitempty"`
	Reason         string   `json:"reason,omitempty"`
	RequiredAction string   `json:"required_action,omitempty"`
	OutputMode     string   `json:"output_mode,omitempty"`
	PendingActions []string `json:"pending_actions,omitempty"`
	Mutation       uint64   `json:"mutation_revision"`
	ChangedPaths   []string `json:"changed_paths,omitempty"`
	QualityCalls   []string `json:"quality_call_ids,omitempty"`
	CompletionCall string   `json:"completion_call_id,omitempty"`
}

type VerificationState struct {
	Status         VerificationStatus `json:"status"`
	Action         VerificationAction `json:"action,omitempty"`
	Mutation       uint64             `json:"mutation_revision"`
	EvidenceCalls  []string           `json:"evidence_call_ids,omitempty"`
	FailureMessage string             `json:"failure_message,omitempty"`
}

type TerminalDecision struct {
	Kind        TerminalKind            `json:"kind"`
	Code        string                  `json:"code,omitempty"`
	Message     string                  `json:"message,omitempty"`
	Fault       *protocol.FaultMetadata `json:"fault,omitempty"`
	Convergence *ConvergenceState       `json:"convergence,omitempty"`
}

type UsageState struct {
	Calls           uint32 `json:"calls,omitempty"`
	InputTokens     uint64 `json:"input_tokens"`
	OutputTokens    uint64 `json:"output_tokens"`
	ReasoningTokens uint64 `json:"reasoning_tokens,omitempty"`
	CachedTokens    uint64 `json:"cached_tokens,omitempty"`
	CostMicrounits  uint64 `json:"cost_microunits,omitempty"`
	CostKnown       bool   `json:"cost_known"`
	Frozen          bool   `json:"frozen"`
}

type ContextState struct {
	Digest        string `json:"digest,omitempty"`
	HistoryBytes  int    `json:"history_bytes,omitempty"`
	MaxBytes      int    `json:"max_bytes,omitempty"`
	Compactions   uint32 `json:"compactions,omitempty"`
	Frozen        bool   `json:"frozen"`
	Truncated     bool   `json:"truncated,omitempty"`
	TruncateCause string `json:"truncate_cause,omitempty"`
}

type ModelSampleState struct {
	ID              string                             `json:"id"`
	Attempt         uint32                             `json:"attempt"`
	Status          SampleStatus                       `json:"status"`
	ProviderRetries uint32                             `json:"provider_retries,omitempty"`
	LastFailure     *provider.Failure                  `json:"last_failure,omitempty"`
	Retry           *ProviderRetryState                `json:"retry,omitempty"`
	Assembly        *providerassembly.ResponseAssembly `json:"assembly,omitempty"`
	Error           string                             `json:"error,omitempty"`
}

type ProviderRetryState struct {
	EffectID         string    `json:"effect_id"`
	Attempt          uint32    `json:"attempt"`
	Retry            uint32    `json:"retry"`
	EffectiveDelayMS uint64    `json:"effective_delay_ms"`
	RetryAt          time.Time `json:"retry_at"`
	PolicyRevision   string    `json:"policy_revision"`
}

type CancellationState struct {
	Requested bool   `json:"requested"`
	Accepted  bool   `json:"accepted"`
	Reason    string `json:"reason,omitempty"`
}

type RecoveryRelation struct {
	SourceTurnID   string `json:"source_turn_id"`
	RecoveryTurnID string `json:"recovery_turn_id"`
	Action         string `json:"action"`
	DraftResumed   bool   `json:"draft_resumed,omitempty"`
}

type State struct {
	Phase                 Phase                       `json:"phase"`
	Intent                protocol.TurnIntent         `json:"intent"`
	Mode                  string                      `json:"mode"`
	ProfileRevision       uint64                      `json:"profile_revision"`
	Policy                Policy                      `json:"policy"`
	MutationRevision      uint64                      `json:"mutation_revision"`
	OpenCalls             map[string]ToolCallState    `json:"open_calls"`
	ClosedCalls           map[string]ToolResultState  `json:"closed_calls"`
	PendingApprovals      map[string]ApprovalState    `json:"pending_approvals"`
	PendingInput          *InputState                 `json:"pending_input,omitempty"`
	Changes               []ObservedChange            `json:"changes,omitempty"`
	Completion            *CompletionDecision         `json:"completion,omitempty"`
	Verification          VerificationState           `json:"verification"`
	Journal               JournalStatus               `json:"journal"`
	Usage                 UsageState                  `json:"usage"`
	Context               ContextState                `json:"context"`
	SampleLedger          map[string]ModelSampleState `json:"sample_ledger"`
	ActiveSampleID        string                      `json:"active_sample_id,omitempty"`
	Cancellation          CancellationState           `json:"cancellation"`
	RecoveryRelation      *RecoveryRelation           `json:"recovery_relation,omitempty"`
	PendingEffects        map[string]Effect           `json:"pending_effects"`
	CompletedEffects      map[string]Effect           `json:"completed_effects"`
	NextEffectSequence    uint64                      `json:"next_effect_sequence"`
	ProvisionalOutput     []string                    `json:"provisional_output,omitempty"`
	FinalOutput           []string                    `json:"final_output,omitempty"`
	OutputEligibility     bool                        `json:"output_eligibility"`
	RepairBudgets         map[RepairKind]RepairBudget `json:"repair_budgets"`
	Progress              ProgressState               `json:"progress"`
	Convergence           *ConvergenceState           `json:"convergence,omitempty"`
	NextAction            StepAction                  `json:"next_action,omitempty"`
	LastModelContinued    bool                        `json:"last_model_continued,omitempty"`
	UnresolvedToolFailure bool                        `json:"unresolved_tool_failure,omitempty"`
	RecoveryToolSucceeded bool                        `json:"recovery_tool_succeeded,omitempty"`
	PendingTerminal       *TerminalDecision           `json:"pending_terminal,omitempty"`
	Terminal              *TerminalDecision           `json:"terminal,omitempty"`
}

func NewState(intent protocol.TurnIntent, mode string, profileRevision uint64) State {
	return State{
		Phase:            PhaseCreated,
		Intent:           protocol.NormalizeTurnIntent(intent),
		Mode:             mode,
		ProfileRevision:  profileRevision,
		Policy:           DefaultPolicy(),
		OpenCalls:        make(map[string]ToolCallState),
		ClosedCalls:      make(map[string]ToolResultState),
		PendingApprovals: make(map[string]ApprovalState),
		SampleLedger:     make(map[string]ModelSampleState),
		PendingEffects:   make(map[string]Effect),
		CompletedEffects: make(map[string]Effect),
		RepairBudgets:    make(map[RepairKind]RepairBudget),
		Verification: VerificationState{
			Status: VerificationNotEvaluated,
		},
		Journal: JournalNone,
	}
}

func NewStateWithPolicy(
	intent protocol.TurnIntent,
	mode string,
	profileRevision uint64,
	policy Policy,
) State {
	state := NewState(intent, mode, profileRevision)
	if policy.Convergence == (ConvergencePolicy{}) {
		policy.Convergence = DefaultConvergencePolicy()
	}
	state.Policy = policy
	return state
}

func RequiresCompletion(state State) bool {
	if !state.Policy.CompletionRequired {
		return false
	}
	if state.Policy.StructuredTerminalRequired {
		return true
	}
	required := state.MutationRevision != 0 ||
		state.Intent == protocol.TurnIntentWorkspaceChange
	if state.Intent == protocol.TurnIntentOperation {
		for _, result := range state.ClosedCalls {
			required = required || !result.IsError
		}
	}
	return required
}

func IsResearchIntent(intent protocol.TurnIntent) bool {
	return intent == protocol.TurnIntentAnswer ||
		intent == protocol.TurnIntentPlan
}
