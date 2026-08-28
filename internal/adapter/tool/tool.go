package tool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	adaptercontent "github.com/fwtllh-png/CodeHelper/internal/adapter/content"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Call-shape failures are safe for the model to repair.
var (
	ErrUnknownTool     = errors.New("unknown tool")
	ErrToolUnavailable = errors.New("unavailable")
	ErrToolRevoked     = errors.New("tool revoked")
	ErrCatalogStale    = errors.New("tool catalog generation is stale")
	ErrToolLoadFailed  = errors.New("tool load failed")
	ErrCatalogLimit    = errors.New("tool catalog materialization limit exceeded")
	// ErrInvalidArguments marks arguments the caller can fix, as opposed to a
	// broken tool definition (schema that fails to compile).
	ErrInvalidArguments = errors.New("invalid tool arguments")
	// ErrPrecondition guarantees the refused call changed nothing.
	ErrPrecondition = errors.New("tool precondition not met")
)

type argumentError struct{ err error }

func (e argumentError) Error() string   { return e.err.Error() }
func (e argumentError) Unwrap() []error { return []error{ErrInvalidArguments, e.err} }

func invalidArguments(err error) error { return argumentError{err: err} }

type preconditionError struct{ err error }

func (e preconditionError) Error() string   { return e.err.Error() }
func (e preconditionError) Unwrap() []error { return []error{ErrPrecondition, e.err} }

// Precondition marks err as a refusal that changed nothing. Tools use it for
// checks they run before touching the workspace.
func Precondition(err error) error { return preconditionError{err: err} }

// RecoveryHint becomes model-visible only at the engine boundary.
type RecoveryHint struct {
	ErrorCategory  string
	RequiredAction string
	Path           string
	RetryOriginal  bool
	FailedChange   int
	MatchCount     int
	StartLine      int
	EndLine        int
	CurrentExcerpt string
	CandidatePaths []string
}

type recoveryHintError struct {
	err  error
	hint RecoveryHint
}

func (e recoveryHintError) Error() string { return e.err.Error() }
func (e recoveryHintError) Unwrap() error { return e.err }

func WithRecoveryHint(err error, hint RecoveryHint) error {
	if err == nil {
		return nil
	}
	return recoveryHintError{err: err, hint: hint}
}

func RecoveryHintFromError(err error) (RecoveryHint, bool) {
	var hinted recoveryHintError
	if !errors.As(err, &hinted) {
		return RecoveryHint{}, false
	}
	return hinted.hint, true
}

type Visibility string

const (
	VisibleModel    Visibility = "model"
	VisibleInternal Visibility = "internal"
	VisibleHidden   Visibility = "hidden"
)

type Capability string

const (
	CapabilityRead    Capability = "read"
	CapabilityWrite   Capability = "write"
	CapabilityProcess Capability = "process"
	CapabilityNetwork Capability = "network"
	CapabilityPlugin  Capability = "plugin"
)

type AccessMode string

const (
	AccessRead  AccessMode = "read"
	AccessWrite AccessMode = "write"
	AccessTree  AccessMode = "tree"
)

type ParallelPolicy string

const (
	ParallelConcurrent ParallelPolicy = "concurrent"
	ParallelSerial     ParallelPolicy = "serial"
)

type RepeatPolicy string

const (
	RepeatExecute        RepeatPolicy = "execute"
	RepeatReplaySameTurn RepeatPolicy = "replay_same_turn"
)

type SandboxRequirement string

const (
	SandboxNone   SandboxRequirement = "none"
	SandboxStrong SandboxRequirement = "strong"
)

type Availability string

const (
	AvailabilityAvailable   Availability = "available"
	AvailabilityUnavailable Availability = "unavailable"
	AvailabilityDeferred    Availability = "deferred"
)

type Alias struct {
	Name   string `json:"name"`
	Hidden bool   `json:"hidden"`
}

type ResourceTemplate struct {
	Kind   string     `json:"kind"`
	Field  string     `json:"field,omitempty"`
	ID     string     `json:"id,omitempty"`
	Access AccessMode `json:"access"`
	Tree   bool       `json:"tree,omitempty"`
	Glob   bool       `json:"glob,omitempty"`
}

type ResourceResolver struct {
	Templates  []ResourceTemplate `json:"templates,omitempty"`
	PatchField string             `json:"patch_field,omitempty"`
	// PathsField resolves exact write paths, never globs or directory trees.
	PathsField string `json:"paths_field,omitempty"`
	// ReadPathsField binds read-only verification coverage.
	ReadPathsField string `json:"read_paths_field,omitempty"`
	// TrustedHostPathField resolves one exact read-only path under either the
	// Workspace or its policy-owned private home.
	TrustedHostPathField string `json:"trusted_host_path_field,omitempty"`
	// ChangesField resolves transaction "path" and "to" entries.
	ChangesField        string `json:"changes_field,omitempty"`
	NetworkTargetsField string `json:"network_targets_field,omitempty"`
	// LoopbackField resolves an explicit local fixture-network grant.
	LoopbackField string `json:"loopback_field,omitempty"`
}

type DeferredLoading struct {
	Enabled bool `json:"enabled"`
}

type Descriptor struct {
	Name               string             `json:"name"`
	Description        string             `json:"description"`
	InputSchema        map[string]any     `json:"input_schema"`
	Visibility         Visibility         `json:"visibility"`
	Capability         Capability         `json:"capability"`
	ResourceResolver   ResourceResolver   `json:"resource_resolver"`
	AccessMode         AccessMode         `json:"access_mode"`
	ParallelPolicy     ParallelPolicy     `json:"parallel_policy"`
	RepeatPolicy       RepeatPolicy       `json:"repeat_policy,omitempty"`
	SandboxRequirement SandboxRequirement `json:"sandbox_requirement"`
	Aliases            []Alias            `json:"aliases,omitempty"`
	DeferredLoading    DeferredLoading    `json:"deferred_loading"`
	Availability       Availability       `json:"availability"`
	UnavailableReason  string             `json:"unavailable_reason,omitempty"`
}

type Resource struct {
	Kind         string     `json:"kind"`
	Path         string     `json:"path,omitempty"`
	ID           string     `json:"id,omitempty"`
	Access       AccessMode `json:"access"`
	Tree         bool       `json:"tree,omitempty"`
	Protocol     string     `json:"protocol,omitempty"`
	Port         uint16     `json:"port,omitempty"`
	Methods      []string   `json:"methods,omitempty"`
	AllowPrivate bool       `json:"allow_private,omitempty"`
}

func (r Resource) Key() string {
	return strings.Join([]string{
		r.Kind, r.Path, r.ID, string(r.Access), fmt.Sprint(r.Tree),
		r.Protocol, fmt.Sprint(r.Port), strings.Join(r.Methods, ","),
		fmt.Sprint(r.AllowPrivate),
	}, "\x00")
}

type Result struct {
	Content       string                           `json:"content"`
	IsError       bool                             `json:"is_error,omitempty"`
	Metadata      map[string]any                   `json:"metadata,omitempty"`
	Outcome       *Outcome                         `json:"outcome,omitempty"`
	Execution     *ExecutionReceipt                `json:"execution,omitempty"`
	Truncated     bool                             `json:"truncated,omitempty"`
	OriginalBytes int                              `json:"original_bytes,omitempty"`
	Handle        string                           `json:"handle,omitempty"`
	Admission     *adaptercontent.AdmissionReceipt `json:"admission,omitempty"`
	// Attachments are projected as bounded Provider image input after the tool
	// result. They are not serialized into the textual tool-result payload.
	Attachments []provider.Attachment `json:"-"`
}

