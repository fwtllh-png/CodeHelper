package oracle

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

const SchemaVersion = 2

const (
	RuntimeID      = "runtime"
	SideEffectID   = "side_effect"
	WorkspaceID    = "workspace"
	VerificationID = "verification"
	PersistenceID  = "persistence"
	HostID         = "host"
	SecurityID     = "security"
	ResourceID     = "resource"
	TaskQualityID  = "task_quality"
)

var AllIDs = []string{
	RuntimeID,
	SideEffectID,
	WorkspaceID,
	VerificationID,
	PersistenceID,
	HostID,
	SecurityID,
	ResourceID,
	TaskQualityID,
}

type Domain string

const (
	DomainRuntimeState          Domain = "runtime_state"
	DomainToolOrGuard           Domain = "tool_or_guard"
	DomainPersistenceOrRecovery Domain = "persistence_or_recovery"
	DomainHostProjection        Domain = "host_projection"
	DomainPlatformEnvironment   Domain = "platform_environment"
	DomainTaskQuality           Domain = "task_quality"
	DomainEvaluationHarness     Domain = "evaluation_harness"
)

type Input struct {
	SchemaVersion       int               `json:"schema_version"`
	ScenarioID          string            `json:"scenario_id"`
	RunPartition        string            `json:"run_partition"`
	Evidence            []EvidenceProof   `json:"evidence"`
	Runtime             RuntimeFacts      `json:"runtime"`
	EffectsAvailable    bool              `json:"effects_available"`
	ExpectedEffectCount int               `json:"expected_effect_count"`
	Effects             []EffectFact      `json:"effects"`
	Workspace           WorkspaceFacts    `json:"workspace"`
	Verification        VerificationFacts `json:"verification"`
	Persistence         PersistenceFacts  `json:"persistence"`
	Host                HostFacts         `json:"host"`
	Security            SecurityFacts     `json:"security"`
	Resources           ResourceFacts     `json:"resources"`
	TaskQuality         TaskQualityFacts  `json:"task_quality"`
}

type EvidenceProof struct {
	Kind         string `json:"kind"`
	Producer     string `json:"producer"`
	Digest       string `json:"digest"`
	RunPartition string `json:"run_partition"`
}

type RuntimeFacts struct {
	EvidenceAvailable bool           `json:"evidence_available"`
	Phase             string         `json:"phase"`
	Events            []RuntimeEvent `json:"events"`
	MailboxCount      int            `json:"mailbox_count"`
	MailboxCapacity   int            `json:"mailbox_capacity"`
	BudgetUsed        int64          `json:"budget_used"`
	BudgetLimit       int64          `json:"budget_limit"`
	ParkOwner         string         `json:"park_owner,omitempty"`
	ParkDeadline      string         `json:"park_deadline,omitempty"`
	Projection        ProjectionFact `json:"projection"`
}

type RuntimeEvent struct {
	Sequence    uint64 `json:"sequence"`
	Kind        string `json:"kind"`
	Turn        string `json:"turn"`
	Operation   string `json:"operation"`
	Effect      string `json:"effect,omitempty"`
	ReceiptHash string `json:"receipt_hash,omitempty"`
}

type ProjectionFact struct {
	EvidenceAvailable bool   `json:"evidence_available"`
	RuntimeTerminal   string `json:"runtime_terminal"`
	HostTerminal      string `json:"host_terminal"`
	DuplicateItems    int    `json:"duplicate_items"`
	MissingItems      int    `json:"missing_items"`
}

type EffectFact struct {
	ID               string `json:"id"`
	Consequential    bool   `json:"consequential"`
	Claims           int    `json:"claims"`
	Executions       int    `json:"executions"`
	Results          int    `json:"results"`
	Guarded          bool   `json:"guarded"`
	ApprovalRequired bool   `json:"approval_required"`
	ApprovalBound    bool   `json:"approval_bound"`
}

type WorkspaceFacts struct {
	EvidenceAvailable bool     `json:"evidence_available"`
	BeforeDigest      string   `json:"before_digest"`
	AfterDigest       string   `json:"after_digest"`
	ChangedPaths      []string `json:"changed_paths"`
	AllowedPaths      []string `json:"allowed_paths"`
	PreexistingDirty  []string `json:"preexisting_dirty"`
	PreservedDirty    []string `json:"preserved_dirty"`
	EscapedRoot       bool     `json:"escaped_root"`
}

