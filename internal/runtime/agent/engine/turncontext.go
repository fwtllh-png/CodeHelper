package engine

import (
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

// TurnContext is the frozen sampling view for one Engine turn. Host policy
// mutations after SnapshotTurnContext apply to the next turn only.
type TurnContext struct {
	TurnID string
	// Purpose is what this turn samples for, derived from the frozen mode. It is
	// what selects Route out of the session's routing table.
	Purpose model.Purpose
	// Route is the model this turn samples on. Freezing it with the mode is what
	// keeps a mid-turn switch to plan from changing which model is already
	// answering.
	Route     model.ReadyRoute
	Provider  string
	Model     string
	Mode      policy.Mode
	Posture   policy.Permission
	Workspace string
	Sandbox   string
	Policy    *policy.Runtime
	Skills    []SkillSummary
}

// SkillSummary is a turn-frozen skill catalog entry (N10).
type SkillSummary struct {
	Name        string
	Description string
	Source      string
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

// SnapshotTurnContext captures route/mode/posture/sandbox/workspace/skills for a
// turn and installs a CloneSampling policy for Guard evaluation.
//
// It fails when the frozen mode's purpose has no route, which happens only under
// a locked route set. Failing here is deliberate: it is before the turn announces
// what it is about to do, so a locked session that cannot honor plan mode says so
// instead of sampling on the act model and reporting plan.
func SnapshotTurnContext(options Options, turnID string) (TurnContext, error) {
	security := options.Security
	if security == nil {
		security = policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass)
	}
	routes, err := effectiveRoutes(options)
	if err != nil {
		return TurnContext{}, err
	}
	purpose := PurposeForMode(security.Mode)
	route, err := routes.For(purpose)
	if err != nil {
		return TurnContext{}, err
	}
	tc := TurnContext{
		TurnID: turnID, Purpose: purpose, Route: route,
		Provider: route.ProviderID(), Model: route.Model().ID,
		Mode: security.Mode, Posture: security.Permission,
		Workspace: options.Workspace, Sandbox: sandboxIdentity(options.Tools),
		Policy: security.CloneSampling(),
	}
	if options.SkillSnapshot != nil {
		tc.Skills = append([]SkillSummary(nil), options.SkillSnapshot()...)
	}
	return tc, nil
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