const MetadataCompletionDeclaration = "completion_declaration"

// CompletionDeclaration binds completion to observed mutation state.
type CompletionDeclaration struct {
	Status              string   `json:"status"`
	Summary             string   `json:"summary"`
	OutputMode          string   `json:"output_mode,omitempty"`
	ChangedPaths        []string `json:"changed_paths"`
	VerificationCallIDs []string `json:"verification_call_ids"`
	PendingActions      []string `json:"pending_actions"`
	MutationRevision    uint64   `json:"mutation_revision,omitempty"`
	CallID              string   `json:"call_id,omitempty"`
}

// MetadataEvidence carries runtime-observed []EvidenceHit.
const MetadataEvidence = "evidence"

// Evidence hit kinds. They mirror the kinds the runtime's evidence ledger
// records; a producer that cannot tell which one applies should say nothing
// rather than guess a specific one.
const (
	EvidenceDefinition = "definition"
	EvidenceReference  = "reference"
	EvidenceTest       = "test"
	EvidenceConfig     = "config"
	EvidenceTextMatch  = "text_match"
)

// EvidenceHit is one classified hit. Paths are spelled the way the tool reported
// them to the model, so the two views can be lined up.
type EvidenceHit struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
	// Line is the 1-based line of the hit, zero when the hit is the whole file.
	Line int `json:"line,omitempty"`
	// Symbol is the declaration a symbol lookup matched.
	Symbol string `json:"symbol,omitempty"`
}

type Executor interface {
	Descriptor() Descriptor
	Execute(context.Context, json.RawMessage) (Result, error)
}

// EditPlan is a side-effect-free preview produced by a workspace writer.
// Guard binds its ID to one approval and requires an identical re-plan before
// execution, so the preview the host showed is the content that gets applied.
type EditPlan struct {
	ID    string         `json:"id"`
	Diff  string         `json:"diff"`
	Files []EditPlanFile `json:"files"`
}

type EditPlanFile struct {
	Path         string `json:"path"`
	Kind         string `json:"kind"`
	Before       string `json:"before,omitempty"`
	After        string `json:"after,omitempty"`
	BeforeExists bool   `json:"before_exists"`
	AfterExists  bool   `json:"after_exists"`
	BeforeDigest string `json:"before_digest"`
	AfterDigest  string `json:"after_digest"`
}

// EditPlanner is implemented by file writers that can fully compose their
// result without touching disk.
type EditPlanner interface {
	PlanEdit(context.Context, json.RawMessage) (EditPlan, error)
}

// ArgumentExpander lets a tool rewrite its arguments after schema normalization
// and before resource resolution. integrate_agent uses it to expand agent_id into
// the concrete file changes Guard must journal for turnDiff.
type ArgumentExpander interface {
	ExpandArguments(ctx context.Context, raw json.RawMessage) (json.RawMessage, error)
}

type ClaimFunc func(json.RawMessage) ([]string, error)

type registered struct {
	descriptor Descriptor
	external   ExternalDescriptor
	binding    TrustedBinding
	executor   Executor
	deferred   func() (Executor, error)
	backend    sandbox.Backend
	source     string
	revision   uint64
	state      CatalogEntryState
	payload    any
	token      uint64
	loading    bool
	wait       *sync.Cond
}

type catalogTombstone struct {
	source    string
	canonical string
	revision  uint64
}

type Registry struct {
	mu              sync.RWMutex
	tools           map[string]*registered
	aliases         map[string]string
	tombstones      map[string]catalogTombstone
	catalogID       string
	generation      uint64
	nextToken       uint64
	maxMaterialized int
	maxSchemaBytes  int
	claims          *Claims
	results         *ResultStore
	images          *ImageStore
	backend         sandbox.Backend
}

type Call struct {
	Name       string
	Arguments  json.RawMessage
	Authorized bool
}

func NewRegistry(claims *Claims, results *ResultStore) *Registry {
	if claims == nil {
		claims = NewClaims()
	}
	if results == nil {
		results = NewResultStore(32 << 10)
	}
	registry := &Registry{
		tools: make(map[string]*registered), aliases: make(map[string]string),
		tombstones:      make(map[string]catalogTombstone),
		catalogID:       nextCatalogID(),
		generation:      1,
		maxMaterialized: DefaultMaxMaterialized,
		maxSchemaBytes:  DefaultMaxMaterializedSchemaBytes,
		claims:          claims, results: results,
		images: newImageStore(results.store),
	}
	retrieval := &resultRetrieval{store: results}
	descriptor := retrieval.Descriptor()
	item := &registered{
		descriptor: descriptor, external: ExternalFromDescriptor(descriptor),
		binding:  TrustedBindingFromDescriptor(descriptor),
		executor: retrieval, source: "builtin:result_get",
		revision: 1, state: CatalogEntryEager, token: 1,
	}
	registry.nextToken = 1
	item.wait = sync.NewCond(&registry.mu)
	registry.tools[descriptor.Name] = item
	return registry
}

func (r *Registry) SetSandboxBackend(backend sandbox.Backend) {
	r.mu.Lock()
	r.backend = backend
	r.mu.Unlock()
}

func (r *Registry) SandboxBackend() sandbox.Backend {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.backend
}

func (r *Registry) Claims() *Claims {
	return r.claims
}

// Register retains the second parameter for source compatibility. Resource
// derivation is exclusively defined by Descriptor.ResourceResolver.
func (r *Registry) Register(executor Executor, _ ClaimFunc) error {
	if executor == nil {
		return errors.New("tool executor is required")
	}
	registration := NewRegistration(executor)
	return r.registerOne(nextLegacySource(executor.Descriptor().Name), registration)
}

func (r *Registry) RegisterTrusted(
	source string,
	registration Registration,
) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return errors.New("trusted tool source is required")
	}
	if !registration.explicit {
		return errors.New("trusted registration requires an explicit binding")
	}
	return r.registerOne(source, registration)
}

func (r *Registry) RegisterDeferred(descriptor Descriptor, loader func() (Executor, error)) error {
	if loader == nil {
		return errors.New("deferred tool loader is required")
	}
	registration := NewDeferredRegistration(descriptor, loader)
	return r.registerOne(nextLegacySource(descriptor.Name), registration)
}

type descriptorExecutor struct{ descriptor Descriptor }

func (e descriptorExecutor) Descriptor() Descriptor { return e.descriptor }
func (descriptorExecutor) Execute(context.Context, json.RawMessage) (Result, error) {
	return Result{}, errors.New("deferred tool is not loaded")
}

func (r *Registry) Resolve(name string) (string, Descriptor, Executor, error) {
	return r.resolve(name, nil)
}

func (r *Registry) ResolveBound(
	name string,
	binding CatalogBinding,
) (string, Descriptor, Executor, error) {
	if binding.CatalogID == "" {
		return r.Resolve(name)
	}
	if binding.Revision == 0 {
		return "", Descriptor{}, nil, fmt.Errorf(
			"%w %q: tool was not advertised in the sampled catalog",
			ErrUnknownTool, name,
		)
	}
	return r.resolve(name, &binding)
}

