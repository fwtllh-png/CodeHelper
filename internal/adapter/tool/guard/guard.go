package guard

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/platform/textdiff"
	"github.com/fwtllh-png/CodeHelper/internal/security/egress"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type ApprovalRequest struct {
	RequestID           string                 `json:"request_id"`
	CallID              string                 `json:"call_id"`
	Tool                string                 `json:"tool"`
	Arguments           json.RawMessage        `json:"arguments"`
	ArgumentsDigest     string                 `json:"arguments_digest"`
	Resources           []tool.Resource        `json:"resources"`
	AllowedScopes       []policy.ApprovalScope `json:"allowed_scopes"`
	ExpiresAt           time.Time              `json:"expires_at"`
	ReplacementAllowed  bool                   `json:"replacement_allowed"`
	ModifiableArguments []string               `json:"modifiable_arguments"`
	// Reason explains why approval is needed (e.g. sandbox_escalate, network_host).
	Reason string `json:"reason,omitempty"`
	// Network is set for host-scoped egress approvals.
	Network  *NetworkApprovalContext `json:"network,omitempty"`
	EditPlan *tool.EditPlan          `json:"edit_plan,omitempty"`
}

// FileChange is one workspace path a tool invocation actually changed, derived
// by comparing the pre-execution fingerprint with the state on disk afterwards.
// Observation is the only reliable source: a tool's arguments need not name the
// paths it writes (file_patch carries them inside the diff body).
type FileChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	// Added and Removed count lines against the before-image the journal holds
	// for the turn, and stay zero for binary content or when no journal is
	// configured.
	Added   int `json:"added,omitempty"`
	Removed int `json:"removed,omitempty"`
}

// Change kinds reported in FileChange.Kind, shared with the workspace journal so
// the guard, the journal and the receipt use one vocabulary.
const (
	FileCreated  = workspacejournal.ChangeCreated
	FileModified = workspacejournal.ChangeModified
	FileDeleted  = workspacejournal.ChangeDeleted
)

// MetadataChanges is the tool.Result metadata key holding []FileChange.
const MetadataChanges = "changes"

// MetadataCanonicalPath is the tool.Result metadata key holding the absolute path
// of the file a read-tracked tool fingerprinted. It is how a caller learns that a
// tool read a file, so it is named rather than spelled out at each use.
const MetadataCanonicalPath = "canonical_path"

// NetworkApprovalContext carries Immediate/Deferred host approval metadata.
type NetworkApprovalContext struct {
	Host     string `json:"host"`
	Protocol string `json:"protocol"`
	Mode     string `json:"mode"`
}

type ApprovalDecision struct {
	RequestID            string
	CallID               string
	Approved             bool
	Canceled             bool // Esc / cancel: abort wait without deny-as-tool-failure
	Scope                policy.ApprovalScope
	ExpiresAt            time.Time
	ReplacementArguments json.RawMessage
	PlanID               string
}

type Hooks interface {
	Before(context.Context, Invocation) error
	After(context.Context, Invocation, tool.Result, error)
}

type Invocation struct {
	CallID     string
	Tool       string
	Arguments  json.RawMessage
	Resources  []tool.Resource
	Descriptor tool.Descriptor
}

// PermissionRequester runs before the interactive approval UI (N20).
// Deny wins; Allow may bypass the Ask prompt; Ask/empty continues to UI.
type PermissionRequester interface {
	PermissionRequest(ctx context.Context, invocation Invocation) (PermissionDecision, error)
}

// PermissionDecision is the pre-approval hook outcome.
type PermissionDecision struct {
	Action PermissionAction
	Reason string
	HookID string
}

// PermissionAction mirrors hooks allow/deny/ask without importing the hooks package.
type PermissionAction string

const (
	PermissionAllow PermissionAction = "allow"
	PermissionDeny  PermissionAction = "deny"
	PermissionAsk   PermissionAction = "ask"
)

type Options struct {
	Registry     *tool.Registry
	Policy       *policy.Runtime
	Workspace    string
	Approvals    func(context.Context, ApprovalRequest) error
	PersistAllow func(policy.Invocation) error
	// OnNetworkAllow is called when a network host is approved (or already
	// allowed by a session/always grant). Wire uses it to write the egress Gate.
	OnNetworkAllow func(host, protocol string)
	Hooks          Hooks
	// PermissionHooks run on ActionAsk before waitForApproval (N20).
	PermissionHooks PermissionRequester
	Now             func() time.Time
	ApprovalTTL     time.Duration
	ReadTracker     *workspacejournal.ReadTracker
	Journal         *workspacejournal.Manager
	Diagnostics     diagnostics.Runner
	// Escalation nil uses DefaultEscalationPolicy (escalate-on-failure on).
	Escalation *EscalationPolicy
	// ForceEditPlanApproval makes mediated workspace writes require a fresh,
	// one-shot plan approval even when a broader policy grant would allow them.
	ForceEditPlanApproval bool
}

type pending struct {
	callID   string
	decision chan ApprovalDecision
	resume   chan struct{}
	decided  bool
}

// ApprovalWaitOutcome is how a wait for a human ended.
type ApprovalWaitOutcome string

const (
	ApprovalWaitDecided  ApprovalWaitOutcome = "decided"
	ApprovalWaitExpired  ApprovalWaitOutcome = "expired"
	ApprovalWaitCanceled ApprovalWaitOutcome = "canceled"
)

// ApprovalWait is one stretch a tool call spent parked waiting for a human.
//
// The guard reports it because only the guard sees both ends: a caller watching
// from outside sees the request go out and then hears nothing until the tool has
// also finished running, so anything it measured would be the wait plus the work.
type ApprovalWait struct {
	RequestID string
	CallID    string
	Tool      string
	Waited    time.Duration
	Outcome   ApprovalWaitOutcome
}

