package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const (
	SourceDefault Source = "default"
	SourceFile    Source = "file"
	SourceRepo    Source = "repo"
	SourceEnv     Source = "env"
	SourceCLI     Source = "cli"
)

const (
	fieldOperationBuffer  = "runtime.operation_buffer"
	fieldEventHistory     = "runtime.event_history"
	fieldSubscriberBuffer = "runtime.subscriber_buffer"
	fieldStateDataDir     = "state.data_dir"
	fieldStateBusyTimeout = "state.busy_timeout"
	fieldStateRetention   = "state.event_retention"
	fieldMemoryEnabled    = "memory.enabled"
	fieldMemoryPath       = "memory.path"
	fieldIndexEnabled     = "context.index.enabled"
	fieldIndexMaxBytes    = "context.index.max_file_bytes"
	fieldIndexMaxFiles    = "context.index.max_files"

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

	fieldCompactMaxHistory = "context.compact.max_history_bytes"
	fieldCompactSummaryMax = "context.compact.summary_max_bytes"
	fieldCompactMaxDigest  = "context.compact.max_digest_entries"

	fieldLogLevel        = "telemetry.log_level"
	fieldCredentialKind  = "credential.kind"
	fieldCredentialName  = "credential.name"
	fieldProvider        = "execution.provider"
	fieldModel           = "execution.model"
	fieldProtocol        = "execution.protocol"
	fieldMode            = "execution.mode"
	fieldWorkspace       = "execution.workspace"
	fieldTools           = "execution.tools"
	fieldMaxOutputTokens = "execution.max_output_tokens"
	fieldMaxSteps        = "execution.max_steps"
	fieldTimeout         = "execution.timeout"
	fieldIdleTimeout     = "execution.idle_timeout"
	fieldMaxConcurrent   = "execution.max_concurrent"
	fieldRateLimit       = "execution.rate_limit"
	fieldBudgetTokens    = "execution.budget_tokens"
	fieldBudgetUSD       = "execution.budget_usd"
	fieldReasoning       = "execution.reasoning_effort"
	fieldNativeSearch    = "execution.native_search"
	fieldVerifyMode      = "execution.verify.mode"
	fieldVerifyScope     = "execution.verify.scope"
	fieldVerifyOnFailure = "execution.verify.on_failure"
	fieldVerifyCommand   = "execution.verify.command"
	fieldVerifyRepair    = "execution.verify.max_repair_steps"
	fieldVerifyTimeout   = "execution.verify.timeout"

	fieldSubagentMaxDepth    = "execution.subagent.max_depth"
	fieldSubagentMaxParallel = "execution.subagent.max_parallel"
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

var secretNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_./:@-]*$`)

type Source string

type SecretRef struct {
	Kind string `json:"kind,omitempty" toml:"kind"`
	Name string `json:"name,omitempty" toml:"name"`
}

func (r SecretRef) Empty() bool {
	return r.Kind == "" && r.Name == ""
}

type Runtime struct {
	OperationBuffer  int `json:"operation_buffer" toml:"operation_buffer"`
	EventHistory     int `json:"event_history" toml:"event_history"`
	SubscriberBuffer int `json:"subscriber_buffer" toml:"subscriber_buffer"`
}

type State struct {
	DataDir        string        `json:"data_dir" toml:"data_dir"`
	BusyTimeout    time.Duration `json:"busy_timeout" toml:"-"`
	EventRetention int           `json:"event_retention" toml:"event_retention"`
}

type Memory struct {
	Enabled bool   `json:"enabled" toml:"enabled"`
	Path    string `json:"path" toml:"path"`
}

type Telemetry struct {
	LogLevel string `json:"log_level" toml:"log_level"`
}

// Context configures what the agent knows about the repository before it starts
// searching.
type Context struct {
	Index        Index        `json:"index" toml:"index"`
	RepoMap      RepoMap      `json:"repo_map" toml:"repo_map"`
	WorkingSet   WorkingSet   `json:"working_set" toml:"working_set"`
	Evidence     Evidence     `json:"evidence" toml:"evidence"`
	CodingPolicy CodingPolicy `json:"coding_policy" toml:"coding_policy"`
	Compact      Compact      `json:"compact" toml:"compact"`
}

// Compact configures what happens when a thread outgrows its context window.
//
// MaxHistoryBytes is the threshold: history above it is replaced by a summary at
// the next opportunity. SummaryMaxBytes caps the summary itself, and is the knob
// that decides how much survives — a summary cut short drops its cheapest
// sections first, keeping the goal and the outstanding work. MaxDigestEntries
// bounds the per-message record of what was removed.
type Compact struct {
	MaxHistoryBytes  int `json:"max_history_bytes" toml:"max_history_bytes"`
	SummaryMaxBytes  int `json:"summary_max_bytes" toml:"summary_max_bytes"`
	MaxDigestEntries int `json:"max_digest_entries" toml:"max_digest_entries"`
}

// RepoMap configures the repository overview appended to every request: which
// directories hold code, how the project is built, where it starts, and what the
// files in play declare. MaxBytes is the ceiling per request, so it is the knob
// that decides what the map costs on a long session.
type RepoMap struct {
	Enabled        bool `json:"enabled" toml:"enabled"`
	MaxBytes       int  `json:"max_bytes" toml:"max_bytes"`
	MaxDirectories int  `json:"max_directories" toml:"max_directories"`
}

// WorkingSet configures the ledger of paths the session has touched. MaxEntries
// bounds the paths the runtime discovered on its own; paths the user pinned are
// always reported.
type WorkingSet struct {
	Enabled    bool `json:"enabled" toml:"enabled"`
	MaxEntries int  `json:"max_entries" toml:"max_entries"`
	MaxBytes   int  `json:"max_bytes" toml:"max_bytes"`
}

// Evidence configures the section reporting what lookups established, which
// changes nothing has verified, and which calls were wasted. MaxEntries bounds
// only the facts: risks and reminders are the point of the section and are always
// reported in full.
type Evidence struct {
	Enabled    bool `json:"enabled" toml:"enabled"`
	MaxEntries int  `json:"max_entries" toml:"max_entries"`
	MaxBytes   int  `json:"max_bytes" toml:"max_bytes"`
}

// CodingPolicy configures the working method carried in the stable prefix. It has
// no ceiling because the text is a constant; disabling it removes the instruction
// but not the mechanisms behind it, which the runtime enforces regardless.
type CodingPolicy struct {
	Enabled bool `json:"enabled" toml:"enabled"`
}

// Index configures the repository symbol index. The ceilings bound a first
// build on a large repository: a file over MaxFileBytes is recorded without
// symbols, and a repository over MaxFiles is indexed only that far, which the
// symbol tools report as a truncated index rather than as complete results.
type Index struct {
	Enabled      bool  `json:"enabled" toml:"enabled"`
	MaxFileBytes int64 `json:"max_file_bytes" toml:"max_file_bytes"`
	MaxFiles     int   `json:"max_files" toml:"max_files"`
}

type Execution struct {
	Provider        string        `json:"provider" toml:"provider"`
	Model           string        `json:"model" toml:"model"`
	Protocol        string        `json:"protocol" toml:"protocol"`
	Mode            string        `json:"mode" toml:"mode"`
	Workspace       string        `json:"workspace" toml:"workspace"`
	Tools           bool          `json:"tools" toml:"tools"`
	MaxOutputTokens uint64        `json:"max_output_tokens" toml:"max_output_tokens"`
	MaxSteps        int           `json:"max_steps" toml:"max_steps"`
	Timeout         time.Duration `json:"timeout" toml:"-"`
	IdleTimeout     time.Duration `json:"idle_timeout" toml:"-"`
	MaxConcurrent   int           `json:"max_concurrent" toml:"max_concurrent"`
	RateLimit       float64       `json:"rate_limit" toml:"rate_limit"`
	BudgetTokens    uint64        `json:"budget_tokens" toml:"budget_tokens"`
	BudgetUSD       float64       `json:"budget_usd" toml:"budget_usd"`
	ReasoningEffort string        `json:"reasoning_effort" toml:"reasoning_effort"`
	NativeSearch    bool          `json:"native_search" toml:"native_search"`
	Verify          Verify        `json:"verify" toml:"verify"`
	Subagent        Subagent      `json:"subagent" toml:"subagent"`
	Worker          Worker        `json:"worker" toml:"worker"`
	Journal         Journal       `json:"journal" toml:"journal"`
}

// Journal configures the edit-transaction journal.
//
// Durable puts the turn ledger and before-images under the workspace state
// directory so a process killed mid-turn can be undone by the next one. Turning
// it off keeps atomicity inside the live process only, which is what a throwaway
// workspace wants and what a workspace holding real work does not.
type Journal struct {
	Durable bool `json:"durable" toml:"durable"`
	// RecoverOnStart undoes interrupted turns found at startup. Off leaves them
	// in the ledger for a later, explicit recovery.
	RecoverOnStart bool `json:"recover_on_start" toml:"recover_on_start"`
}

// Worker bounds the scheduler that executes durable background tasks. It is off
// for one-shot hosts: a process that exists to run one command should not pick
// up background work it would then have to finish before exiting.
type Worker struct {
	Enabled     bool `json:"enabled" toml:"enabled"`
	MaxParallel int  `json:"max_parallel" toml:"max_parallel"`
	// MaxAttempts is the default attempt budget for tasks that do not set one.
	MaxAttempts int `json:"max_attempts" toml:"max_attempts"`
	// Lease is how long a claim survives without a heartbeat. Shorter means a
	// dead worker's task is taken over sooner, and a slow one loses it sooner.
	Lease time.Duration `json:"lease" toml:"-"`
	// ClaimInterval is how often the scheduler looks for runnable work, and
	// AutomationInterval how often it checks for due schedules.
	ClaimInterval      time.Duration `json:"claim_interval" toml:"-"`
	AutomationInterval time.Duration `json:"automation_interval" toml:"-"`
	// RetryBackoff is the first delay after a retryable failure; each further
	// attempt doubles it up to RetryBackoffMax.
	RetryBackoff    time.Duration `json:"retry_backoff" toml:"-"`
	RetryBackoffMax time.Duration `json:"retry_backoff_max" toml:"-"`
	// MaxTokens and MaxCostUSD bound all background tasks in this process
	// together, separately from the session and child-agent ledgers, so that an
	// operator can tell which pot was spent.
	MaxTokens  uint64  `json:"max_tokens" toml:"max_tokens"`
	MaxCostUSD float64 `json:"max_cost_usd" toml:"max_cost_usd"`
}

// Subagent bounds what a spawned child agent may spend and where it may write.
// Child budgets are deliberately separate from the session budget: a runaway
// child must not be able to consume the parent's whole allowance, and a child
// that writes must not be able to write into the parent's workspace.
type Subagent struct {
	MaxDepth    int `json:"max_depth" toml:"max_depth"`
	MaxParallel int `json:"max_parallel" toml:"max_parallel"`
	// MaxSteps is the child's own step quota, independent of Execution.MaxSteps.
	MaxSteps int `json:"max_steps" toml:"max_steps"`
	// MaxTokens and MaxCostUSD bound all child agents in the session together,
	// and each child is capped by the same number on its own: the shared ledger
	// refuses the next child once the pot is spent, while the per-child ceiling
	// stops a single runaway child during its turn. Zero means unbounded beyond
	// the session budget the child inherits.
	MaxTokens  uint64  `json:"max_tokens" toml:"max_tokens"`
	MaxCostUSD float64 `json:"max_cost_usd" toml:"max_cost_usd"`
	// WallTime is how long one child turn may run before it is canceled.
	WallTime time.Duration `json:"wall_time" toml:"-"`
	// Workspace selects the isolation strategy: auto picks per stance,
	// read_only shares the parent workspace without a journal, worktree
	// requires a git worktree per writing child.
	Workspace string `json:"workspace" toml:"workspace"`
}

// Subagent workspace isolation strategies.
const (
	SubagentWorkspaceAuto       = "auto"
	SubagentWorkspaceReadOnly   = "read_only"
	SubagentWorkspaceWorktree   = "worktree"
	SubagentWorkspaceSerialized = "same_workspace_serialized"
)

// Verify configures the gate that runs before a turn commits its edits.
//
// Mode off skips the gate; soft reports the verdict without changing the turn
// outcome; hard applies OnFailure once the repair budget is spent.
type Verify struct {
	Mode      string `json:"mode" toml:"mode"`
	Scope     string `json:"scope" toml:"scope"`
	OnFailure string `json:"on_failure" toml:"on_failure"`
	// Command overrides the repository scope's detected commands with one shell
	// command, for workspaces whose entry point cannot be inferred.
	Command        string        `json:"command,omitempty" toml:"command"`
	MaxRepairSteps int           `json:"max_repair_steps" toml:"max_repair_steps"`
	Timeout        time.Duration `json:"timeout" toml:"-"`
}

type Vision struct {
	Enabled  bool   `json:"enabled" toml:"enabled"`
	Provider string `json:"provider" toml:"provider"`
	Model    string `json:"model" toml:"model"`
}

// RouteSlot is the provider and model one purpose samples on.
type RouteSlot struct {
	Provider string `json:"provider" toml:"provider"`
	Model    string `json:"model" toml:"model"`
}

// Empty reports a slot nobody configured.
func (s RouteSlot) Empty() bool { return s.Provider == "" && s.Model == "" }

// Route is per-purpose model selection. Slots is keyed by purpose name and holds
// only what configuration named; the act route stays in Execution, because
// execution.provider and execution.model already are it.
//
// Lock forbids falling back to act, which is what a reproducible run wants: with
// it on, a purpose without a slot is an error rather than a silent substitution.
type Route struct {
	Lock  bool                 `json:"lock" toml:"lock"`
	Slots map[string]RouteSlot `json:"slots,omitempty" toml:"-"`
}

type Web struct {
	SearchBackend string `json:"search_backend" toml:"search_backend"`
}

type Config struct {
	Runtime    Runtime   `json:"runtime" toml:"runtime"`
	State      State     `json:"state" toml:"state"`
	Memory     Memory    `json:"memory" toml:"memory"`
	Context    Context   `json:"context" toml:"context"`
	Telemetry  Telemetry `json:"telemetry" toml:"telemetry"`
	Credential SecretRef `json:"credential" toml:"credential"`
	Execution  Execution `json:"execution" toml:"execution"`
	Route      Route     `json:"route" toml:"route"`
	Vision     Vision    `json:"vision" toml:"vision"`
	Web        Web       `json:"web" toml:"web"`
}

type Snapshot struct {
	Config     Config            `json:"config"`
	Provenance map[string]Source `json:"provenance"`
}

type Overrides struct {
	OperationBuffer  *int
	EventHistory     *int
	SubscriberBuffer *int
	StateDataDir     *string
	StateBusyTimeout *time.Duration
	StateRetention   *int
	MemoryEnabled    *bool
	MemoryPath       *string
	IndexEnabled     *bool
	IndexMaxBytes    *int64
	IndexMaxFiles    *int

	RepoMapEnabled        *bool
	RepoMapMaxBytes       *int
	RepoMapMaxDirectories *int
	WorkingSetEnabled     *bool
	WorkingSetMaxEntries  *int
	WorkingSetMaxBytes    *int
	EvidenceEnabled       *bool
	EvidenceMaxEntries    *int
	EvidenceMaxBytes      *int
	CodingPolicyEnabled   *bool
	CompactMaxHistory     *int
	CompactSummaryMax     *int
	CompactMaxDigest      *int

	LogLevel        *string
	CredentialKind  *string
	CredentialName  *string
	Provider        *string
	Model           *string
	Protocol        *string
	Mode            *string
	Workspace       *string
	Tools           *bool
	MaxOutputTokens *uint64
	MaxSteps        *int
	Timeout         *time.Duration
	IdleTimeout     *time.Duration
	MaxConcurrent   *int
	RateLimit       *float64
	BudgetTokens    *uint64
	BudgetUSD       *float64
	ReasoningEffort *string
	NativeSearch    *bool
	VerifyMode      *string
	VerifyScope     *string
	VerifyOnFailure *string
	VerifyCommand   *string
	VerifyRepair    *int
	VerifyTimeout   *time.Duration

	SubagentMaxDepth    *int
	SubagentMaxParallel *int
	SubagentMaxSteps    *int
	SubagentMaxTokens   *uint64
	SubagentMaxCostUSD  *float64
	SubagentWallTime    *time.Duration
	SubagentWorkspace   *string

	WorkerEnabled            *bool
	WorkerMaxParallel        *int
	WorkerMaxAttempts        *int
	WorkerLease              *time.Duration
	WorkerClaimInterval      *time.Duration
	WorkerAutomationInterval *time.Duration
	WorkerRetryBackoff       *time.Duration
	WorkerRetryBackoffMax    *time.Duration
	WorkerMaxTokens          *uint64
	WorkerMaxCostUSD         *float64

	VisionEnabled    *bool
	VisionProvider   *string
	VisionModel      *string
	WebSearchBackend *string

	// RouteLock forbids falling back to the act route. Slots themselves are
	// configuration-only: six purposes worth of provider/model flags would crowd
	// every command's help for something a reproducible run states in a file.
	RouteLock *bool
}

type LoadOptions struct {
	Path string
	// RepoPath is an untrusted project-local config (N19). When set, it is
	// applied after Path but cannot override credential/provider/model/protocol.
	RepoPath string
	// TrustRepo when true allows RepoPath to set denylisted fields (explicit opt-in).
	TrustRepo bool
	LookupEnv func(string) (string, bool)
	Overrides Overrides
}

type FieldError struct {
	Field  string
	Source Source
	Reason string
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("invalid config field %s from %s: %s", e.Field, e.Source, e.Reason)
}

func Defaults() Config {
	return Config{
		Runtime: Runtime{
			OperationBuffer:  64,
			EventHistory:     256,
			SubscriberBuffer: 64,
		},
		State: State{
			DataDir:        defaultDataDir(),
			BusyTimeout:    5 * time.Second,
			EventRetention: 1_000_000,
		},
		Memory: Memory{Path: defaultMemoryPath()},
		// The index is on by default: it is what the symbol tools need, and a
		// repository that cannot be indexed degrades to plain search on its own.
		Context: Context{
			Index: Index{Enabled: true, MaxFileBytes: 1 << 20, MaxFiles: 20000},
			// Both volatile sections are on by default and bounded: they are what
			// keeps an agent from rediscovering the repository every turn, and the
			// ceilings are what keeps that from costing an unbounded number of
			// input tokens on a long session.
			RepoMap:    RepoMap{Enabled: true, MaxBytes: 8 << 10, MaxDirectories: 24},
			WorkingSet: WorkingSet{Enabled: true, MaxEntries: 16, MaxBytes: 8 << 10},
			// The evidence section and the method that goes with it are on for the
			// same reason: an agent that is told what it has not proved is cheaper
			// than one that has to be asked.
			Evidence:     Evidence{Enabled: true, MaxEntries: 24, MaxBytes: 4 << 10},
			CodingPolicy: CodingPolicy{Enabled: true},
			// 256 KiB of history is roughly where a long session starts crowding a
			// 200K-token window; the summary gets 8 KiB of it, which is enough for
			// the structured sections plus a hundred-odd lines of transcript.
			Compact: Compact{
				MaxHistoryBytes: 256 << 10, SummaryMaxBytes: 8 << 10, MaxDigestEntries: 120,
			},
		},
		Telemetry: Telemetry{LogLevel: "info"},
		Execution: Execution{
			Protocol: "openai_chat", Mode: "act", Workspace: ".",
			MaxOutputTokens: 4096, MaxSteps: 64, Timeout: 2 * time.Minute,
			IdleTimeout: 60 * time.Second, MaxConcurrent: 8,
			// Soft verification is the production default: every observed edit
			// forms a verdict, but a failing or unavailable check does not turn a
			// successful edit into a failed turn. Operators that need enforcement
			// opt into hard; tests or constrained hosts can still explicitly use off.
			Verify: Verify{
				Mode: "soft", Scope: "diagnostics", OnFailure: "fail",
				MaxRepairSteps: 1, Timeout: 2 * time.Minute,
			},
			// A child gets half the parent's steps and five minutes of wall
			// clock: enough for a scoped task, small enough that a confused
			// child cannot burn the session.
			Subagent: Subagent{
				MaxDepth: 5, MaxParallel: 4, MaxSteps: 8,
				WallTime: 5 * time.Minute, Workspace: SubagentWorkspaceAuto,
			},
			// The scheduler is off by default and each host turns it on for
			// itself: the long-lived hosts want it, and `exec` must not.
			Worker: Worker{
				MaxParallel: 2, MaxAttempts: 1, Lease: 30 * time.Second,
				ClaimInterval: time.Second, AutomationInterval: 30 * time.Second,
				RetryBackoff: 15 * time.Second, RetryBackoffMax: 10 * time.Minute,
			},
			// Durable by default: a half-applied edit left by a killed process is
			// the kind of damage a person has to find by hand, and the cost is two
			// small appends per edited file.
			Journal: Journal{Durable: true, RecoverOnStart: true},
		},
	}
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(".codehelper", "v1")
	}
	return filepath.Join(home, ".codehelper", "v1")
}

func defaultMemoryPath() string {
	return filepath.Join(defaultDataDir(), "memory")
}

func Load(options LoadOptions) (Snapshot, error) {
	config := Defaults()
	provenance := defaultProvenance()

	if options.Path != "" {
		if err := applyFile(options.Path, &config, provenance, SourceFile, true); err != nil {
			return Snapshot{}, err
		}
	}
	if options.RepoPath != "" {
		if err := applyFile(
			options.RepoPath, &config, provenance, SourceRepo, options.TrustRepo,
		); err != nil {
			return Snapshot{}, err
		}
	}
	lookupEnv := options.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	if err := applyEnvironment(lookupEnv, &config, provenance); err != nil {
		return Snapshot{}, err
	}
	applyOverrides(options.Overrides, &config, provenance)
	normalizeRoutes(&config, provenance)

	snapshot := Snapshot{Config: config, Provenance: provenance}
	if err := snapshot.Validate(); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
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
		// The ceilings only matter with the index on, and a zero one would index
		// nothing while still reporting itself ready.
		if index.MaxFileBytes < 1024 || index.MaxFileBytes > 64<<20 {
			return fieldError(fieldIndexMaxBytes, s.Provenance,
				"must be between 1024 and 67108864")
		}
		if err := checkRange(fieldIndexMaxFiles, index.MaxFiles, 1_000_000); err != nil {
			return err
		}
	}
	if repoMap := s.Config.Context.RepoMap; repoMap.Enabled {
		// The floor is what a useful map costs: a header plus a handful of
		// directory lines. Below it the section only reports its own truncation.
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
	// Compaction has no off switch, so its ceilings are always checked. The floor
	// on the threshold is deliberately low: a benchmark forces compaction by
	// setting a few hundred bytes, and a floor that forbade it would put the
	// behaviour out of reach of a test.
	compaction := s.Config.Context.Compact
	if compaction.MaxHistoryBytes < 256 || compaction.MaxHistoryBytes > 64<<20 {
		return fieldError(fieldCompactMaxHistory, s.Provenance,
			"must be between 256 and 67108864")
	}
	// A summary needs room for the wrapper plus a section or two; below that it
	// would only ever report its own truncation.
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
	return s.validateWeb()
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

// normalizeRoutes folds the [vision] section into the route table.
//
// [vision] predates per-purpose routing and stays as an alias for a release
// cycle, because deleting it would leave an upgraded configuration silently
// without vision. The alias only fills a slot nobody named, so [route.vision]
// wins whenever both are present.
func normalizeRoutes(config *Config, provenance map[string]Source) {
	vision := config.Vision
	if !vision.Enabled || vision.Provider == "" || vision.Model == "" {
		return
	}
	if _, configured := config.Route.Slots["vision"]; configured {
		return
	}
	if config.Route.Slots == nil {
		config.Route.Slots = make(map[string]RouteSlot)
	}
	config.Route.Slots["vision"] = RouteSlot{Provider: vision.Provider, Model: vision.Model}
	// The slot inherits the provenance of the section it came from, so a reader
	// asking why it exists is pointed at [vision] rather than at nothing.
	if source, known := provenance[fieldVisionProvider]; known {
		provenance[fieldRouteProvider("vision")] = source
	}
	if source, known := provenance[fieldVisionModel]; known {
		provenance[fieldRouteModel("vision")] = source
	}
}

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

type executionFileConfig struct {
	Provider        *string  `toml:"provider"`
	Model           *string  `toml:"model"`
	Protocol        *string  `toml:"protocol"`
	Mode            *string  `toml:"mode"`
	Workspace       *string  `toml:"workspace"`
	Tools           *bool    `toml:"tools"`
	MaxOutputTokens *uint64  `toml:"max_output_tokens"`
	MaxSteps        *int     `toml:"max_steps"`
	Timeout         *string  `toml:"timeout"`
	IdleTimeout     *string  `toml:"idle_timeout"`
	MaxConcurrent   *int     `toml:"max_concurrent"`
	RateLimit       *float64 `toml:"rate_limit"`
	BudgetTokens    *uint64  `toml:"budget_tokens"`
	BudgetUSD       *float64 `toml:"budget_usd"`
	ReasoningEffort *string  `toml:"reasoning_effort"`
	NativeSearch    *bool    `toml:"native_search"`
	Verify          struct {
		Mode           *string `toml:"mode"`
		Scope          *string `toml:"scope"`
		OnFailure      *string `toml:"on_failure"`
		Command        *string `toml:"command"`
		MaxRepairSteps *int    `toml:"max_repair_steps"`
		Timeout        *string `toml:"timeout"`
	} `toml:"verify"`
	Subagent struct {
		MaxDepth    *int     `toml:"max_depth"`
		MaxParallel *int     `toml:"max_parallel"`
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
			MaxHistoryBytes  *int `toml:"max_history_bytes"`
			SummaryMaxBytes  *int `toml:"summary_max_bytes"`
			MaxDigestEntries *int `toml:"max_digest_entries"`
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
		input.Context.Compact.MaxHistoryBytes, &compaction.MaxHistoryBytes,
		fieldCompactMaxHistory, source, provenance,
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
	return nil
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
	applyInt(input.Subagent.MaxDepth, &child.MaxDepth, fieldSubagentMaxDepth, source, provenance)
	applyInt(input.Subagent.MaxParallel, &child.MaxParallel, fieldSubagentMaxParallel, source, provenance)
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
		lookup, "CODEHELPER_COMPACT_MAX_HISTORY_BYTES", fieldCompactMaxHistory,
		&compaction.MaxHistoryBytes, provenance,
	); err != nil {
		return err
	}
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
	if err := applyEnvDuration(lookup, "CODEHELPER_IDLE_TIMEOUT", fieldIdleTimeout, &execution.IdleTimeout, provenance); err != nil {
		return err
	}
	if err := applyEnvInt(lookup, "CODEHELPER_MAX_CONCURRENT", fieldMaxConcurrent, &execution.MaxConcurrent, provenance); err != nil {
		return err
	}
	if err := applyEnvFloat64(lookup, "CODEHELPER_RATE_LIMIT", fieldRateLimit, &execution.RateLimit, provenance); err != nil {
		return err
	}
	if err := applyEnvUint64(lookup, "CODEHELPER_BUDGET_TOKENS", fieldBudgetTokens, &execution.BudgetTokens, provenance); err != nil {
		return err
	}
	if err := applyEnvFloat64(lookup, "CODEHELPER_BUDGET_USD", fieldBudgetUSD, &execution.BudgetUSD, provenance); err != nil {
		return err
	}
	applyEnvString(lookup, "CODEHELPER_REASONING_EFFORT", fieldReasoning, &execution.ReasoningEffort, provenance)
	if err := applyEnvBool(lookup, "CODEHELPER_NATIVE_SEARCH", fieldNativeSearch, &execution.NativeSearch, provenance); err != nil {
		return err
	}
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
		overrides.CompactMaxHistory, &compaction.MaxHistoryBytes,
		fieldCompactMaxHistory, SourceCLI, provenance,
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
	applyInt(overrides.SubagentMaxDepth, &child.MaxDepth, fieldSubagentMaxDepth, SourceCLI, provenance)
	applyInt(overrides.SubagentMaxParallel, &child.MaxParallel, fieldSubagentMaxParallel, SourceCLI, provenance)
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

func applyInt(value *int, target *int, field string, source Source, provenance map[string]Source) {
	if value != nil {
		*target = *value
		provenance[field] = source
	}
}

func applyInt64(value *int64, target *int64, field string, source Source, provenance map[string]Source) {
	if value != nil {
		*target = *value
		provenance[field] = source
	}
}

func applyUint64(value *uint64, target *uint64, field string, source Source, provenance map[string]Source) {
	if value != nil {
		*target = *value
		provenance[field] = source
	}
}

func applyFloat64(value *float64, target *float64, field string, source Source, provenance map[string]Source) {
	if value != nil {
		*target = *value
		provenance[field] = source
	}
}

func applyBool(value *bool, target *bool, field string, source Source, provenance map[string]Source) {
	if value != nil {
		*target = *value
		provenance[field] = source
	}
}

func applyDuration(value *time.Duration, target *time.Duration, field string, source Source, provenance map[string]Source) {
	if value != nil {
		*target = *value
		provenance[field] = source
	}
}

func applyDurationString(value *string, target *time.Duration, field string, source Source, provenance map[string]Source) {
	if value == nil {
		return
	}
	parsed, err := time.ParseDuration(*value)
	if err != nil {
		*target = 0
	} else {
		*target = parsed
	}
	provenance[field] = source
}

func applyString(value *string, target *string, field string, source Source, provenance map[string]Source) {
	if value != nil {
		*target = *value
		provenance[field] = source
	}
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

func defaultProvenance() map[string]Source {
	return map[string]Source{
		fieldOperationBuffer:  SourceDefault,
		fieldEventHistory:     SourceDefault,
		fieldSubscriberBuffer: SourceDefault,
		fieldStateDataDir:     SourceDefault,
		fieldStateBusyTimeout: SourceDefault,
		fieldStateRetention:   SourceDefault,
		fieldMemoryEnabled:    SourceDefault,
		fieldMemoryPath:       SourceDefault,
		fieldIndexEnabled:     SourceDefault,
		fieldIndexMaxBytes:    SourceDefault,
		fieldIndexMaxFiles:    SourceDefault,

		fieldRepoMapEnabled:        SourceDefault,
		fieldRepoMapMaxBytes:       SourceDefault,
		fieldRepoMapMaxDirectories: SourceDefault,
		fieldWorkingSetEnabled:     SourceDefault,
		fieldWorkingSetMaxEntries:  SourceDefault,
		fieldWorkingSetMaxBytes:    SourceDefault,
		fieldEvidenceEnabled:       SourceDefault,
		fieldEvidenceMaxEntries:    SourceDefault,
		fieldEvidenceMaxBytes:      SourceDefault,
		fieldCodingPolicyEnabled:   SourceDefault,
		fieldCompactMaxHistory:     SourceDefault,
		fieldCompactSummaryMax:     SourceDefault,
		fieldCompactMaxDigest:      SourceDefault,

		fieldLogLevel:        SourceDefault,
		fieldCredentialKind:  SourceDefault,
		fieldCredentialName:  SourceDefault,
		fieldProvider:        SourceDefault,
		fieldModel:           SourceDefault,
		fieldProtocol:        SourceDefault,
		fieldMode:            SourceDefault,
		fieldWorkspace:       SourceDefault,
		fieldTools:           SourceDefault,
		fieldMaxOutputTokens: SourceDefault,
		fieldMaxSteps:        SourceDefault,
		fieldTimeout:         SourceDefault,
		fieldIdleTimeout:     SourceDefault,
		fieldMaxConcurrent:   SourceDefault,
		fieldRateLimit:       SourceDefault,
		fieldBudgetTokens:    SourceDefault,
		fieldBudgetUSD:       SourceDefault,
		fieldReasoning:       SourceDefault,
		fieldNativeSearch:    SourceDefault,
		fieldVerifyMode:      SourceDefault,
		fieldVerifyScope:     SourceDefault,
		fieldVerifyOnFailure: SourceDefault,
		fieldVerifyCommand:   SourceDefault,
		fieldVerifyRepair:    SourceDefault,
		fieldVerifyTimeout:   SourceDefault,

		fieldSubagentMaxDepth:    SourceDefault,
		fieldSubagentMaxParallel: SourceDefault,
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

func fieldError(field string, provenance map[string]Source, reason string) error {
	source, exists := provenance[field]
	if !exists {
		source = SourceDefault
	}
	return &FieldError{Field: field, Source: source, Reason: reason}
}

type Manager struct {
	mu      sync.RWMutex
	options LoadOptions
	current Snapshot
}

type ReloadEvent struct {
	Type    string            `json:"type"`
	Current Snapshot          `json:"current"`
	Problem *protocol.Problem `json:"problem,omitempty"`
}

func NewManager(options LoadOptions) (*Manager, error) {
	snapshot, err := Load(options)
	if err != nil {
		return nil, err
	}
	return &Manager{options: options, current: snapshot}, nil
}

func (m *Manager) Current() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneSnapshot(m.current)
}

func (m *Manager) Reload() (Snapshot, error) {
	event := m.ReloadEvent()
	if event.Problem != nil {
		return Snapshot{}, event.Problem
	}
	return cloneSnapshot(event.Current), nil
}

func (m *Manager) ReloadEvent() ReloadEvent {
	m.mu.RLock()
	options := m.options
	m.mu.RUnlock()
	return m.ReloadFrom(options)
}

func (m *Manager) ReloadFrom(options LoadOptions) ReloadEvent {
	snapshot, err := Load(options)
	if err != nil {
		return ReloadEvent{
			Type:    "config.reload.failed",
			Current: m.Current(),
			Problem: protocol.NewProblem(protocol.CodeInvalidArgument, err.Error(), false, err),
		}
	}
	m.mu.Lock()
	m.options = options
	m.current = snapshot
	m.mu.Unlock()
	return ReloadEvent{Type: "config.reload.succeeded", Current: cloneSnapshot(snapshot)}
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	provenance := make(map[string]Source, len(snapshot.Provenance))
	maps.Copy(provenance, snapshot.Provenance)
	snapshot.Provenance = provenance
	return snapshot
}

func IsFieldError(err error) bool {
	var target *FieldError
	return errors.As(err, &target)
}
