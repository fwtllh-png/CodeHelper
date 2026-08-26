package policy

import (
	"fmt"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

type PlanningPolicy string
type PlanApproval string

const (
	PlanningOff      PlanningPolicy = "off"
	PlanningAdaptive PlanningPolicy = "adaptive"
	PlanningRequired PlanningPolicy = "required"

	PlanApprovalManual PlanApproval = "manual"
	PlanApprovalAuto   PlanApproval = "auto"
)

type PlanningSnapshot struct {
	Planning      string `json:"planning,omitempty"`
	PlanApproval  string `json:"plan_approval,omitempty"`
	PlanSubmitted bool   `json:"plan_submitted,omitempty"`
	PlanApproved  bool   `json:"plan_approved,omitempty"`
}

func (r *Runtime) PlanningSnapshot() PlanningSnapshot {
	if r == nil {
		return PlanningSnapshot{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return PlanningSnapshot{
		Planning: string(r.PlanningPolicy), PlanApproval: string(r.PlanApproval),
		PlanSubmitted: r.PlanSubmitted, PlanApproved: r.PlanApproved,
	}
}

func (s PlanningSnapshot) Guidance() string {
	if s.Planning == "" || s.Planning == string(PlanningOff) {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(
		&b, "- planning=%s approval=%s submitted=%t approved=%t\n",
		s.Planning, s.PlanApproval, s.PlanSubmitted, s.PlanApproved,
	)
	if s.Planning == string(PlanningRequired) {
		b.WriteString("- submit_plan is required before consequential actions\n")
	} else {
		b.WriteString("- use submit_plan before complex or high-risk actions\n")
	}
	if s.PlanApproval == string(PlanApprovalManual) {
		b.WriteString("- after submit_plan, wait for approval before acting\n")
	}
	return b.String()
}

func (p SurfacePosture) Label() string {
	if p == "" {
		return "inherit"
	}
	return string(p)
}

func (r *Runtime) ConfigurePlanning(
	planning PlanningPolicy,
	approval PlanApproval,
) uint64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.PlanningPolicy, r.PlanApproval = planning, approval
	r.PlanSubmitted, r.PlanApproved = false, false
	return r.bumpRevisionLocked()
}

func (r *Runtime) SubmitPlan() uint64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.PlanSubmitted = true
	r.PlanApproved = r.PlanApproval == PlanApprovalAuto
	return r.bumpRevisionLocked()
}

func (r *Runtime) ApprovePlan() uint64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.PlanSubmitted, r.PlanApproved = true, true
	return r.bumpRevisionLocked()
}

func (r *Runtime) ResetPlanState() uint64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.PlanSubmitted, r.PlanApproved = false, false
	return r.bumpRevisionLocked()
}

func planningDecision(
	r *Runtime,
	invocation Invocation,
	effect Effect,
) *Decision {
	if r == nil || r.Mode == ModePlan ||
		!consequentialPlanningEffect(effect.Kind) {
		return nil
	}
	if r.PlanningPolicy != PlanningOff &&
		r.PlanningPolicy != PlanningAdaptive &&
		r.PlanningPolicy != PlanningRequired {
		return &Decision{
			Action: ActionDeny, Code: "planning_policy_invalid",
			Reason: "unknown planning policy is denied",
		}
	}
	if r.PlanningPolicy == PlanningOff {
		return nil
	}
	required := r.PlanningPolicy == PlanningRequired ||
		(r.PlanningPolicy == PlanningAdaptive &&
			adaptivePlanningRequired(invocation, effect))
	if !required && !r.PlanSubmitted {
		return nil
	}
	if !r.PlanSubmitted {
		return &Decision{
			Action: ActionHold, Code: "plan_required",
			Reason: "submit a structured Plan before consequential actions",
		}
	}
	if !r.PlanApproved {
		return &Decision{
			Action: ActionHold, Code: "plan_approval_required",
			Reason: "the submitted Plan requires approval before execution",
		}
	}
	return nil
}

func validatePlanning(planning PlanningPolicy, approval PlanApproval) error {
	if planning != PlanningOff && planning != PlanningAdaptive &&
		planning != PlanningRequired {
		return fmt.Errorf("unknown planning policy %q", planning)
	}
	if approval != PlanApprovalManual && approval != PlanApprovalAuto {
		return fmt.Errorf("unknown plan approval %q", approval)
	}
	return nil
}

func consequentialPlanningEffect(kind EffectKind) bool {
	switch kind {
	case EffectWorkspaceRead, EffectProcessReadOnly,
		EffectSessionMutation, EffectAgentMessage:
		return false
	default:
		return true
	}
}

func adaptivePlanningRequired(invocation Invocation, effect Effect) bool {
	if effect.Risk == RiskHigh || effect.Risk == RiskCritical ||
		effect.Kind == EffectNetworkMutating ||
		effect.Kind == EffectExternalMutation ||
		effect.Kind == EffectAgentLifecycle {
		return true
	}
	writes := 0
	for _, resource := range invocation.Resources {
		if (resource.Kind == "file" || resource.Kind == "directory") &&
			resource.Access != tool.AccessRead {
			writes++
		}
	}
	return writes > 1
}