type Guard struct {
	registry              *tool.Registry
	policy                *policy.Runtime
	workspace             string
	approvals             func(context.Context, ApprovalRequest) error
	persistAllow          func(policy.Invocation) error
	onNetworkAllow        func(host, protocol string)
	hooks                 Hooks
	permissionHooks       PermissionRequester
	now                   func() time.Time
	approvalTTL           time.Duration
	readTracker           *workspacejournal.ReadTracker
	journal               *workspacejournal.Manager
	diagnostics           diagnostics.Runner
	escalation            EscalationPolicy
	forceEditPlanApproval bool

	mu           sync.Mutex
	pending      map[string]*pending
	completed    map[string]struct{}
	approvalWait func(ApprovalWait)
}

type approvalAsk struct {
	Reason         string
	AllowedScopes  []policy.ApprovalScope
	DisableReplace bool
	Network        *NetworkApprovalContext
	EditPlan       *tool.EditPlan
}

func New(options Options) (*Guard, error) {
	if options.Registry == nil {
		return nil, errors.New("tool guard registry is required")
	}
	if options.Policy == nil {
		return nil, errors.New("tool guard policy is required")
	}
	if err := policy.Validate(options.Policy); err != nil {
		return nil, err
	}
	workspace := options.Workspace
	if workspace == "" {
		workspace = "."
	}
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve tool guard workspace: %w", err)
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.ApprovalTTL <= 0 {
		options.ApprovalTTL = 5 * time.Minute
	}
	if options.ReadTracker == nil {
		options.ReadTracker = workspacejournal.NewReadTracker()
	}
	if options.Diagnostics == nil {
		options.Diagnostics = diagnostics.UnavailableRunner{}
	}
	escalation := DefaultEscalationPolicy()
	if options.Escalation != nil {
		escalation = *options.Escalation
	}
	for index := range options.Policy.Repository {
		if err := canonicalizeRuleResource(&options.Policy.Repository[index], absolute); err != nil {
			return nil, fmt.Errorf("repository rule: %w", err)
		}
	}
	for index := range options.Policy.Grants {
		if err := canonicalizeRuleResource(&options.Policy.Grants[index], absolute); err != nil {
			return nil, fmt.Errorf("grant rule: %w", err)
		}
	}
	return &Guard{
		registry: options.Registry, policy: options.Policy, workspace: absolute,
		approvals: options.Approvals, persistAllow: options.PersistAllow,
		onNetworkAllow: options.OnNetworkAllow,
		hooks:          options.Hooks, permissionHooks: options.PermissionHooks, now: options.Now,
		approvalTTL: options.ApprovalTTL,
		readTracker: options.ReadTracker, journal: options.Journal, diagnostics: options.Diagnostics,
		escalation:            escalation,
		forceEditPlanApproval: options.ForceEditPlanApproval,
		pending:               make(map[string]*pending), completed: make(map[string]struct{}),
	}, nil
}

func (g *Guard) Execute(
	ctx context.Context, callID, name string, raw json.RawMessage,
) (tool.Result, error) {
	return g.ExecuteBound(ctx, callID, name, raw, tool.CatalogBinding{})
}