type VerificationFacts struct {
	EvidenceAvailable bool              `json:"evidence_available"`
	Commands          []VerificationRun `json:"commands"`
}

type VerificationRun struct {
	ID        string      `json:"id"`
	Status    spec.Status `json:"status"`
	ExitCode  *int        `json:"exit_code"`
	TimedOut  bool        `json:"timed_out"`
	Mandatory bool        `json:"mandatory"`
}

type PersistenceFacts struct {
	EvidenceAvailable     bool   `json:"evidence_available"`
	EventDigest           string `json:"event_digest"`
	ReplayDigest          string `json:"replay_digest"`
	SnapshotDigest        string `json:"snapshot_digest"`
	RebuiltDigest         string `json:"rebuilt_digest"`
	TerminalDigest        string `json:"terminal_digest"`
	ReceiptTerminalDigest string `json:"receipt_terminal_digest"`
	OutboxPublications    int    `json:"outbox_publications"`
}

type HostFacts struct {
	EvidenceAvailable bool   `json:"evidence_available"`
	RuntimeTerminal   string `json:"runtime_terminal"`
	VisibleTerminal   string `json:"visible_terminal"`
	DuplicateItems    int    `json:"duplicate_items"`
	MissingItems      int    `json:"missing_items"`
	CursorCommitted   bool   `json:"cursor_committed"`
	Continued         bool   `json:"continued"`
}

type SecurityFacts struct {
	EvidenceAvailable  bool `json:"evidence_available"`
	SecretLeaks        int  `json:"secret_leaks"`
	UnauthorizedEgress int  `json:"unauthorized_egress"`
	SandboxRequired    bool `json:"sandbox_required"`
	SandboxAvailable   bool `json:"sandbox_available"`
	UnsafeLinks        int  `json:"unsafe_links"`
	PermissionBypass   int  `json:"permission_bypass"`
}

type ResourceFacts struct {
	EvidenceAvailable       bool     `json:"evidence_available"`
	ProcessesBefore         int      `json:"processes_before"`
	ProcessesAfter          int      `json:"processes_after"`
	GoroutinesBefore        int      `json:"goroutines_before"`
	GoroutinesAfter         int      `json:"goroutines_after"`
	GoroutineTolerance      int      `json:"goroutine_tolerance"`
	FDsBefore               int      `json:"fds_before"`
	FDsAfter                int      `json:"fds_after"`
	FDTolerance             int      `json:"fd_tolerance"`
	SubscribersBefore       int      `json:"subscribers_before"`
	SubscribersAfter        int      `json:"subscribers_after"`
	TemporaryPathsRemaining int      `json:"temporary_paths_remaining"`
	QueuePeak               int      `json:"queue_peak"`
	QueueCapacity           int      `json:"queue_capacity"`
	InputBytes              int64    `json:"input_bytes"`
	PersistedBytes          int64    `json:"persisted_bytes"`
	MaxAmplificationMilli   int64    `json:"max_amplification_milli"`
	ProcessIDsBefore        []string `json:"process_ids_before"`
	ProcessIDsAfter         []string `json:"process_ids_after"`
}

type TaskQualityFacts struct {
	EvidenceAvailable bool `json:"evidence_available"`
	Deterministic     bool `json:"deterministic"`
	Assertions        int  `json:"assertions"`
	Passed            int  `json:"passed"`
}

type Finding struct {
	OracleID string    `json:"oracle_id"`
	Code     string    `json:"code"`
	Domain   Domain    `json:"domain"`
	Severity spec.Risk `json:"severity"`
	Summary  string    `json:"summary"`
}

type Result struct {
	OracleID string      `json:"oracle_id"`
	Status   spec.Status `json:"status"`
	Findings []Finding   `json:"findings"`
	Summary  string      `json:"summary"`
}

type Report struct {
	SchemaVersion    int         `json:"schema_version"`
	ScenarioID       string      `json:"scenario_id"`
	Status           spec.Status `json:"status"`
	Results          []Result    `json:"results"`
	Primary          *Finding    `json:"primary,omitempty"`
	FailureSignature string      `json:"failure_signature,omitempty"`
}

var (
	identityPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	digestPattern    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	eventKindPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
)