func (r *Registry) ResolveBoundRef(
	name string,
	binding CatalogBinding,
) (ToolRef, Descriptor, Executor, error) {
	canonical, descriptor, executor, err := r.ResolveBound(name, binding)
	if err != nil {
		return ToolRef{}, Descriptor{}, nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	item := r.tools[canonical]
	if item == nil || !reflect.DeepEqual(item.descriptor, descriptor) {
		return ToolRef{}, Descriptor{}, nil, fmt.Errorf(
			"%w for tool %q: execution authority changed",
			ErrCatalogStale,
			canonical,
		)
	}
	if binding.CatalogID != "" {
		if err := r.validateBindingLocked(canonical, item, &binding); err != nil {
			return ToolRef{}, Descriptor{}, nil, err
		}
	}
	generation := r.generation
	if binding.Generation != 0 {
		generation = binding.Generation
	}
	ref := ToolRef{
		Name: canonical, Source: item.source,
		CatalogID: r.catalogID, Generation: generation,
		Revision: item.revision, Authority: item.token,
	}
	if err := ref.Validate(); err != nil {
		return ToolRef{}, Descriptor{}, nil, err
	}
	return ref, descriptor, executor, nil
}

func (r *Registry) ResolveTrustedBinding(
	ref ToolRef,
) (TrustedBinding, error) {
	if err := ref.Validate(); err != nil {
		return TrustedBinding{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	item := r.tools[ref.Name]
	if item == nil || item.source != ref.Source ||
		item.revision != ref.Revision ||
		item.token != ref.Authority ||
		r.catalogID != ref.CatalogID {
		return TrustedBinding{}, fmt.Errorf(
			"%w for tool %q: trusted binding changed",
			ErrCatalogStale, ref.Name,
		)
	}
	return cloneTrustedBinding(item.binding), nil
}

// ResolveCatalogToolID validates a sampled binding without materializing or
// executing the tool and returns the stable identity used by Session
// allowlists.
func (r *Registry) ResolveCatalogToolID(
	name string,
	binding CatalogBinding,
) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if canonical := r.aliases[name]; canonical != "" {
		name = canonical
	}
	item := r.tools[name]
	if item == nil {
		if tombstone, revoked := r.tombstones[name]; revoked {
			return "", fmt.Errorf(
				"%w %q (source=%s revision=%d)",
				ErrToolRevoked, tombstone.canonical, tombstone.source, tombstone.revision,
			)
		}
		return "", fmt.Errorf("%w %q", ErrUnknownTool, name)
	}
	if binding.CatalogID != "" {
		if err := r.validateBindingLocked(name, item, &binding); err != nil {
			return "", err
		}
	}
	return CatalogToolID(name, item.source), nil
}

func (r *Registry) resolve(
	name string,
	binding *CatalogBinding,
) (string, Descriptor, Executor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if canonical := r.aliases[name]; canonical != "" {
		name = canonical
	}
	item := r.tools[name]
	if item == nil {
		if tombstone, revoked := r.tombstones[name]; revoked {
			return "", Descriptor{}, nil, fmt.Errorf(
				"%w %q (source=%s revision=%d)",
				ErrToolRevoked, tombstone.canonical, tombstone.source, tombstone.revision,
			)
		}
		return "", Descriptor{}, nil, fmt.Errorf("%w %q", ErrUnknownTool, name)
	}
	if err := r.validateBindingLocked(name, item, binding); err != nil {
		return "", Descriptor{}, nil, err
	}
	for item.loading {
		item.wait.Wait()
	}
	if r.tools[name] != item {
		if _, revoked := r.tombstones[name]; revoked {
			return "", Descriptor{}, nil, fmt.Errorf("%w %q", ErrToolRevoked, name)
		}
		return "", Descriptor{}, nil, fmt.Errorf("%w for tool %q", ErrCatalogStale, name)
	}
	if err := r.validateBindingLocked(name, item, binding); err != nil {
		return "", Descriptor{}, nil, err
	}
	if err := r.refreshAvailabilityLocked(item); err != nil {
		return name, Descriptor{}, nil, err
	}
	if err := r.validateBindingLocked(name, item, binding); err != nil {
		return "", Descriptor{}, nil, err
	}
	descriptor := cloneDescriptor(item.descriptor)
	if descriptor.Availability == AvailabilityUnavailable {
		return name, descriptor, nil, fmt.Errorf(
			"tool %q is %w: %s", name, ErrToolUnavailable, descriptor.UnavailableReason,
		)
	}
	if item.deferred != nil {
		if err := r.checkMaterializeLimitLocked(item); err != nil {
			return name, descriptor, nil, err
		}
		item.loading = true
		loader := item.deferred
		r.mu.Unlock()
		executor, err := loader()
		r.mu.Lock()
		item.loading = false
		item.wait.Broadcast()
		if r.tools[name] != item {
			if _, revoked := r.tombstones[name]; revoked {
				return "", Descriptor{}, nil, fmt.Errorf("%w %q", ErrToolRevoked, name)
			}
			return "", Descriptor{}, nil, fmt.Errorf("%w for tool %q", ErrCatalogStale, name)
		}
		if err != nil {
			descriptor.Availability = AvailabilityUnavailable
			descriptor.UnavailableReason = err.Error()
			descriptor.DeferredLoading.Enabled = false
			item.descriptor = cloneDescriptor(descriptor)
			item.executor = descriptorExecutor{descriptor: descriptor}
			item.deferred = nil
			item.state = CatalogEntryUnavailable
			item.revision++
			r.generation++
			return name, descriptor, nil, fmt.Errorf(
				"%w for %q: %v", ErrToolLoadFailed, name, err,
			)
		}
		if executor == nil {
			return name, descriptor, nil, fmt.Errorf("deferred tool %q loaded a nil executor", name)
		}
		loaded := executor.Descriptor()
		if provider, ok := executor.(TrustedBindingProvider); ok {
			loadedBinding := provider.TrustedBinding()
			if !reflect.DeepEqual(item.binding, loadedBinding) {
				return name, descriptor, nil, fmt.Errorf(
					"%w for %q: loader changed frozen trusted binding",
					ErrToolLoadFailed, name,
				)
			}
			loaded = ApplyTrustedBinding(loaded, loadedBinding)
		}
		if loaded.Name != name {
			return name, descriptor, nil, fmt.Errorf("deferred tool %q loaded as %q", name, loaded.Name)
		}
		if err := validateDescriptor(loaded); err != nil {
			return name, descriptor, nil, err
		}
		if !sameAliases(descriptor.Aliases, loaded.Aliases) {
			return name, descriptor, nil, fmt.Errorf("deferred tool %q changed aliases while loading", name)
		}
		expected := cloneDescriptor(descriptor)
		expected.Availability = loaded.Availability
		expected.UnavailableReason = loaded.UnavailableReason
		expected.DeferredLoading = loaded.DeferredLoading
		if !reflect.DeepEqual(expected, loaded) {
			return name, descriptor, nil, fmt.Errorf(
				"%w for %q: loader changed frozen descriptor authority or schema",
				ErrToolLoadFailed, name,
			)
		}
		item.executor = executor
		item.deferred = nil
		item.descriptor = cloneDescriptor(loaded)
		item.state = CatalogEntryMaterialized
		item.revision++
		r.generation++
		descriptor = cloneDescriptor(loaded)
	}
	if err := r.validateBindingLocked(name, item, binding); err != nil {
		return "", Descriptor{}, nil, err
	}
	return name, descriptor, item.executor, nil
}

func (r *Registry) validateBindingLocked(
	name string,
	item *registered,
	binding *CatalogBinding,
) error {
	if binding == nil {
		return nil
	}
	if binding.CatalogID != r.catalogID {
		return fmt.Errorf("%w for tool %q: catalog id changed", ErrCatalogStale, name)
	}
	if binding.Authority == 0 || item.token != binding.Authority {
		return fmt.Errorf("%w for tool %q: execution authority changed", ErrCatalogStale, name)
	}
	if item.revision != binding.Revision {
		return fmt.Errorf(
			"%w for tool %q: sampled revision=%d current=%d",
			ErrCatalogStale, name, binding.Revision, item.revision,
		)
	}
	return nil
}

func (r *Registry) InjectedSandbox(name string) sandbox.Backend {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if canonical := r.aliases[name]; canonical != "" {
		name = canonical
	}
	if item := r.tools[name]; item != nil {
		return item.backend
	}
	return nil
}

func (r *Registry) Descriptors(visibility Visibility) []Descriptor {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []Descriptor
	for _, item := range r.tools {
		// Preserve self-degrading tools (for example a failed repository index)
		// without letting their live Descriptor mutate authority or schema.
		_ = r.refreshAvailabilityLocked(item)
		descriptor := cloneDescriptor(item.descriptor)
		if descriptor.Visibility == visibility {
			result = append(result, descriptor)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func (r *Registry) Execute(ctx context.Context, call Call) (Result, error) {
	name, descriptor, executor, err := r.Resolve(call.Name)
	if err != nil {
		return Result{}, err
	}
	if !call.Authorized {
		return Result{}, fmt.Errorf("tool %q is not authorized", name)
	}
	arguments := RepairArguments(call.Arguments)
	if err := ValidateArguments(descriptor.InputSchema, arguments); err != nil {
		return Result{}, fmt.Errorf("tool %q arguments: %w", name, err)
	}
	result, _, err := r.ExecutePreparedOutcome(ctx, name, arguments, executor)
	return result, err
}

func (r *Registry) ExecutePreparedOutcome(
	ctx context.Context,
	canonicalName string,
	arguments json.RawMessage,
	executor Executor,
) (Result, Outcome, error) {
	var (
		result  Result
		outcome Outcome
		err     error
	)
	if typed, ok := executor.(OutcomeExecutor); ok {
		result, outcome, err = typed.ExecuteOutcome(ctx, arguments)
	} else {
		result, err = executor.Execute(ctx, arguments)
		outcome = OutcomeFromResult(result)
	}
	if result.Outcome != nil {
		outcome = *CloneOutcome(result.Outcome)
	} else {
		if outcome.Status == "" {
			outcome = OutcomeFromResult(result)
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				outcome.Status = OutcomeCanceled
			} else {
				outcome.Status = OutcomeFailed
			}
		}
		result.Outcome = CloneOutcome(&outcome)
	}
	if outcome.Status == "" {
		outcome.Status = OutcomeFromResult(result).Status
		result.Outcome.Status = outcome.Status
	}
	if err != nil {
		return result, outcome, err
	}
	if canonicalName == "result_get" || canonicalName == "handle_read" {
		return result, outcome, nil
	}
	return r.results.RouteFor(canonicalName, result), outcome, nil
}

// PruneResultSurface stores the full result and returns a deterministic,
// retrieval-backed model projection.
func (r *Registry) PruneResultSurface(
	name string,
	result Result,
	maxBytes int,
) (Result, bool) {
	return r.results.PruneSurface(name, result, maxBytes)
}

func (r *Registry) AdmitResult(
	name string,
	result Result,
) (Result, adaptercontent.AdmissionReceipt) {
	return r.results.Admit(name, result)
}

func (r *Registry) AdmitResultWithin(
	name string,
	result Result,
	maxTokens uint64,
) (Result, adaptercontent.AdmissionReceipt) {
	return r.results.AdmitWithin(name, result, maxTokens)
}

func (r *Registry) ResultTokenCapacity() uint64 {
	return r.results.TokenCapacity()
}

func RepairArguments(raw json.RawMessage) json.RawMessage {
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return json.RawMessage(`{}`)
	}
	if strings.HasPrefix(value, "```") && strings.HasSuffix(value, "```") {
		value = strings.TrimPrefix(value, "```")
		value = strings.TrimPrefix(value, "json")
		value = strings.TrimSpace(strings.TrimSuffix(value, "```"))
	}
	return json.RawMessage(value)
}

func validateDescriptor(descriptor Descriptor) error {
	if descriptor.Name == "" || strings.ContainsAny(descriptor.Name, " \t\n") {
		return errors.New("tool name must be non-empty and contain no whitespace")
	}
	if descriptor.Description == "" {
		return fmt.Errorf("tool %q description is required", descriptor.Name)
	}
	if descriptor.Visibility != VisibleModel && descriptor.Visibility != VisibleInternal && descriptor.Visibility != VisibleHidden {
		return fmt.Errorf("tool %q has invalid visibility", descriptor.Name)
	}
	if descriptor.InputSchema["type"] != "object" {
		return fmt.Errorf("tool %q input schema must describe an object", descriptor.Name)
	}
	if descriptor.Capability != CapabilityRead && descriptor.Capability != CapabilityWrite &&
		descriptor.Capability != CapabilityProcess && descriptor.Capability != CapabilityNetwork &&
		descriptor.Capability != CapabilityPlugin {
		return fmt.Errorf("tool %q has invalid capability", descriptor.Name)
	}
	if descriptor.AccessMode != AccessRead && descriptor.AccessMode != AccessWrite &&
		descriptor.AccessMode != AccessTree {
		return fmt.Errorf("tool %q has invalid access mode", descriptor.Name)
	}
	if descriptor.ParallelPolicy != ParallelConcurrent && descriptor.ParallelPolicy != ParallelSerial {
		return fmt.Errorf("tool %q has invalid parallel policy", descriptor.Name)
	}
	if descriptor.RepeatPolicy != "" &&
		descriptor.RepeatPolicy != RepeatExecute &&
		descriptor.RepeatPolicy != RepeatReplaySameTurn {
		return fmt.Errorf("tool %q has invalid repeat policy", descriptor.Name)
	}
	if descriptor.SandboxRequirement != SandboxNone &&
		descriptor.SandboxRequirement != SandboxStrong {
		return fmt.Errorf("tool %q has invalid sandbox requirement", descriptor.Name)
	}
	if descriptor.Availability != AvailabilityAvailable &&
		descriptor.Availability != AvailabilityUnavailable &&
		descriptor.Availability != AvailabilityDeferred {
		return fmt.Errorf("tool %q has invalid availability", descriptor.Name)
	}
	if descriptor.Availability == AvailabilityUnavailable && descriptor.UnavailableReason == "" {
		return fmt.Errorf("tool %q unavailable reason is required", descriptor.Name)
	}
	aliases := make(map[string]struct{}, len(descriptor.Aliases))
	for _, alias := range descriptor.Aliases {
		if alias.Name == "" || alias.Name == descriptor.Name || !alias.Hidden {
			return fmt.Errorf("tool %q aliases must be non-empty hidden compatibility names", descriptor.Name)
		}
		if _, exists := aliases[alias.Name]; exists {
			return fmt.Errorf("tool %q has duplicate alias %q", descriptor.Name, alias.Name)
		}
		aliases[alias.Name] = struct{}{}
	}
	for _, template := range descriptor.ResourceResolver.Templates {
		if template.Kind == "" || (template.Field == "" && template.ID == "") ||
			(template.Access != AccessRead && template.Access != AccessWrite) {
			return fmt.Errorf("tool %q has invalid resource template", descriptor.Name)
		}
	}
	return nil
}

// ValidateDescriptor checks the stable registry contract without registering
// an executor. Construction helpers use it to fail before wiring a catalog.
func ValidateDescriptor(descriptor Descriptor) error {
	return validateDescriptor(descriptor)
}

func ValidateArguments(schema map[string]any, raw json.RawMessage) error {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return invalidArguments(fmt.Errorf("invalid JSON object: %w", err))
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return invalidArguments(errors.New("multiple JSON values"))
	}
	compiler := jsonschema.NewCompiler()
	data, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("encode schema: %w", err)
	}
	var schemaValue any
	if err := json.Unmarshal(data, &schemaValue); err != nil {
		return fmt.Errorf("decode schema: %w", err)
	}
	if err := compiler.AddResource("tool-schema.json", schemaValue); err != nil {
		return fmt.Errorf("compile schema resource: %w", err)
	}
	compiled, err := compiler.Compile("tool-schema.json")
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	if err := compiled.Validate(value); err != nil {
		return invalidArguments(err)
	}
	return nil
}

func NormalizeArguments(schema map[string]any, raw json.RawMessage) (json.RawMessage, error) {
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, invalidArguments(fmt.Errorf("invalid JSON object: %w", err))
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, invalidArguments(errors.New("multiple JSON values"))
	}
	applyDefaults(value, schema)
	normalized, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if err := ValidateArguments(schema, normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func applyDefaults(value map[string]any, schema map[string]any) {
	properties, _ := schema["properties"].(map[string]any)
	for name, rawDefinition := range properties {
		definition, ok := rawDefinition.(map[string]any)
		if !ok {
			continue
		}
		current, exists := value[name]
		if !exists {
			if defaultValue, hasDefault := definition["default"]; hasDefault {
				value[name] = cloneJSONValue(defaultValue)
			}
			continue
		}
		if object, ok := current.(map[string]any); ok {
			applyDefaults(object, definition)
		}
		if items, ok := current.([]any); ok {
			itemSchema, _ := definition["items"].(map[string]any)
			for _, item := range items {
				if object, ok := item.(map[string]any); ok {
					applyDefaults(object, itemSchema)
				}
			}
		}
	}
}

func cloneJSONValue(value any) any {
	data, _ := json.Marshal(value)
	var cloned any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	_ = decoder.Decode(&cloned)
	return cloned
}

type ResultStore struct {
	maxInline    int
	retrievalCap int
	store        contentstore.Store
}

type storedResult struct {
	Content   string            `json:"content"`
	IsError   bool              `json:"is_error"`
	Metadata  map[string]any    `json:"metadata,omitempty"`
	Outcome   *Outcome          `json:"outcome,omitempty"`
	Execution *ExecutionReceipt `json:"execution,omitempty"`
}

func NewResultStore(maxInline int) *ResultStore {
	return NewResultStoreWithStore(maxInline, contentstore.NewMemory(contentstore.Options{}))
}

func NewResultStoreWithStore(maxInline int, store contentstore.Store) *ResultStore {
	if maxInline <= 0 {
		maxInline = 32 << 10
	}
	if store == nil {
		store = contentstore.NewMemory(contentstore.Options{})
	}
	retrievalCap := min(maxInline, 32<<10)
	return &ResultStore{
		maxInline: maxInline, retrievalCap: retrievalCap, store: store,
	}
}

func (s *ResultStore) Route(result Result) Result {
	return s.RouteFor("", result)
}

func (s *ResultStore) TokenCapacity() uint64 {
	return uint64((s.maxInline + 3) / 4)
}

func (s *ResultStore) RouteFor(name string, result Result) Result {
	admitted, _ := s.Admit(name, result)
	return admitted
}

func (s *ResultStore) Admit(
	name string,
	result Result,
) (Result, adaptercontent.AdmissionReceipt) {
	return s.AdmitWithin(name, result, 0)
}

func (s *ResultStore) AdmitWithin(
	name string,
	result Result,
	maxTokens uint64,
) (Result, adaptercontent.AdmissionReceipt) {
	if s.validAdmission(result) &&
		(maxTokens == 0 || result.Admission.TokenLimit <= maxTokens) {
		result.Admission = adaptercontent.CloneAdmissionReceipt(result.Admission)
		return result, *result.Admission
	}
	result.Admission = nil
	retrieval := name == "result_get" || name == "handle_read"
	if !retrieval && result.Truncated && result.Handle != "" {
		if stored, ok := s.getResult(result.Handle); ok {
			result = Result{
				Content: stored.Content, IsError: stored.IsError,
				Metadata:  cloneMetadata(stored.Metadata),
				Outcome:   CloneOutcome(stored.Outcome),
				Execution: CloneExecutionReceipt(stored.Execution),
			}
		}
	}
	original := result.Content
	originalBytes := len(original)
	originalTokens := estimateResultTokens(original)
	limit, kind, tokens := s.projectionLimit(name, original, maxTokens)
	receipt := adaptercontent.AdmissionReceipt{
		Kind: kind, Reason: "inline", Digest: resultDigest(original),
		OriginalBytes: originalBytes, RetainedBytes: originalBytes,
		OriginalTokens: originalTokens, RetainedTokens: originalTokens,
		TokenLimit: tokens,
	}
	result.OriginalBytes = originalBytes
	if originalTokens <= tokens && len(original) <= limit {
		result.Admission = &receipt
		return result, receipt
	}
	stored := storedResult{
		Content: original, IsError: result.IsError,
		Metadata:  cloneMetadata(result.Metadata),
		Outcome:   CloneOutcome(result.Outcome),
		Execution: CloneExecutionReceipt(result.Execution),
	}
	data, err := json.Marshal(stored)
	if err != nil {
		result = projectionFailure(result, limit, err)
		receipt.Reason = "store_failure"
		receipt.Truncated = true
		receipt.RetainedBytes = len(result.Content)
		receipt.RetainedTokens = estimateResultTokens(result.Content)
		result.Admission = &receipt
		return result, receipt
	}
	handle := contentstore.StableHandle("result", data)
	if err := s.store.Put(context.Background(), handle, data); err != nil {
		result = projectionFailure(result, limit, err)
		receipt.Reason = "store_failure"
		receipt.Truncated = true
		receipt.RetainedBytes = len(result.Content)
		receipt.RetainedTokens = estimateResultTokens(result.Content)
		result.Admission = &receipt
		return result, receipt
	}
	result.Content = fitTruncationNotice(original, handle, limit, tokens)
	result.Truncated = true
	result.Handle = handle
	EnsureOutcomeFacts(&result).ResultHandle = handle
	if result.Metadata == nil {
		result.Metadata = map[string]any{}
	} else {
		result.Metadata = cloneMetadata(result.Metadata)
	}
	result.Metadata["original_bytes"] = result.OriginalBytes
	result.Metadata["truncated"] = true
	result.Metadata["handle"] = handle
	result.Metadata["projection_kind"] = kind
	result.Metadata["projection_tokens"] = tokens
	receipt.Reason = "token_limit"
	receipt.Handle = handle
	receipt.Truncated = true
	receipt.RetainedBytes = len(result.Content)
	receipt.RetainedTokens = estimateResultTokens(result.Content)
	result.Admission = &receipt
	return result, receipt
}

func (s *ResultStore) PruneSurface(
	name string,
	result Result,
	maxBytes int,
) (Result, bool) {
	if name == "result_get" || name == "handle_read" || maxBytes <= 0 {
		return result, false
	}
	full := storedResult{
		Content: result.Content, IsError: result.IsError,
		Metadata:  cloneMetadata(result.Metadata),
		Outcome:   CloneOutcome(result.Outcome),
		Execution: CloneExecutionReceipt(result.Execution),
	}
	if result.Handle != "" {
		stored, ok := s.getResult(result.Handle)
		if !ok {
			return result, false
		}
		full = stored
	}
	if len(full.Content) <= maxBytes {
		return result, false
	}
	data, err := json.Marshal(full)
	if err != nil {
		return result, false
	}
	handle := result.Handle
	if handle == "" {
		handle = contentstore.StableHandle("result", data)
		if err := s.store.Put(context.Background(), handle, data); err != nil {
			return result, false
		}
	}
	headBytes := maxBytes * 3 / 4
	tailBytes := maxBytes - headBytes
	head, _ := boundedSlice(full.Content, 0, headBytes)
	tail, _ := boundedSlice(full.Content, len(full.Content)-tailBytes, tailBytes)
	result.Content = SurfaceTruncationNotice(
		len(full.Content),
		handle,
		head,
		tail,
	)
	result.IsError = full.IsError
	result.Truncated = true
	result.OriginalBytes = len(full.Content)
	result.Handle = handle
	result.Metadata = cloneMetadata(full.Metadata)
	result.Outcome = CloneOutcome(full.Outcome)
	result.Execution = CloneExecutionReceipt(full.Execution)
	if result.Metadata == nil {
		result.Metadata = map[string]any{}
	}
	result.Metadata["original_bytes"] = result.OriginalBytes
	result.Metadata["truncated"] = true
	result.Metadata["handle"] = handle
	result.Metadata["projection_kind"] = "context_surface"
	retainedTokens := estimateResultTokens(result.Content)
	result.Admission = &adaptercontent.AdmissionReceipt{
		Kind: "context_surface", Reason: "pressure_limit",
		Digest: resultDigest(full.Content), Handle: handle,
		OriginalBytes: len(full.Content), RetainedBytes: len(result.Content),
		OriginalTokens: estimateResultTokens(full.Content),
		RetainedTokens: retainedTokens,
		TokenLimit: max(
			retainedTokens,
			uint64(max(1, (maxBytes+3)/4)),
		),
		Truncated: true,
	}
	return result, true
}

func (s *ResultStore) projectionLimit(
	name, content string,
	maxTokens uint64,
) (int, string, uint64) {
	kind := "generic"
	switch {
	case name == "result_get" || name == "handle_read":
		kind = "retrieval"
	case name == "spawn_agent" || name == "send_input" ||
		name == "wait_agent" || name == "close_agent":
		kind = "structured"
	case name == "file_read" || name == "file_list" || name == "shell_read" ||
		strings.HasPrefix(name, "search_") || strings.HasPrefix(name, "git_"):
		kind = "read"
	case name == "skills.read" || name == "skills.list" ||
		name == "skills_read" || name == "skills_list":
		kind = "skill"
	case strings.HasPrefix(name, "quality_"):
		kind = "test"
	case name == "exec_command" || name == "write_stdin":
		kind = "build"
	}
	storeTokens := s.TokenCapacity()
	if maxTokens == 0 || maxTokens > storeTokens {
		maxTokens = storeTokens
	}
	return min(s.maxInline, len(content), int(maxTokens*4)), kind, maxTokens
}

func estimateResultTokens(value string) uint64 {
	if value == "" {
		return 0
	}
	return uint64((utf8.RuneCountInString(value) + 3) / 4)
}

func (s *ResultStore) validAdmission(result Result) bool {
	receipt := result.Admission
	if receipt == nil || receipt.TokenLimit == 0 ||
		receipt.TokenLimit > uint64((s.maxInline+3)/4) ||
		receipt.RetainedBytes != len(result.Content) ||
		receipt.RetainedTokens != estimateResultTokens(result.Content) ||
		receipt.RetainedTokens > receipt.TokenLimit {
		return false
	}
	if !receipt.Truncated {
		return receipt.Handle == "" &&
			receipt.OriginalBytes == receipt.RetainedBytes &&
			receipt.OriginalTokens == receipt.RetainedTokens &&
			receipt.Digest == resultDigest(result.Content)
	}
	if receipt.Handle == "" || result.Handle != receipt.Handle {
		return false
	}
	stored, ok := s.getResult(receipt.Handle)
	return ok &&
		receipt.Digest == resultDigest(stored.Content) &&
		receipt.OriginalBytes == len(stored.Content) &&
		receipt.OriginalTokens == estimateResultTokens(stored.Content)
}

func resultDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func fitTruncationNotice(
	original string,
	handle string,
	maxBodyBytes int,
	tokenLimit uint64,
) string {
	low, high := 0, min(len(original), maxBodyBytes)
	best := TruncationNotice(len(original), handle, "")
	for low <= high {
		middle := low + (high-low)/2
		body, _ := boundedSlice(original, 0, middle)
		candidate := TruncationNotice(len(original), handle, body)
		if estimateResultTokens(candidate) <= tokenLimit {
			best = candidate
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	return best
}

func projectionFailure(result Result, limit int, err error) Result {
	body, _ := boundedSlice(result.Content, 0, limit)
	result.Content = TruncationNotice(result.OriginalBytes, "", body)
	result.Truncated, result.IsError = true, true
	result.Metadata = map[string]any{
		"result_store_error": err.Error(),
		"original_bytes":     result.OriginalBytes, "truncated": true,
	}
	return result
}

func ModelResult(name string, result Result) Result {
	var processSession *ProcessSessionFact
	processTool := name == "exec_command" || name == "write_stdin"
	if result.Outcome != nil && result.Outcome.Facts != nil {
		processSession = result.Outcome.Facts.ProcessSession
	}
	if result.Outcome != nil && result.Outcome.Facts != nil &&
		len(result.Outcome.Facts.Diagnostics) != 0 {
		result.Metadata = cloneMetadata(result.Metadata)
		result.Metadata["diagnostics"] = append(
			[]diagnostics.Receipt(nil),
			result.Outcome.Facts.Diagnostics...,
		)
	}
	result.Admission = nil
	result.Outcome = nil
	result.Execution = nil
	if name == "result_get" || name == "handle_read" ||
		(result.Metadata == nil && processSession == nil) {
		return result
	}
	metadata := make(map[string]any)
	for key, value := range result.Metadata {
		switch key {
		case "error_category", "required_action", "retry_original", "retryable",
			"fatal", "handle", "original_bytes", "truncated",
			"completion_declaration_accepted", "completion_declaration_rejection",
			"completion_declaration_error", "verification_evidence_accepted",
			"verification_evidence_rejection", "replayed_from_call_id",
			"citations", "diagnostics":
			metadata[key] = value
		case "session_id", "cursor", "running", "exit_code", "timed_out",
			"tty", "archived", "pending_bytes", "omitted_bytes":
			// Tool Results are admitted and projected again before every
			// sample. Preserve only the prior controlled process projection
			// when the internal Outcome has already been removed.
			if processTool && processSession == nil {
				metadata[key] = value
			}
		}
	}
	if processTool && processSession != nil {
		metadata["cursor"] = processSession.Cursor
		metadata["running"] = processSession.Running
		metadata["exit_code"] = processSession.ExitCode
		metadata["timed_out"] = processSession.TimedOut
		metadata["tty"] = processSession.TTY
		if processSession.SessionID != "" {
			metadata["session_id"] = processSession.SessionID
		}
		if processSession.Archived {
			metadata["archived"] = true
		}
		if processSession.PendingBytes != 0 {
			metadata["pending_bytes"] = processSession.PendingBytes
		}
		if processSession.OmittedBytes != 0 {
			metadata["omitted_bytes"] = processSession.OmittedBytes
		}
	}
	result.Metadata = metadata
	if len(metadata) == 0 {
		result.Metadata = nil
	}
	return result
}

// TruncationNotice preserves explicit truncation and retrieval instructions.
func TruncationNotice(originalBytes int, handle, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Warning: truncated output (original bytes: %d)", originalBytes)
	if handle != "" {
		fmt.Fprintf(&b, ". Use result_get with handle %q to page the full result", handle)
	}
	b.WriteString(".\n\n")
	b.WriteString(body)
	return b.String()
}

func SurfaceTruncationNotice(
	originalBytes int,
	handle, head, tail string,
) string {
	var b strings.Builder
	fmt.Fprintf(
		&b,
		"Warning: pruned tool result (original bytes: %d). "+
			"Use result_get with handle %q to page the full result.\n\n",
		originalBytes,
		handle,
	)
	b.WriteString(head)
	b.WriteString("\n\n... pruned middle ...\n\n")
	b.WriteString(tail)
	return b.String()
}

func (s *ResultStore) Get(handle string) (string, bool) {
	value, exists := s.getResult(handle)
	return value.Content, exists
}

func (s *ResultStore) getResult(handle string) (storedResult, bool) {
	data, err := s.store.Get(context.Background(), handle)
	if err != nil {
		return storedResult{}, false
	}
	var value storedResult
	if json.Unmarshal(data, &value) != nil {
		return storedResult{}, false
	}
	value.Metadata = cloneMetadata(value.Metadata)
	return value, true
}

func cloneMetadata(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	target := make(map[string]any, len(source))
	maps.Copy(target, source)
	return target
}

type resultRetrieval struct {
	store *ResultStore
}

func (*resultRetrieval) Descriptor() Descriptor {
	return Descriptor{
		Name:        "result_get",
		Description: "Retrieve a bounded excerpt or metadata using the exact result_* handle from a truncation notice; tool call IDs are not handles",
		Visibility:  VisibleModel,
		Capability:  CapabilityRead, AccessMode: AccessRead,
		ParallelPolicy: ParallelConcurrent, SandboxRequirement: SandboxNone,
		Availability: AvailabilityAvailable,
		ResourceResolver: ResourceResolver{Templates: []ResourceTemplate{{
			Kind: "result", Field: "handle", Access: AccessRead,
		}}},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"handle":     map[string]any{"type": "string", "minLength": float64(1)},
				"mode":       map[string]any{"type": "string", "enum": []any{"metadata", "summary", "head", "tail", "lines", "query", "bytes"}},
				"start_line": map[string]any{"type": "integer"},
				"max_lines":  map[string]any{"type": "integer"},
				"query":      map[string]any{"type": "string"},
				"offset":     map[string]any{"type": "integer"},
				"max_bytes":  map[string]any{"type": "integer"},
			},
			"required":             []string{"handle"},
			"additionalProperties": false,
		},
	}
}

func (t *resultRetrieval) Execute(_ context.Context, raw json.RawMessage) (Result, error) {
	var input struct {
		Handle    string `json:"handle"`
		Mode      string `json:"mode"`
		StartLine int    `json:"start_line"`
		MaxLines  int    `json:"max_lines"`
		Query     string `json:"query"`
		Offset    int    `json:"offset"`
		MaxBytes  int    `json:"max_bytes"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return Result{}, err
	}
	value, exists := t.store.getResult(input.Handle)
	if !exists {
		return Result{}, Precondition(WithRecoveryHint(
			errors.New("result handle not found"),
			RecoveryHint{
				ErrorCategory:  "result_handle_not_found",
				RequiredAction: "use_advertised_result_handle",
				RetryOriginal:  false,
			},
		))
	}
	if input.Mode == "" {
		input.Mode = "summary"
	}
	if input.Offset < 0 || input.StartLine < 0 || input.MaxLines < 0 || input.MaxBytes < 0 {
		return Result{}, errors.New("offset and limits must not be negative")
	}
	if input.Mode == "query" && input.Query == "" {
		return Result{}, errors.New("query mode requires query")
	}

	limit := t.store.retrievalCap
	if input.MaxBytes > 0 {
		limit = min(limit, input.MaxBytes)
	}
	metadata := cloneMetadata(value.Metadata)
	if metadata == nil {
		metadata = make(map[string]any)
	}
	metadata["mode"] = input.Mode
	metadata["total_bytes"] = len(value.Content)
	metadata["total_lines"] = countLines(value.Content)
	metadata["hard_cap_bytes"] = t.store.retrievalCap

	var excerpt string
	var more bool
	switch input.Mode {
	case "metadata":
	case "summary":
		excerpt, more = summarizeResult(value.Content, limit)
	case "head":
		excerpt, more = boundedSlice(value.Content, 0, limit)
		if more {
			metadata["next_offset"] = len(excerpt)
		}
	case "tail":
		start := max(0, len(value.Content)-limit)
		excerpt = validSuffix(value.Content, start)
		more = start > 0
		if more {
			metadata["previous_offset"] = start
		}
	case "bytes":
		excerpt, more = boundedSlice(value.Content, input.Offset, limit)
		metadata["offset"] = min(input.Offset, len(value.Content))
		if more {
			metadata["next_offset"] = min(len(value.Content), input.Offset+len(excerpt))
		}
	case "lines":
		startLine := input.StartLine
		if startLine == 0 {
			startLine = 1
		}
		var nextLine, nextOffset int
		excerpt, more, nextLine, nextOffset = selectLines(value.Content, startLine, input.MaxLines, limit)
		metadata["start_line"] = startLine
		if more {
			if nextLine > 0 {
				metadata["next_start_line"] = nextLine
			}
			if nextOffset > 0 {
				metadata["next_offset"] = nextOffset
			}
		}
	case "query":
		var nextOffset int
		excerpt, more, nextOffset = queryLines(value.Content, input.Query, input.Offset, input.MaxLines, limit)
		metadata["query"] = input.Query
		metadata["offset"] = input.Offset
		if more {
			metadata["next_offset"] = nextOffset
		}
	default:
		return Result{}, fmt.Errorf("unsupported result retrieval mode %q", input.Mode)
	}
	metadata["excerpt_bytes"] = len(excerpt)
	return Result{
		Content: excerpt, IsError: value.IsError, Metadata: metadata,
		Truncated: more, OriginalBytes: len(value.Content), Handle: input.Handle,
	}, nil
}

func (*resultRetrieval) ExecutionDisposition() ExecutionDisposition {
	return DispositionAbortImmediately
}

func (t *resultRetrieval) ExecuteOutcome(
	ctx context.Context,
	raw json.RawMessage,
) (Result, Outcome, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, Outcome{Status: OutcomeCanceled}, err
	}
	result, err := t.Execute(ctx, raw)
	outcome := OutcomeFromResult(result)
	if err != nil {
		outcome.Status = OutcomeFailed
	}
	result.Outcome = CloneOutcome(&outcome)
	return result, outcome, err
}

func boundedSlice(value string, offset, limit int) (string, bool) {
	if offset >= len(value) || limit == 0 {
		return "", offset < len(value)
	}
	offset = validUTF8Start(value, offset)
	end := min(len(value), offset+limit)
	for end > offset && !utf8.ValidString(value[offset:end]) {
		end--
	}
	return value[offset:end], end < len(value)
}

func validUTF8Start(value string, offset int) int {
	offset = min(max(0, offset), len(value))
	for offset < len(value) && offset > 0 && value[offset]&0xc0 == 0x80 {
		offset++
	}
	return offset
}

func validSuffix(value string, offset int) string {
	return value[validUTF8Start(value, offset):]
}

func summarizeResult(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	if limit < 5 {
		excerpt, _ := boundedSlice(value, 0, limit)
		return excerpt, true
	}
	const separator = "\n...\n"
	if limit <= len(separator) {
		excerpt, _ := boundedSlice(value, 0, limit)
		return excerpt, true
	}
	headBytes := (limit - len(separator)) / 2
	tailBytes := limit - len(separator) - headBytes
	head, _ := boundedSlice(value, 0, headBytes)
	tail := validSuffix(value, len(value)-tailBytes)
	for len(head)+len(separator)+len(tail) > limit {
		tail = validSuffix(tail, 1)
	}
	return head + separator + tail, true
}

func countLines(value string) int {
	if value == "" {
		return 0
	}
	count := strings.Count(value, "\n")
	if !strings.HasSuffix(value, "\n") {
		count++
	}
	return count
}

func selectLines(value string, startLine, maxLines, limit int) (excerpt string, more bool, nextLine, nextOffset int) {
	lines := strings.SplitAfter(value, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if startLine > len(lines) {
		return "", false, 0, 0
	}
	startOffset := 0
	for _, line := range lines[:startLine-1] {
		startOffset += len(line)
	}
	lines = lines[startLine-1:]
	selectedLines := len(lines)
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[:maxLines]
		selectedLines = maxLines
	}
	selected := strings.Join(lines, "")
	excerpt, byteMore := boundedSlice(selected, 0, limit)
	if byteMore {
		return excerpt, true, 0, startOffset + len(excerpt)
	}
	nextLine = startLine + selectedLines
	if nextLine > countLines(value) {
		nextLine = 0
	}
	return excerpt, nextLine > 0, nextLine, 0
}

func queryLines(value, query string, offset, maxLines, limit int) (excerpt string, more bool, nextOffset int) {
	if offset >= len(value) {
		return "", false, 0
	}
	offset = validUTF8Start(value, offset)
	var builder strings.Builder
	cursor := offset
	matches := 0
	for _, line := range strings.SplitAfter(value[offset:], "\n") {
		lineStart := cursor
		cursor += len(line)
		if strings.Contains(line, query) {
			remaining := limit - builder.Len()
			part, lineMore := boundedSlice(line, 0, remaining)
			builder.WriteString(part)
			matches++
			if lineMore {
				return builder.String(), true, lineStart + len(part)
			}
			if maxLines > 0 && matches >= maxLines {
				return builder.String(), cursor < len(value), cursor
			}
			if builder.Len() >= limit {
				return builder.String(), cursor < len(value), cursor
			}
		}
	}
	return builder.String(), false, 0
}

type Claims struct {
	mu     sync.Mutex
	active map[uint64][]Resource
	queue  []*claimWaiter
	next   uint64
}

type claimWaiter struct {
	resources []Resource
	ready     chan uint64
}

func NewClaims() *Claims {
	return &Claims{active: make(map[uint64][]Resource)}
}

func (c *Claims) Acquire(ctx context.Context, resources []string) (func(), error) {
	typed := make([]Resource, 0, len(resources))
	for _, value := range uniqueSorted(resources) {
		kind, id, ok := strings.Cut(value, ":")
		if !ok {
			kind, id = "opaque", value
		}
		typed = append(typed, Resource{Kind: kind, ID: id, Access: AccessWrite})
	}
	return c.AcquireResources(ctx, typed)
}

func (c *Claims) AcquireResources(ctx context.Context, resources []Resource) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resources = uniqueResources(resources)
	waiter := &claimWaiter{
		resources: resources,
		ready:     make(chan uint64, 1),
	}
	c.mu.Lock()
	if !c.conflictsActive(resources) && !c.conflictsQueued(resources) {
		id := c.grantLocked(resources)
		c.mu.Unlock()
		return c.releaseFunc(id), nil
	}
	c.queue = append(c.queue, waiter)
	c.mu.Unlock()
	select {
	case id := <-waiter.ready:
		return c.releaseFunc(id), nil
	case <-ctx.Done():
		c.mu.Lock()
		if removeClaimWaiter(&c.queue, waiter) {
			c.dispatchLocked()
			c.mu.Unlock()
			return nil, ctx.Err()
		}
		c.mu.Unlock()
		// The grant raced with cancellation. Consume it and release the Claim
		// before returning so canceled waiters cannot leak ownership.
		id := <-waiter.ready
		c.releaseID(id)
		return nil, ctx.Err()
	}
}

func (c *Claims) Active() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.active)
}

func (c *Claims) Waiting() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.queue)
}

func (c *Claims) releaseFunc(id uint64) func() {
	var once sync.Once
	return func() {
		once.Do(func() { c.releaseID(id) })
	}
}

func (c *Claims) releaseID(id uint64) {
	c.mu.Lock()
	if _, exists := c.active[id]; exists {
		delete(c.active, id)
		c.dispatchLocked()
	}
	c.mu.Unlock()
}

func (c *Claims) grantLocked(resources []Resource) uint64 {
	c.next++
	c.active[c.next] = resources
	return c.next
}

func (c *Claims) dispatchLocked() {
	for index := 0; index < len(c.queue); {
		waiter := c.queue[index]
		if c.conflictsActive(waiter.resources) ||
			c.conflictsEarlierQueued(index, waiter.resources) {
			index++
			continue
		}
		id := c.grantLocked(waiter.resources)
		copy(c.queue[index:], c.queue[index+1:])
		c.queue = c.queue[:len(c.queue)-1]
		waiter.ready <- id
	}
}

func (c *Claims) conflictsActive(requested []Resource) bool {
	for _, held := range c.active {
		if resourceSetsConflict(requested, held) {
			return true
		}
	}
	return false
}

func (c *Claims) conflictsQueued(requested []Resource) bool {
	for _, waiter := range c.queue {
		if resourceSetsConflict(requested, waiter.resources) {
			return true
		}
	}
	return false
}

func (c *Claims) conflictsEarlierQueued(index int, requested []Resource) bool {
	for earlier := 0; earlier < index; earlier++ {
		if resourceSetsConflict(requested, c.queue[earlier].resources) {
			return true
		}
	}
	return false
}

func resourceSetsConflict(left, right []Resource) bool {
	for _, requested := range left {
		for _, held := range right {
			if resourcesOverlap(requested, held) &&
				(requested.Access == AccessWrite || held.Access == AccessWrite) {
				return true
			}
		}
	}
	return false
}

func removeClaimWaiter(queue *[]*claimWaiter, target *claimWaiter) bool {
	for index, waiter := range *queue {
		if waiter != target {
			continue
		}
		copy((*queue)[index:], (*queue)[index+1:])
		*queue = (*queue)[:len(*queue)-1]
		return true
	}
	return false
}

func resourcesOverlap(left, right Resource) bool {
	if isPathKind(left.Kind) && isPathKind(right.Kind) {
		leftPath, rightPath := filepath.Clean(left.Path), filepath.Clean(right.Path)
		if leftPath == rightPath {
			return true
		}
		return left.Tree && pathContains(leftPath, rightPath) ||
			right.Tree && pathContains(rightPath, leftPath)
	}
	if left.Kind != right.Kind {
		return false
	}
	leftID, rightID := left.ID, right.ID
	if leftID == "" {
		leftID = left.Path
	}
	if rightID == "" {
		rightID = right.Path
	}
	if leftID == rightID {
		return true
	}
	return left.Tree && idContains(leftID, rightID) || right.Tree && idContains(rightID, leftID)
}

func isPathKind(kind string) bool {
	return kind == "file" || kind == "directory" || kind == "repo" || kind == "workspace"
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func idContains(parent, child string) bool {
	parent = strings.TrimSuffix(parent, "/")
	return child == parent || strings.HasPrefix(child, parent+"/")
}

func uniqueResources(values []Resource) []Resource {
	sort.Slice(values, func(i, j int) bool { return values[i].Key() < values[j].Key() })
	result := values[:0]
	for _, value := range values {
		if value.Kind != "" && (len(result) == 0 || value.Key() != result[len(result)-1].Key()) {
			result = append(result, value)
		}
	}
	return result
}

func uniqueSorted(values []string) []string {
	sort.Strings(values)
	result := values[:0]
	for _, value := range values {
		if value != "" && (len(result) == 0 || value != result[len(result)-1]) {
			result = append(result, value)
		}
	}
	return result
}