func (g *Guard) ExecuteBound(
	ctx context.Context,
	callID, name string,
	raw json.RawMessage,
	binding tool.CatalogBinding,
) (tool.Result, error) {
	if callID == "" {
		return tool.Result{}, errors.New("tool guard call id is required")
	}
	identity := tool.InvocationIdentityFrom(ctx)
	identity.CallID = callID
	ctx = tool.WithInvocationIdentity(ctx, identity)
	for {
		invocation, executor, err := g.prepare(ctx, name, callID, raw, binding)
		if err != nil {
			return tool.Result{}, err
		}
		decision := g.policy.Evaluate(policy.Invocation{
			CallID: callID, Tool: invocation.Tool, Arguments: invocation.Arguments,
			Resources: invocation.Resources, Capability: invocation.Descriptor.Capability,
			Validated: true,
		})
		if g.forceEditPlanApproval && mediatedFileWriter(invocation.Tool) &&
			decision.Action == policy.ActionAllow {
			decision.Action = policy.ActionAsk
			decision.Code = "edit_plan_required"
			decision.Reason = "workspace writes require a fresh edit plan approval"
		}
		switch decision.Action {
		case policy.ActionDeny, policy.ActionHold:
			return tool.Result{}, &policy.DecisionError{Code: decision.Code, Reason: decision.Reason}
		case policy.ActionAsk:
			now := g.now()
			policyInvocation := policy.Invocation{
				CallID: callID, Tool: invocation.Tool, Arguments: invocation.Arguments,
				Resources: invocation.Resources, Capability: invocation.Descriptor.Capability,
				Validated: true,
			}
			if !g.forceEditPlanApproval &&
				g.policy.Approvals != nil &&
				g.policy.Approvals.MatchInvocation(policyInvocation, now) {
				break
			}
			bypassAsk := false
			if g.permissionHooks != nil {
				permDecision, permErr := g.permissionHooks.PermissionRequest(ctx, invocation)
				if permErr != nil {
					return tool.Result{}, permErr
				}
				switch permDecision.Action {
				case PermissionDeny:
					reason := permDecision.Reason
					if reason == "" {
						reason = "permission hook denied"
					}
					return tool.Result{}, &policy.DecisionError{
						Code: "permission_hook_denied", Reason: reason,
					}
				case PermissionAllow:
					bypassAsk = true
				}
			}
			if bypassAsk {
				break
			}
			var editPlan *tool.EditPlan
			if mediatedFileWriter(invocation.Tool) {
				planner, ok := executor.(tool.EditPlanner)
				if !ok {
					return tool.Result{}, &policy.DecisionError{
						Code:   "edit_plan_unavailable",
						Reason: "workspace writer cannot produce a safe edit preview",
					}
				}
				plan, err := planner.PlanEdit(ctx, invocation.Arguments)
				if err != nil {
					return tool.Result{}, fmt.Errorf("plan workspace edit: %w", err)
				}
				editPlan = &plan
			}
			ask := networkApprovalAsk(policyInvocation, invocation.Descriptor.Capability)
			if editPlan != nil {
				ask.AllowedScopes = []policy.ApprovalScope{policy.ApprovalOnce}
				ask.DisableReplace = true
				ask.EditPlan = editPlan
			}
			approval, err := g.waitForApproval(
				ctx, invocation, policyInvocation, now, ask,
			)
			if err != nil {
				return tool.Result{}, err
			}
			if editPlan != nil {
				if approval.PlanID != editPlan.ID {
					return tool.Result{}, &policy.DecisionError{
						Code:   "edit_plan_mismatch",
						Reason: "approval does not identify the displayed edit plan",
					}
				}
				current, err := executor.(tool.EditPlanner).PlanEdit(ctx, invocation.Arguments)
				if err != nil {
					return tool.Result{}, fmt.Errorf("revalidate workspace edit: %w", err)
				}
				if current.ID != editPlan.ID {
					return tool.Result{}, &policy.DecisionError{
						Code:   "edit_plan_stale",
						Reason: "workspace changed after edit preview",
					}
				}
				// Planned writes are one-shot. Proceed directly to the existing
				// journal/fingerprint/atomic commit path without caching a grant.
				break
			}
			if len(approval.ReplacementArguments) != 0 {
				raw = append(json.RawMessage(nil), approval.ReplacementArguments...)
				replacement, _, err := g.prepare(ctx, name, callID, raw, binding)
				if err != nil {
					return tool.Result{}, fmt.Errorf("replacement arguments: %w", err)
				}
				replacementInvocation := policy.Invocation{
					CallID: callID, Tool: replacement.Tool, Arguments: replacement.Arguments,
					Resources: replacement.Resources, Capability: replacement.Descriptor.Capability,
					Validated: true,
				}
				replacementDecision := g.policy.Evaluate(replacementInvocation)
				if replacementDecision.Action != policy.ActionAsk {
					if replacementDecision.Action == policy.ActionAllow {
						continue
					}
					return tool.Result{}, &policy.DecisionError{
						Code: replacementDecision.Code, Reason: replacementDecision.Reason,
					}
				}
				if err := g.cacheApproval(replacementInvocation, approval); err != nil {
					return tool.Result{}, err
				}
				continue
			}
			if err := g.cacheApproval(policyInvocation, approval); err != nil {
				return tool.Result{}, err
			}
			continue
		case policy.ActionAllow:
			g.grantNetworkHosts(invocation.Resources)
		default:
			return tool.Result{}, errors.New("tool guard received invalid policy action")
		}

		release, err := g.registry.Claims().AcquireResources(ctx, invocation.Resources)
		if err != nil {
			return tool.Result{}, err
		}
		if g.hooks != nil {
			if err := g.hooks.Before(ctx, invocation); err != nil {
				release()
				return tool.Result{}, err
			}
		}
		writePaths := invocationWritePaths(invocation)
		requireRead := mediatedFileWriter(invocation.Tool)
		expectedWrites, err := g.prepareFileWrites(ctx, writePaths, requireRead)
		if err != nil {
			release()
			return tool.Result{}, err
		}
		executeContext := workspacejournal.WithExpectedWrites(ctx, expectedWrites)
		var readBefore *workspacejournal.Fingerprint
		if invocation.Tool == "file_read" {
			for _, resource := range invocation.Resources {
				if resource.Kind == "file" && resource.Path != "" {
					value, _, _, err := workspacejournal.Snapshot(resource.Path)
					if err != nil {
						release()
						return tool.Result{}, err
					}
					readBefore = &value
					break
				}
			}
		}
		egressRetried := false
		for {
			attempt := SandboxModeStrong
			var result tool.Result
			var executeErr error
			for {
				runContext := WithSandboxAttempt(executeContext, SandboxAttempt{Mode: attempt})
				result, executeErr = g.registry.ExecutePrepared(
					runContext, invocation.Tool, invocation.Arguments, executor,
				)
				if !IsSandboxDenial(executeErr, result) ||
					attempt == SandboxModeNone ||
					!g.canEscalate(invocation) {
					break
				}
				escalateInvocation := policy.Invocation{
					CallID: callID, Tool: invocation.Tool, Arguments: invocation.Arguments,
					Resources:  withSandboxNoneResource(invocation.Resources),
					Capability: invocation.Descriptor.Capability, Validated: true,
				}
				now := g.now()
				if g.policy.Approvals == nil || !g.policy.Approvals.MatchInvocation(escalateInvocation, now) {
					approval, err := g.waitForApproval(
						ctx, invocation, escalateInvocation, now,
						approvalAsk{
							Reason: ApprovalReasonSandboxEscalate,
							AllowedScopes: []policy.ApprovalScope{
								policy.ApprovalOnce, policy.ApprovalSession,
							},
							DisableReplace: true,
						},
					)
					if err != nil {
						if g.hooks != nil {
							g.hooks.After(ctx, invocation, result, err)
						}
						release()
						return tool.Result{}, err
					}
					if err := g.cacheApproval(escalateInvocation, approval); err != nil {
						if g.hooks != nil {
							g.hooks.After(ctx, invocation, result, err)
						}
						release()
						return tool.Result{}, err
					}
				}
				attempt = SandboxModeNone
			}
			if invocation.Tool == "file_read" && executeErr == nil {
				if err := g.recordFileRead(&result, invocation, readBefore); err != nil {
					executeErr = err
				}
			}
			if len(writePaths) != 0 {
				if err := g.finishFileWrites(
					ctx, writePaths, expectedWrites, &result,
					executeErr == nil, mediatedFileWriter(invocation.Tool),
				); err != nil && executeErr == nil {
					executeErr = err
				}
			}
			if host, protocol, ok := egressDeniedTarget(result, executeErr); ok && !egressRetried {
				egressRetried = true
				if err := g.approveEgressHost(ctx, invocation, callID, host, protocol); err != nil {
					if softFailEgressApproval(err) {
						if g.hooks != nil {
							g.hooks.After(ctx, invocation, result, nil)
						}
						release()
						if executeErr != nil {
							return tool.Result{}, executeErr
						}
						return result, nil
					}
					if g.hooks != nil {
						g.hooks.After(ctx, invocation, result, err)
					}
					release()
					return tool.Result{}, err
				}
				continue
			}
			if g.hooks != nil {
				g.hooks.After(ctx, invocation, result, executeErr)
			}
			release()
			if executeErr != nil {
				return tool.Result{}, executeErr
			}
			return result, nil
		}
	}
}