func (i Input) Validate() error {
	if i.SchemaVersion != SchemaVersion {
		return fmt.Errorf("oracle input schema_version = %d, want %d", i.SchemaVersion, SchemaVersion)
	}
	if !identityPattern.MatchString(i.ScenarioID) {
		return errors.New("oracle input scenario_id is invalid")
	}
	if !digestPattern.MatchString(i.RunPartition) {
		return errors.New("oracle input run_partition is invalid")
	}
	if err := validateEvidence(i.Evidence, i.RunPartition); err != nil {
		return err
	}
	if err := i.Runtime.validate(); err != nil {
		return fmt.Errorf("runtime facts: %w", err)
	}
	if i.ExpectedEffectCount < 0 {
		return errors.New("expected effect count cannot be negative")
	}
	if err := validateEffects(i.Effects); err != nil {
		return err
	}
	if err := i.Workspace.validate(); err != nil {
		return fmt.Errorf("workspace facts: %w", err)
	}
	if err := i.Verification.validate(); err != nil {
		return fmt.Errorf("verification facts: %w", err)
	}
	if err := i.Persistence.validate(); err != nil {
		return fmt.Errorf("persistence facts: %w", err)
	}
	if err := i.Host.validate(); err != nil {
		return fmt.Errorf("host facts: %w", err)
	}
	if err := i.Security.validate(); err != nil {
		return fmt.Errorf("security facts: %w", err)
	}
	if err := i.Resources.validate(); err != nil {
		return fmt.Errorf("resource facts: %w", err)
	}
	if err := i.TaskQuality.validate(); err != nil {
		return fmt.Errorf("task quality facts: %w", err)
	}
	return nil
}

func validateEvidence(proofs []EvidenceProof, partition string) error {
	seen := make(map[string]struct{}, len(proofs))
	for _, proof := range proofs {
		if !identityPattern.MatchString(proof.Kind) ||
			!identityPattern.MatchString(proof.Producer) ||
			!digestPattern.MatchString(proof.Digest) ||
			proof.RunPartition != partition {
			return fmt.Errorf("oracle evidence %q is invalid or unbound", proof.Kind)
		}
		if _, exists := seen[proof.Kind]; exists {
			return fmt.Errorf("duplicate oracle evidence %q", proof.Kind)
		}
		seen[proof.Kind] = struct{}{}
	}
	return nil
}

func (f RuntimeFacts) validate() error {
	if f.MailboxCount < 0 || f.MailboxCapacity < 0 ||
		f.BudgetUsed < 0 || f.BudgetLimit < 0 {
		return errors.New("counts and budgets cannot be negative")
	}
	if f.EvidenceAvailable {
		switch f.Phase {
		case "running", "parked", "terminal":
		default:
			return fmt.Errorf("phase %q is invalid", f.Phase)
		}
	}
	var previous uint64
	for _, event := range f.Events {
		if event.Sequence == 0 || event.Sequence <= previous ||
			!eventKindPattern.MatchString(event.Kind) ||
			!identityPattern.MatchString(event.Turn) ||
			!identityPattern.MatchString(event.Operation) {
			return errors.New("runtime event identity or order is invalid")
		}
		if event.Effect != "" && !identityPattern.MatchString(event.Effect) {
			return errors.New("runtime event effect identity is invalid")
		}
		if event.ReceiptHash != "" && !digestPattern.MatchString(event.ReceiptHash) {
			return errors.New("runtime receipt hash is invalid")
		}
		previous = event.Sequence
	}
	if err := f.Projection.validate(); err != nil {
		return err
	}
	return nil
}

func (f ProjectionFact) validate() error {
	if f.DuplicateItems < 0 || f.MissingItems < 0 {
		return errors.New("projection item counts cannot be negative")
	}
	return nil
}

func validateEffects(effects []EffectFact) error {
	seen := make(map[string]struct{}, len(effects))
	for _, effect := range effects {
		if !identityPattern.MatchString(effect.ID) {
			return errors.New("effect id is invalid")
		}
		if _, exists := seen[effect.ID]; exists {
			return fmt.Errorf("duplicate effect fact %q", effect.ID)
		}
		seen[effect.ID] = struct{}{}
		if effect.Claims < 0 || effect.Executions < 0 || effect.Results < 0 {
			return fmt.Errorf("effect %q counts cannot be negative", effect.ID)
		}
	}
	return nil
}

