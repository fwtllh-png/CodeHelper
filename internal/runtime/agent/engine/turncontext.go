package engine

import (
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextstore"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	runtimeextension "github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

// TurnIdentity binds a Scope to the durable Session and Turn revisions it may
// commit. These values never change after the Scope is opened.
type TurnIdentity struct {
	SessionID       string
	TurnID          string
	ProfileRevision uint64
}

// TurnRequest is the host input frozen before a Scope starts.
type TurnRequest struct {
	Prompt        string
	Intent        protocol.TurnIntent
	Orchestration *protocol.OrchestrationCorrelation
	Attachments   []provider.Attachment
	Recovery      *protocol.TurnRecoveryContext
}

// TurnLimits freezes the budgets that bound one Scope.
type TurnLimits struct {
	MaxSteps        int
	MaxOutputTokens uint64
	Budget          Budget
}

type TurnSnapshotSources struct {
	MCP            func() []MCPHealthSnapshot
	ExtensionPlan  func() (runtimeextension.Plan, error)
	Skills         func() []SkillSummary
	SkillSelection func(string) ([]SkillSummary, SkillSelectionMetrics, error)
}

// TurnSpec is the complete immutable input for one Engine Scope. Host profile,
// policy, route, catalog, and skill mutations apply only to the next Scope.
type TurnSpec struct {
	Identity TurnIdentity
	Request  TurnRequest
	Profile  protocol.SessionProfile
	// Purpose is what this turn samples for, derived from the frozen mode. It is
	// what selects Route out of the session's routing table.
	Purpose model.Purpose
	// Route is the model this turn samples on. Freezing it with the mode is what
	// keeps a mid-turn switch to plan from changing which model is already
	// answering.
	Route          model.ReadyRoute
	Provider       string
	Model          string
	Mode           policy.Mode
	Posture        policy.Permission
	Workspace      string
	Sandbox        string
	Policy         *policy.Runtime
	Kernel         turnkernel.Policy
	Limits         TurnLimits
	World          contextstore.WorldBaseline
	Window         contextstore.WindowLedger
	Catalog        tool.CatalogSnapshot
	Skills         []SkillSummary
	SkillSelection SkillSelectionMetrics
	MCP            []MCPHealthSnapshot
	ExtensionPlan  runtimeextension.Plan
}

// SkillSummary is a turn-frozen skill catalog entry (N10).
type SkillSummary struct {
	Name           string
	Description    string
	Source         string
	Path           string
	Plugin         string
	Handle         string
	PackageHandle  string
	ResourceHandle string
}

type SkillSelectionMetrics struct {
	Method          string
	CatalogSize     int
	CandidateSize   int
	VisibleSize     int
	ExplicitMatches int
	OriginalTokens  uint64
	ProjectedTokens uint64
	TokenSavings    float64
	Recall          float64
	Precision       float64
	CacheHit        bool
}

// PurposeForMode is which route a turn in this mode samples on. Plan mode is the
// only mode with a purpose of its own: operate is act with wider permissions, not
// a different kind of thinking, so giving it a third slot would ask an operator
// to configure a distinction the runtime does not make.
func PurposeForMode(mode policy.Mode) model.Purpose {
	if mode == policy.ModePlan {
		return model.PurposePlan
	}
	return model.PurposeAct
}

// SnapshotTurnSpec captures all mutable Session inputs before a Scope starts.
//
// It fails when the frozen mode's purpose has no route, which happens only under
// a locked route set. Failing here is deliberate: it is before the turn announces
// what it is about to do, so a locked session that cannot honor plan mode says so
// instead of sampling on the act model and reporting plan.
func SnapshotTurnSpec(
	options Options,
	identity TurnIdentity,
	request TurnRequest,
) (TurnSpec, error) {
	security := options.Security
	if security == nil {
		security = policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass)
	}
	routes, err := effectiveRoutes(options)
	if err != nil {
		return TurnSpec{}, err
	}
	purpose := PurposeForMode(security.Mode)
	route, err := routes.For(purpose)
	if err != nil {
		return TurnSpec{}, err
	}
	if options.ToolCatalogSync != nil {
		if err := options.ToolCatalogSync(); err != nil {
			return TurnSpec{}, protocol.WrapProblem(
				protocol.CodeUnavailable,
				"tool catalog synchronization failed",
				true,
				err,
			)
		}
	}
	catalog, err := options.Tools.Snapshot()
	if err != nil {
		return TurnSpec{}, fmt.Errorf("snapshot turn tool catalog: %w", err)
	}
	request.Intent = protocol.NormalizeTurnIntent(request.Intent)
	request.Orchestration = protocol.CloneOrchestrationCorrelation(
		request.Orchestration,
	)
	request.Attachments = append([]provider.Attachment(nil), request.Attachments...)
	if request.Recovery != nil {
		recovery := *request.Recovery
		request.Recovery = &recovery
	}
	spec := TurnSpec{
		Identity: identity,
		Request:  request,
		Profile: protocol.SessionProfile{
			Version:  protocol.SessionProfileVersion,
			Revision: identity.ProfileRevision,
			Mode:     string(security.Mode), Provider: route.ProviderID(),
			Model: route.Model().ID, ReasoningEffort: options.ReasoningEffort,
			ApprovalPosture: string(security.Permission),
			ExecutionTarget: "local", MaxSteps: options.MaxSteps,
			PromptCacheRevision: identity.ProfileRevision,
		},
		Purpose:  purpose,
		Route:    route,
		Provider: route.ProviderID(), Model: route.Model().ID,
		Mode: security.Mode, Posture: security.Permission,
		Workspace: options.Workspace, Sandbox: sandboxIdentity(options.Tools),
		Policy: security.CloneSampling(),
		Kernel: turnkernel.Policy{
			CompletionRequired: options.RequireCompletionDeclaration,
			VerificationRequired: options.Verify.enabled() ||
				request.Intent == protocol.TurnIntentWorkspaceChange ||
				options.RequireCompletionDeclaration,
			VerificationMustPass: request.Intent ==
				protocol.TurnIntentWorkspaceChange ||
				options.RequireCompletionDeclaration,
			VerificationMode:        options.Verify.Mode,
			VerificationOnFailure:   options.Verify.OnFailure,
			CompletionRepairLimit:   maxCompletionRepairs,
			WorkspaceRepairLimit:    maxWorkspaceChangeRepairs,
			DeclarationRepairLimit:  maxDeclarationRepairs,
			VerificationRepairLimit: uint32(max(options.Verify.MaxRepairSteps, 0)),
			JournalRequired:         options.Journal != nil,
		},
		Limits: TurnLimits{
			MaxSteps:        options.MaxSteps,
			MaxOutputTokens: options.MaxOutputTokens,
			Budget:          options.Budget,
		},
		Catalog: catalog,
	}
	if options.TurnSnapshots.SkillSelection != nil {
		spec.Skills, spec.SkillSelection, err =
			options.TurnSnapshots.SkillSelection(request.Prompt)
		if err != nil {
			return TurnSpec{}, fmt.Errorf("select turn skills: %w", err)
		}
		spec.Skills = append([]SkillSummary(nil), spec.Skills...)
	} else if options.TurnSnapshots.Skills != nil {
		spec.Skills = append([]SkillSummary(nil), options.TurnSnapshots.Skills()...)
	}
	if options.TurnSnapshots.MCP != nil {
		spec.MCP = append([]MCPHealthSnapshot(nil), options.TurnSnapshots.MCP()...)
	}
	if options.TurnSnapshots.ExtensionPlan != nil {
		spec.ExtensionPlan, err = options.TurnSnapshots.ExtensionPlan()
		if err != nil {
			return TurnSpec{}, fmt.Errorf("snapshot turn extension plan: %w", err)
		}
		spec.ExtensionPlan = spec.ExtensionPlan.Clone()
	}
	return spec, nil
}

// effectiveRoutes is the routing table an Options describes: the one it carries,
// or a single-route table built from Route for the callers that only ever had
// one model.
func effectiveRoutes(options Options) (model.RouteSet, error) {
	if options.Routes.Ready() {
		return options.Routes, nil
	}
	return model.NewRouteSet(options.Route, nil, false)
}

func sandboxIdentity(registry *tool.Registry) string {
	if registry == nil {
		return "unavailable"
	}
	backend := registry.SandboxBackend()
	if backend == nil {
		return "unavailable"
	}
	capability := backend.Capability()
	if !capability.Available {
		return "unavailable"
	}
	return fmt.Sprintf("%s/%s/%s", capability.Platform, capability.Backend, capability.Strength)
}