func (g *Guard) canEscalate(invocation Invocation) bool {
	return g.escalation.EscalateOnFailure &&
		invocation.Descriptor.SandboxRequirement == tool.SandboxStrong &&
		invocation.Descriptor.Capability != tool.CapabilityRead
}

// egressDeniedTarget extracts a host that RoundTrip refused for policy reasons.
// Soft tool failures carry error_category=egress_denied; hard errors may wrap
// egress.ErrDenied.
func egressDeniedTarget(result tool.Result, executeErr error) (host, protocol string, ok bool) {
	protocol = "https"
	if result.IsError && result.Metadata != nil {
		if category, _ := result.Metadata["error_category"].(string); category == "egress_denied" {
			host, _ = result.Metadata["host"].(string)
			if p, _ := result.Metadata["protocol"].(string); p != "" {
				protocol = p
			}
			if host != "" {
				return strings.ToLower(host), protocol, true
			}
		}
	}
	if executeErr != nil && errors.Is(executeErr, egress.ErrDenied) {
		host, protocol = hostProtocolFromEgressError(executeErr)
		if host != "" {
			return host, protocol, true
		}
	}
	return "", "", false
}

func hostProtocolFromEgressError(err error) (host, protocol string) {
	host, protocol, ok := egress.DeniedTarget(err)
	if !ok {
		return "", ""
	}
	return host, protocol
}

func softFailEgressApproval(err error) bool {
	if err == nil {
		return false
	}
	var decision *policy.DecisionError
	if errors.As(err, &decision) {
		switch decision.Code {
		case "approval_denied", "approval_canceled", "approval_expired":
			return true
		}
	}
	return false
}

// approveEgressHost asks the operator to Grant a host discovered mid-flight
// (redirects, search backends not in the pre-flight resource list, etc.).
func (g *Guard) approveEgressHost(
	ctx context.Context, invocation Invocation, callID, host, protocol string,
) error {
	if protocol == "" {
		protocol = "https"
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return errors.New("egress approval requires a host")
	}
	resources := []tool.Resource{
		{Kind: "host", ID: host, Access: tool.AccessRead},
		{Kind: "url", ID: protocol + "://" + host + "/", Access: tool.AccessRead},
	}
	policyInvocation := policy.Invocation{
		CallID: callID, Tool: invocation.Tool, Arguments: invocation.Arguments,
		Resources: resources, Capability: invocation.Descriptor.Capability, Validated: true,
	}
	now := g.now()
	if g.policy.Approvals != nil && g.policy.Approvals.MatchInvocation(policyInvocation, now) {
		g.grantNetworkHosts(resources)
		return nil
	}
	// Full/bypass already opted out of asks: grant the discovered host (e.g.
	// www.bing.com → cn.bing.com redirect) without a second prompt.
	if g.policy != nil && g.policy.Permission == policy.PermissionBypass {
		g.grantNetworkHosts(resources)
		return nil
	}
	approval, err := g.waitForApproval(
		ctx, invocation, policyInvocation, now,
		approvalAsk{
			Reason: ApprovalReasonNetworkHost,
			Network: &NetworkApprovalContext{
				Host: host, Protocol: protocol, Mode: string(policy.NetworkImmediate),
			},
			DisableReplace: true,
		},
	)
	if err != nil {
		return err
	}
	if err := g.cacheApproval(policyInvocation, approval); err != nil {
		return err
	}
	g.grantNetworkHosts(resources)
	return nil
}

func invocationWritePaths(invocation Invocation) []string {
	var paths []string
	for _, resource := range invocation.Resources {
		if resource.Kind == "file" && resource.Access == tool.AccessWrite && resource.Path != "" {
			paths = append(paths, resource.Path)
		}
	}
	sort.Strings(paths)
	return paths
}

func (g *Guard) prepareFileWrites(
	ctx context.Context, paths []string, requireRead bool,
) (map[string]workspacejournal.Fingerprint, error) {
	expected := make(map[string]workspacejournal.Fingerprint, len(paths))
	for _, path := range paths {
		if requireRead {
			if _, err := g.readTracker.ValidateWrite(path); err != nil {
				return nil, g.readValidationError(path, err)
			}
		}
		if g.journal != nil {
			if err := g.journal.Before(ctx, path); err != nil {
				return nil, fmt.Errorf("journal before-image %q: %w", path, err)
			}
			if requireRead {
				if _, err := g.readTracker.ValidateWrite(path); err != nil {
					return nil, g.readValidationError(path, err)
				}
			}
		}
		current, _, _, err := workspacejournal.Snapshot(path)
		if err != nil {
			return nil, err
		}
		if requireRead {
			if _, err := g.readTracker.ValidateWrite(path); err != nil {
				return nil, g.readValidationError(path, err)
			}
		}
		expected[path] = current
	}
	return expected, nil
}

func (g *Guard) readValidationError(path string, cause error) error {
	relative, err := filepath.Rel(g.workspace, path)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		relative = path
	}
	return &workspacejournal.ReadValidationError{
		Path: filepath.ToSlash(relative), Cause: cause,
	}
}

func (g *Guard) recordFileRead(
	result *tool.Result, invocation Invocation, before *workspacejournal.Fingerprint,
) error {
	for _, resource := range invocation.Resources {
		if resource.Kind != "file" || resource.Path == "" {
			continue
		}
		fingerprint, err := g.readTracker.Record(resource.Path)
		if err != nil {
			return fmt.Errorf("record file read fingerprint: %w", err)
		}
		if before == nil || !workspacejournal.Equal(*before, fingerprint) {
			g.readTracker.Invalidate(resource.Path)
			return fmt.Errorf("file read race %q: %w", resource.Path, workspacejournal.ErrStale)
		}
		if result.Metadata == nil {
			result.Metadata = make(map[string]any)
		}
		result.Metadata[MetadataCanonicalPath] = fingerprint.Path
		result.Metadata["content_sha256"] = fingerprint.SHA256
		result.Metadata["identity"] = fingerprint.Identity
		result.Metadata["fingerprint"] = fingerprint
	}
	return nil
}