func (f WorkspaceFacts) validate() error {
	if f.EvidenceAvailable {
		if !digestPattern.MatchString(f.BeforeDigest) ||
			!digestPattern.MatchString(f.AfterDigest) {
			return errors.New("workspace digests are invalid")
		}
	}
	for _, values := range [][]string{
		f.ChangedPaths, f.AllowedPaths, f.PreexistingDirty, f.PreservedDirty,
	} {
		for _, value := range values {
			if !validRelativePath(value) {
				return fmt.Errorf("workspace path %q is invalid", value)
			}
		}
	}
	return nil
}

func (f VerificationFacts) validate() error {
	seen := make(map[string]struct{}, len(f.Commands))
	for _, command := range f.Commands {
		if !identityPattern.MatchString(command.ID) || !command.Status.Valid() {
			return errors.New("verification command identity or status is invalid")
		}
		if _, exists := seen[command.ID]; exists {
			return fmt.Errorf("duplicate verification command %q", command.ID)
		}
		seen[command.ID] = struct{}{}
		if command.Status == spec.StatusPassed && command.ExitCode != nil &&
			*command.ExitCode != 0 {
			return fmt.Errorf("passed verification %q has nonzero exit code", command.ID)
		}
	}
	return nil
}

func (f PersistenceFacts) validate() error {
	if !f.EvidenceAvailable {
		return nil
	}
	for name, value := range map[string]string{
		"event_digest":            f.EventDigest,
		"replay_digest":           f.ReplayDigest,
		"snapshot_digest":         f.SnapshotDigest,
		"rebuilt_digest":          f.RebuiltDigest,
		"terminal_digest":         f.TerminalDigest,
		"receipt_terminal_digest": f.ReceiptTerminalDigest,
	} {
		if !digestPattern.MatchString(value) {
			return fmt.Errorf("%s is invalid", name)
		}
	}
	if f.OutboxPublications < 0 {
		return errors.New("outbox publication count cannot be negative")
	}
	return nil
}

func (f HostFacts) validate() error {
	if f.DuplicateItems < 0 || f.MissingItems < 0 {
		return errors.New("host item counts cannot be negative")
	}
	return nil
}

func (f SecurityFacts) validate() error {
	if f.SecretLeaks < 0 || f.UnauthorizedEgress < 0 ||
		f.UnsafeLinks < 0 || f.PermissionBypass < 0 {
		return errors.New("security counts cannot be negative")
	}
	return nil
}

func (f ResourceFacts) validate() error {
	values := []int{
		f.ProcessesBefore, f.ProcessesAfter, f.GoroutinesBefore,
		f.GoroutinesAfter, f.GoroutineTolerance, f.FDsBefore, f.FDsAfter,
		f.FDTolerance, f.SubscribersBefore, f.SubscribersAfter,
		f.TemporaryPathsRemaining, f.QueuePeak, f.QueueCapacity,
	}
	for _, value := range values {
		if value < 0 {
			return errors.New("resource counts cannot be negative")
		}
	}
	if f.InputBytes < 0 || f.PersistedBytes < 0 ||
		f.MaxAmplificationMilli < 0 {
		return errors.New("resource byte and amplification values cannot be negative")
	}
	for _, ids := range [][]string{f.ProcessIDsBefore, f.ProcessIDsAfter} {
		seen := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			if !identityPattern.MatchString(id) {
				return errors.New("resource process identity is invalid")
			}
			if _, exists := seen[id]; exists {
				return errors.New("resource process identities contain a duplicate")
			}
			seen[id] = struct{}{}
		}
	}
	return nil
}

func (f TaskQualityFacts) validate() error {
	if f.Assertions < 0 || f.Passed < 0 || f.Passed > f.Assertions {
		return errors.New("task quality assertion counts are invalid")
	}
	return nil
}

func validRelativePath(value string) bool {
	if strings.TrimSpace(value) == "" || strings.HasPrefix(value, "/") ||
		value == ".." || strings.HasPrefix(value, "../") ||
		strings.Contains(value, "\\") {
		return false
	}
	parts := strings.Split(value, "/")
	return !slices.Contains(parts, "..")
}

func IsOracleID(value string) bool {
	return slices.Contains(AllIDs, value)
}

func (d Domain) Valid() bool {
	switch d {
	case DomainRuntimeState, DomainToolOrGuard,
		DomainPersistenceOrRecovery, DomainHostProjection,
		DomainPlatformEnvironment, DomainTaskQuality,
		DomainEvaluationHarness:
		return true
	default:
		return false
	}
}