func (g *Guard) finishFileWrites(
	ctx context.Context,
	paths []string,
	expected map[string]workspacejournal.Fingerprint,
	result *tool.Result,
	succeeded, runDiagnostics bool,
) error {
	var receipts []diagnostics.Receipt
	var changes []FileChange
	for _, path := range paths {
		if g.journal != nil {
			if err := g.journal.After(path); err != nil {
				return fmt.Errorf("journal commit record %q: %w", path, err)
			}
		}
		if !succeeded {
			continue
		}
		g.readTracker.Invalidate(path)
		change, changed, err := g.observeFileChange(ctx, expected[path], path)
		if err != nil {
			return fmt.Errorf("observe write %q: %w", path, err)
		}
		if changed {
			changes = append(changes, change)
		}
	}
	if !succeeded {
		return nil
	}
	if len(changes) != 0 {
		if result.Metadata == nil {
			result.Metadata = make(map[string]any)
		}
		result.Metadata[MetadataChanges] = changes
	}
	if runDiagnostics {
		// Seal every journal record before invoking an external checker. A
		// multi-file tool has already written all paths, so returning after the
		// first diagnostic failure must not leave the remaining records with
		// stale after-images that make automatic rollback conflict.
		for _, path := range paths {
			receipt, err := g.diagnostics.Run(ctx, path)
			if err != nil {
				if errors.Is(err, context.Canceled) ||
					errors.Is(err, context.DeadlineExceeded) {
					return fmt.Errorf("post-edit diagnostics %q: %w", path, err)
				}
				receipt = diagnostics.Receipt{
					Path: path, Status: "failed", Diagnostics: []diagnostics.Diagnostic{},
					Message: err.Error(),
				}
			}
			receipts = append(receipts, receipt)
		}
		if result.Metadata == nil {
			result.Metadata = make(map[string]any)
		}
		result.Metadata["diagnostics"] = receipts
	}
	return nil
}

// observeFileChange compares the fingerprint taken before execution against the
// state on disk afterwards. Content decides: rewriting a file with identical
// bytes is not a change even though its identity (mtime/inode) moved.
func (g *Guard) observeFileChange(
	ctx context.Context, before workspacejournal.Fingerprint, path string,
) (FileChange, bool, error) {
	after, _, _, err := workspacejournal.Snapshot(path)
	if err != nil {
		return FileChange{}, false, err
	}
	kind := workspacejournal.Record{Before: before, After: after}.Kind()
	if kind == "" {
		return FileChange{}, false, nil
	}
	relative, err := g.workspaceRelative(path)
	if err != nil {
		return FileChange{}, false, err
	}
	change := FileChange{Path: relative, Kind: kind}
	stats, counted, err := g.countLines(ctx, path)
	if err != nil {
		return FileChange{}, false, err
	}
	if counted {
		change.Added, change.Removed = stats.Added, stats.Removed
	}
	return change, true, nil
}

// countLines measures the turn's net line delta for a path against the journal's
// before-image, so a file edited several times in a turn reports the cumulative
// change rather than the last call's. Binary content and a missing journal yield
// no counts: an absent number is honest, a zero would read as "nothing changed".
func (g *Guard) countLines(
	ctx context.Context, path string,
) (textdiff.Stats, bool, error) {
	if g.journal == nil {
		return textdiff.Stats{}, false, nil
	}
	beforeData, existed, found, err := g.journal.BeforeImage(ctx, path)
	if err != nil || !found {
		return textdiff.Stats{}, false, err
	}
	afterData, err := os.ReadFile(path)
	afterMissing := false
	switch {
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		afterMissing = true
	default:
		return textdiff.Stats{}, false, err
	}
	stats, err := textdiff.Count(
		textdiff.Content{Data: beforeData, Missing: !existed},
		textdiff.Content{Data: afterData, Missing: afterMissing},
	)
	if errors.Is(err, textdiff.ErrBinary) {
		return textdiff.Stats{}, false, nil
	}
	if err != nil {
		return textdiff.Stats{}, false, err
	}
	return stats, true, nil
}

// mediatedFileWriter reports whether a tool edits workspace files through the
// guard's own read/write mediation: those calls must honour read-before-edit
// and get post-edit diagnostics. Change observation is not gated on this — it
// covers every declared write resource, whatever the tool.
func mediatedFileWriter(name string) bool {
	switch name {
	case "file_write", "file_edit", "file_apply", "file_patch":
		return true
	default:
		return false
	}
}

func (g *Guard) prepare(
	ctx context.Context, name, callID string, raw json.RawMessage, binding tool.CatalogBinding,
) (Invocation, tool.Executor, error) {
	canonical, descriptor, executor, err := g.registry.ResolveBound(name, binding)
	if err != nil {
		return Invocation{}, nil, err
	}
	repaired := tool.RepairArguments(raw)
	arguments, err := tool.NormalizeArguments(descriptor.InputSchema, repaired)
	if err != nil {
		return Invocation{}, nil, fmt.Errorf("tool %q arguments: %w", canonical, err)
	}
	if expander, ok := executor.(tool.ArgumentExpander); ok {
		arguments, err = expander.ExpandArguments(ctx, arguments)
		if err != nil {
			return Invocation{}, nil, fmt.Errorf("tool %q expand: %w", canonical, err)
		}
	}
	arguments, err = g.rewriteAbsolutePathArgs(descriptor, arguments)
	if err != nil {
		return Invocation{}, nil, fmt.Errorf("tool %q resources: %w", canonical, err)
	}
	resources, err := g.resolveResources(descriptor, arguments)
	if err != nil {
		return Invocation{}, nil, fmt.Errorf("tool %q resources: %w", canonical, err)
	}
	if descriptor.ParallelPolicy == tool.ParallelSerial {
		resources = append(resources, tool.Resource{
			Kind: "parallel", ID: "serial-tools", Access: tool.AccessWrite, Tree: true,
		})
	}
	if descriptor.SandboxRequirement == tool.SandboxStrong {
		if err := sandbox.RequireStrong(g.registry.InjectedSandbox(canonical)); err != nil {
			return Invocation{}, nil, err
		}
	}
	return Invocation{
		CallID: callID, Tool: canonical, Arguments: arguments,
		Resources: resources, Descriptor: descriptor,
	}, executor, nil
}

// rewriteAbsolutePathArgs converts absolute paths that resolve inside the
// workspace into relative paths so sandbox Open* APIs and resource checks agree.
func (g *Guard) rewriteAbsolutePathArgs(
	descriptor tool.Descriptor, arguments json.RawMessage,
) (json.RawMessage, error) {
	var values map[string]any
	if err := json.Unmarshal(arguments, &values); err != nil {
		return nil, err
	}
	changed := false
	for _, template := range descriptor.ResourceResolver.Templates {
		if template.Field == "" || !isPathKind(template.Kind) {
			continue
		}
		raw, ok := values[template.Field].(string)
		if !ok || raw == "" || !filepath.IsAbs(raw) {
			continue
		}
		rel, err := g.workspaceRelative(raw)
		if err != nil {
			return nil, err
		}
		values[template.Field] = rel
		changed = true
	}
	if !changed {
		return arguments, nil
	}
	out, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (g *Guard) workspaceRelative(value string) (string, error) {
	absWorkspace, err := filepath.Abs(g.workspace)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(absWorkspace); err == nil {
		absWorkspace = resolved
	}
	absValue, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(absValue); err == nil {
		absValue = resolved
	} else {
		// Missing path: resolve existing parents so /var vs /private/var matches.
		resolvedParent, missing, joinErr := resolveExistingParent(absValue)
		if joinErr != nil {
			return "", errors.New("absolute resource path is not allowed")
		}
		absValue = resolvedParent
		for _, part := range missing {
			absValue = filepath.Join(absValue, part)
		}
	}
	rel, err := filepath.Rel(absWorkspace, absValue)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("absolute resource path is not allowed")
	}
	if rel == "." {
		return ".", nil
	}
	return rel, nil
}

func resolveExistingParent(path string) (string, []string, error) {
	current := filepath.Clean(path)
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			return resolved, missing, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", nil, err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", nil, err
		}
		missing = append([]string{filepath.Base(current)}, missing...)
		current = parent
	}
}

func (g *Guard) waitForApproval(
	ctx context.Context,
	invocation Invocation,
	policyInvocation policy.Invocation,
	now time.Time,
	ask ...approvalAsk,
) (ApprovalDecision, error) {
	handler := g.approvalHandler()
	if handler == nil {
		return ApprovalDecision{}, &policy.DecisionError{
			Code: "approval_host_unavailable", Reason: "approval host is not connected",
		}
	}
	var opts approvalAsk
	if len(ask) > 0 {
		opts = ask[0]
	}
	expiresAt := now.Add(g.approvalTTL)
	request, err := policy.NewApprovalRequestForScope(
		policyInvocation, policy.ApprovalOnce, expiresAt,
	)
	if err != nil {
		return ApprovalDecision{}, err
	}
	requestID := randomID("approval_")
	request.RequestID = requestID
	fields := schemaProperties(invocation.Descriptor.InputSchema)
	scopes := []policy.ApprovalScope{
		policy.ApprovalOnce, policy.ApprovalSession, policy.ApprovalAlways,
	}
	if len(opts.AllowedScopes) != 0 {
		scopes = append([]policy.ApprovalScope(nil), opts.AllowedScopes...)
	}
	replacementAllowed := !opts.DisableReplace
	modifiable := fields
	if opts.DisableReplace {
		modifiable = nil
	}
	event := ApprovalRequest{
		RequestID: requestID, CallID: invocation.CallID, Tool: invocation.Tool,
		Arguments: request.Arguments, ArgumentsDigest: request.ArgumentsDigest,
		Resources: request.Resources, AllowedScopes: scopes,
		ExpiresAt: expiresAt, ReplacementAllowed: replacementAllowed,
		ModifiableArguments: modifiable, Reason: opts.Reason, Network: opts.Network,
		EditPlan: opts.EditPlan,
	}
	entry := &pending{
		callID: invocation.CallID, decision: make(chan ApprovalDecision, 1),
		resume: make(chan struct{}),
	}
	g.mu.Lock()
	g.pending[requestID] = entry
	g.mu.Unlock()
	defer g.finishPending(requestID)
	// The wait starts when the request is raised, so a host that is slow to show it
	// counts as waiting rather than as free. It is reported the moment the wait
	// ends rather than when this function returns: what follows a decision — scope
	// checks, a persisted allow rule — is the guard's own work, not a human's.
	parked := g.now()
	reportWait := func(outcome ApprovalWaitOutcome) {
		observe := g.approvalWaitObserver()
		if observe == nil {
			return
		}
		observe(ApprovalWait{
			RequestID: requestID, CallID: invocation.CallID, Tool: invocation.Tool,
			Waited: g.now().Sub(parked), Outcome: outcome,
		})
	}
	if err := handler(ctx, event); err != nil {
		return ApprovalDecision{}, fmt.Errorf("emit approval request: %w", err)
	}
	timer := time.NewTimer(max(time.Millisecond, expiresAt.Sub(g.now())))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		reportWait(ApprovalWaitCanceled)
		return ApprovalDecision{}, ctx.Err()
	case <-timer.C:
		reportWait(ApprovalWaitExpired)
		return ApprovalDecision{}, &policy.DecisionError{
			Code: "approval_expired", Reason: "approval request expired",
		}
	case decision := <-entry.decision:
		select {
		case <-ctx.Done():
			reportWait(ApprovalWaitCanceled)
			return ApprovalDecision{}, ctx.Err()
		case <-entry.resume:
			reportWait(ApprovalWaitDecided)
		}
		if decision.Canceled {
			return ApprovalDecision{}, &policy.DecisionError{
				Code: "approval_canceled", Reason: "approval was canceled",
			}
		}
		if !decision.Approved {
			return ApprovalDecision{}, &policy.DecisionError{
				Code: "approval_denied", Reason: "approval was denied",
			}
		}
		if decision.Scope == "" {
			decision.Scope = policy.ApprovalOnce
		}
		if len(opts.AllowedScopes) != 0 {
			allowed := false
			for _, scope := range opts.AllowedScopes {
				if decision.Scope == scope {
					allowed = true
					break
				}
			}
			if !allowed {
				return ApprovalDecision{}, &policy.DecisionError{
					Code: "approval_scope_denied", Reason: "approval scope is not allowed",
				}
			}
		}
		if decision.Scope == policy.ApprovalAlways {
			if decision.ExpiresAt.IsZero() || !decision.ExpiresAt.After(g.now()) {
				decision.ExpiresAt = g.now().Add(24 * time.Hour)
			}
		} else {
			if decision.ExpiresAt.IsZero() {
				decision.ExpiresAt = expiresAt
			}
			if !decision.ExpiresAt.After(g.now()) || decision.ExpiresAt.After(expiresAt) {
				return ApprovalDecision{}, &policy.DecisionError{
					Code: "approval_expired", Reason: "approval expiry is invalid",
				}
			}
		}
		return decision, nil
	}
}

func (g *Guard) cacheApproval(
	invocation policy.Invocation, decision ApprovalDecision,
) error {
	toCache := invocation
	if hostScoped, ok := policy.HostScopedInvocation(invocation); ok {
		// Session/Always grants are host-scoped so URLs on the same host reuse.
		toCache = hostScoped
	}
	if decision.Scope == policy.ApprovalAlways {
		if g.persistAllow != nil {
			if err := g.persistAllow(toCache); err != nil {
				return fmt.Errorf("persist always allow: %w", err)
			}
		}
		// Same-process acceleration: treat as session-scoped cache entry.
		decision.Scope = policy.ApprovalSession
		if decision.ExpiresAt.IsZero() || !decision.ExpiresAt.After(g.now()) {
			decision.ExpiresAt = g.now().Add(24 * time.Hour)
		}
	}
	if decision.Scope == policy.ApprovalOnce {
		// Once stays exact (full args + resources) so a single URL grant does
		// not silently cover other paths on the same host.
		toCache = invocation
	}
	g.grantNetworkHosts(toCache.Resources)
	request, err := policy.NewApprovalRequestForScope(
		toCache, decision.Scope, decision.ExpiresAt,
	)
	if err != nil {
		return err
	}
	if g.policy.Approvals == nil {
		g.policy.Approvals = policy.NewApprovalCache()
	}
	if err := g.policy.Approvals.Add(request, decision.Scope); err != nil {
		return err
	}
	return nil
}

func networkApprovalAsk(
	invocation policy.Invocation, capability tool.Capability,
) approvalAsk {
	if capability != tool.CapabilityNetwork {
		return approvalAsk{}
	}
	host := ""
	protocol := "https"
	for _, resource := range invocation.Resources {
		if resource.Kind == "url" && strings.TrimSpace(resource.ID) != "" {
			if target, ok := policy.ParseNetworkTarget(resource.ID); ok {
				host = target.Host
				protocol = target.Protocol
				break
			}
		}
	}
	if host == "" {
		for _, resource := range invocation.Resources {
			if resource.Kind == "host" && strings.TrimSpace(resource.ID) != "" {
				host = strings.ToLower(strings.TrimSpace(resource.ID))
				break
			}
		}
	}
	if host == "" {
		return approvalAsk{}
	}
	return approvalAsk{
		Reason: ApprovalReasonNetworkHost,
		Network: &NetworkApprovalContext{
			Host: host, Protocol: protocol, Mode: string(policy.NetworkImmediate),
		},
	}
}

func (g *Guard) grantNetworkHosts(resources []tool.Resource) {
	if g == nil || g.onNetworkAllow == nil {
		return
	}
	for _, resource := range resources {
		switch resource.Kind {
		case "host":
			host := strings.ToLower(strings.TrimSpace(resource.ID))
			if host != "" {
				g.onNetworkAllow(host, "https")
			}
		case "url":
			if target, ok := policy.ParseNetworkTarget(resource.ID); ok {
				g.onNetworkAllow(target.Host, target.Protocol)
			}
		}
	}
}

func (g *Guard) Decide(decision ApprovalDecision) error {
	if err := g.StageDecision(decision); err != nil {
		return err
	}
	return g.Resume(decision.RequestID)
}

func (g *Guard) StageDecision(decision ApprovalDecision) error {
	if decision.RequestID == "" {
		return errors.New("approval request id is required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	entry := g.pending[decision.RequestID]
	if entry == nil {
		if _, exists := g.completed[decision.RequestID]; exists {
			return errors.New("approval decision is duplicate or late")
		}
		return errors.New("approval request is unknown")
	}
	if decision.CallID != "" && decision.CallID != entry.callID {
		return errors.New("approval decision call id does not match request")
	}
	if entry.decided {
		return errors.New("approval decision is duplicate")
	}
	entry.decided = true
	entry.decision <- decision
	return nil
}

func (g *Guard) Resume(requestID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	entry := g.pending[requestID]
	if entry == nil {
		return errors.New("approval request is duplicate or late")
	}
	if !entry.decided {
		return errors.New("approval request has no decision")
	}
	close(entry.resume)
	delete(g.pending, requestID)
	g.completed[requestID] = struct{}{}
	return nil
}

func (g *Guard) finishPending(requestID string) {
	g.mu.Lock()
	if _, exists := g.pending[requestID]; exists {
		delete(g.pending, requestID)
		g.completed[requestID] = struct{}{}
	}
	g.mu.Unlock()
}

func (g *Guard) Pending() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.pending)
}

func (g *Guard) SetApprovalHandler(handler func(context.Context, ApprovalRequest) error) {
	g.mu.Lock()
	g.approvals = handler
	g.mu.Unlock()
}

// SetApprovalWaitObserver installs the observer that hears how long each
// approval kept a tool parked.
func (g *Guard) SetApprovalWaitObserver(observe func(ApprovalWait)) {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.approvalWait = observe
	g.mu.Unlock()
}

// SwapPolicy replaces the Guard policy pointer and returns the previous one.
// Used to install a turn-local CloneSampling view for the duration of a turn.
func (g *Guard) SwapPolicy(next *policy.Runtime) *policy.Runtime {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	prev := g.policy
	if next != nil {
		g.policy = next
	}
	return prev
}

// Policy returns the policy currently installed on the Guard.
func (g *Guard) Policy() *policy.Runtime {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.policy
}

func (g *Guard) approvalHandler() func(context.Context, ApprovalRequest) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.approvals
}

func (g *Guard) approvalWaitObserver() func(ApprovalWait) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.approvalWait
}

func (g *Guard) resolveResources(
	descriptor tool.Descriptor, arguments json.RawMessage,
) ([]tool.Resource, error) {
	var values map[string]any
	if err := json.Unmarshal(arguments, &values); err != nil {
		return nil, err
	}
	var resources []tool.Resource
	for _, template := range descriptor.ResourceResolver.Templates {
		value := template.ID
		if template.Field != "" {
			field, exists := values[template.Field]
			if !exists {
				continue
			}
			text, ok := field.(string)
			if !ok {
				return nil, fmt.Errorf("resource field %q is not a string", template.Field)
			}
			value = text
		}
		if value == "" && !isPathKind(template.Kind) {
			continue
		}
		resource := tool.Resource{
			Kind: template.Kind, Access: template.Access, Tree: template.Tree,
		}
		if isPathKind(template.Kind) {
			path, err := g.canonicalPath(value, template.Glob)
			if err != nil {
				return nil, err
			}
			resource.Path = path
		} else {
			resource.ID = strings.TrimSpace(value)
		}
		resources = append(resources, resource)
	}
	// Derive host resources from URLs for host-scoped network approvals.
	for _, resource := range append([]tool.Resource(nil), resources...) {
		if resource.Kind != "url" || resource.ID == "" {
			continue
		}
		target, ok := policy.ParseNetworkTarget(resource.ID)
		if !ok {
			continue
		}
		resources = append(resources, policy.HostResource(target, resource.Access))
	}
	if field := descriptor.ResourceResolver.PatchField; field != "" {
		patch, _ := values[field].(string)
		for _, path := range patchPaths(patch) {
			canonical, err := g.canonicalPath(path, false)
			if err != nil {
				return nil, err
			}
			resources = append(resources, tool.Resource{
				Kind: "file", Path: canonical, Access: tool.AccessWrite,
			})
		}
	}
	if field := descriptor.ResourceResolver.ChangesField; field != "" {
		paths, err := changePaths(values[field])
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			canonical, err := g.canonicalPath(path, false)
			if err != nil {
				return nil, err
			}
			resources = append(resources, tool.Resource{
				Kind: "file", Path: canonical, Access: tool.AccessWrite,
			})
		}
	}
	sort.Slice(resources, func(i, j int) bool { return resources[i].Key() < resources[j].Key() })
	result := resources[:0]
	for _, resource := range resources {
		if len(result) == 0 || result[len(result)-1].Key() != resource.Key() {
			result = append(result, resource)
		}
	}
	return result, nil
}

func (g *Guard) canonicalPath(value string, glob bool) (string, error) {
	if value == "" {
		value = "."
	}
	if filepath.IsAbs(value) {
		rel, err := g.workspaceRelative(value)
		if err != nil {
			return "", err
		}
		value = rel
	}
	clean := filepath.Clean(value)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("resource path escapes workspace")
	}
	suffix := ""
	base := clean
	if glob {
		if index := strings.IndexAny(base, "*?[{"); index >= 0 {
			slash := strings.LastIndex(base[:index], string(filepath.Separator))
			if slash < 0 {
				base, suffix = ".", base
			} else {
				base, suffix = base[:slash], base[slash+1:]
			}
		}
	}
	candidate := filepath.Join(g.workspace, base)
	canonical, err := canonicalMissing(candidate)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(g.workspace, canonical)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("resource path resolves outside workspace")
	}
	if suffix != "" {
		canonical = filepath.Join(canonical, suffix)
	}
	return filepath.Clean(canonical), nil
}

func canonicalMissing(path string) (string, error) {
	current := filepath.Clean(path)
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func patchPaths(patch string) []string {
	var result []string
	for line := range strings.SplitSeq(patch, "\n") {
		for _, marker := range []string{"--- ", "+++ ", "rename from ", "rename to "} {
			value, found := strings.CutPrefix(line, marker)
			if !found {
				continue
			}
			fields := strings.Fields(strings.TrimSpace(value))
			if len(fields) == 0 || fields[0] == "/dev/null" {
				break
			}
			path := strings.TrimPrefix(strings.TrimPrefix(fields[0], "a/"), "b/")
			if path != "" {
				result = append(result, path)
			}
			break
		}
	}
	return result
}

// changePaths enumerates every path named by a transaction argument, both the
// subject of each change and a move's destination. It fails on anything it
// cannot read: a shape the guard does not understand must stop the call rather
// than produce a short resource list that leaves writes uncovered.
func changePaths(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, errors.New("changes argument is not an array")
	}
	var paths []string
	for index, item := range items {
		change, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("change %d is not an object", index)
		}
		for _, field := range []string{"path", "to"} {
			raw, exists := change[field]
			if !exists {
				continue
			}
			text, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("change %d field %q is not a string", index, field)
			}
			if text = strings.TrimSpace(text); text != "" {
				paths = append(paths, text)
			}
		}
	}
	return paths, nil
}

func schemaProperties(schema map[string]any) []string {
	properties, _ := schema["properties"].(map[string]any)
	result := make([]string, 0, len(properties))
	for name := range properties {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func isPathKind(kind string) bool {
	return kind == "file" || kind == "directory" || kind == "repo" || kind == "workspace"
}

func randomID(prefix string) string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(value[:])
}

func canonicalizeRuleResource(rule *policy.Rule, workspace string) error {
	if rule.Resource == "" || rule.Resource == "*" || !isPathRule(rule.Resource) {
		return nil
	}
	if filepath.IsAbs(rule.Resource) {
		canonical, err := canonicalMissing(rule.Resource)
		if err != nil {
			return err
		}
		rule.Resource = canonical
		return nil
	}
	clean := filepath.Clean(rule.Resource)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return errors.New("rule resource escapes workspace")
	}
	canonical, err := canonicalMissing(filepath.Join(workspace, clean))
	if err != nil {
		return err
	}
	rule.Resource = canonical
	return nil
}

func isPathRule(value string) bool {
	return strings.Contains(value, "/") || strings.Contains(value, `\`) ||
		value == "." || !strings.Contains(value, ":")
}
