package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

type Mode string

const (
	ModePlan    Mode = "plan"
	ModeAct     Mode = "act"
	ModeOperate Mode = "operate"
)

type Permission string

const (
	PermissionSuggest Permission = "suggest"
	PermissionAuto    Permission = "auto"
	PermissionBypass  Permission = "bypass"
	PermissionNever   Permission = "never"
)

type Capability = tool.Capability

const (
	CapabilityRead    = tool.CapabilityRead
	CapabilityWrite   = tool.CapabilityWrite
	CapabilityProcess = tool.CapabilityProcess
	CapabilityNetwork = tool.CapabilityNetwork
	CapabilityPlugin  = tool.CapabilityPlugin
)

type Action string

const (
	ActionAllow Action = "allow"
	ActionAsk   Action = "ask"
	ActionDeny  Action = "deny"
	ActionHold  Action = "hold"
)

type Invocation struct {
	CallID, Tool string
	Arguments    json.RawMessage
	Resources    []tool.Resource
	Capability   tool.Capability
	Access       tool.AccessMode
	Sandbox      tool.SandboxRequirement
	Journaled    bool
	Validated    bool
}

type Rule struct {
	Tool          string `json:"tool"`
	Resource      string `json:"resource,omitempty"`
	CommandPrefix string `json:"command_prefix,omitempty"`
	GrantKey      string `json:"grant_key,omitempty"`
	Action        Action `json:"action"`
	Code          string `json:"code,omitempty"`
}

type Runtime struct {
	mu         sync.RWMutex
	Revision   uint64
	Mode       Mode
	Permission Permission
	// DisableAutoReview is the fail-closed operational kill switch.
	DisableAutoReview        bool
	Grants, User, Repository []Rule
	Approvals                *ApprovalCache
	Granular                 Granular
	Now                      func() time.Time
}

type Decision struct {
	Action       Action
	Code, Reason string
}

type DecisionError struct {
	Code, Reason string
}

func (e *DecisionError) Error() string {
	return e.Code + ": " + e.Reason
}

func DefaultRuntime(mode Mode, permission Permission) *Runtime {
	return &Runtime{
		Revision: 1, Mode: mode, Permission: permission,
		Grants:    []Rule{{Tool: "*", Resource: "*", Action: ActionAllow}},
		Approvals: NewApprovalCache(), Now: time.Now,
	}
}

func TightenPermission(requested, ceiling Permission) Permission {
	ranks := map[Permission]int{
		PermissionNever: 0, PermissionSuggest: 1,
		PermissionAuto: 2, PermissionBypass: 3,
	}
	requestedRank, requestedOK := ranks[requested]
	ceilingRank, ceilingOK := ranks[ceiling]
	if !requestedOK || !ceilingOK {
		return PermissionNever
	}
	if requestedRank > ceilingRank {
		return ceiling
	}
	return requested
}

// CloneSampling copies policy state while sharing session grants and clocks.
func (r *Runtime) CloneSampling() *Runtime {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return &Runtime{
		Revision: r.Revision, Mode: r.Mode, Permission: r.Permission,
		DisableAutoReview: r.DisableAutoReview,
		Grants:            append([]Rule(nil), r.Grants...),
		User:              append([]Rule(nil), r.User...),
		Repository:        append([]Rule(nil), r.Repository...),
		Approvals:         r.Approvals, Granular: r.Granular, Now: r.Now,
	}
}

func (r *Runtime) Evaluate(invocation Invocation) Decision {
	if r == nil {
		return deny("policy_unavailable", "security runtime is required")
	}
	return r.CloneSampling().evaluate(invocation)
}

func (r *Runtime) ManagedGrant(invocation Invocation) (Rule, bool) {
	if r == nil {
		return Rule{}, false
	}
	snapshot := r.CloneSampling()
	return strongestMatch(snapshot.Grants, invocation)
}

func (r *Runtime) evaluate(invocation Invocation) Decision {
	if invocation.CallID == "" || invocation.Tool == "" {
		return deny("policy_invalid_invocation", "call id and tool are required")
	}
	if !invocation.Validated {
		return deny("policy_unvalidated_invocation", "schema and resources must be validated before policy")
	}
	if invocation.Capability == "" {
		return deny("policy_unknown_capability", "descriptor capability is required")
	}
	grant, ok := strongestMatch(r.Grants, invocation)
	if !ok {
		return deny("tool_grant_missing", "no matching managed tool grant")
	}
	if grant.Action == ActionDeny || grant.Action == ActionHold {
		return deny("tool_grant_denied", "managed tool grant denied this invocation")
	}
	repositoryAsk := false
	if rule, ok := strongestMatch(r.Repository, invocation); ok {
		switch rule.Action {
		case ActionDeny:
			return deny("repository_rule_denied", "repository deny rule matched")
		case ActionHold:
			code := rule.Code
			if code == "" {
				code = "repository_hold"
			}
			return deny(code, "repository mechanical hold matched")
		case ActionAsk:
			repositoryAsk = true
		case ActionAllow:
			return deny("repository_source_invalid", "repository authority cannot allow")
		}
	}
	userAsk, userAllow := false, false
	if rule, ok := strongestMatch(r.User, invocation); ok {
		switch rule.Action {
		case ActionDeny, ActionHold:
			return deny("user_rule_denied", "user authority denied this invocation")
		case ActionAsk:
			userAsk = true
		case ActionAllow:
			userAllow = true
		}
	}
	if err := modeDecision(r.Mode, invocation.Capability); err != nil {
		return decisionFromError(err)
	}
	effect := NormalizeEffect(invocation)
	permissionAction, err := permissionDecision(r.Permission, invocation.Capability, effect.Risk)
	if err != nil {
		return decisionFromError(err)
	}
	needsApproval := repositoryAsk || userAsk || grant.Action == ActionAsk ||
		(permissionAction == ActionAsk && !userAllow)
	decision := Decision{Action: ActionAllow}
	if needsApproval {
		decision = Decision{Action: ActionAsk, Code: "approval_required", Reason: "approval is required"}
		effect := NormalizeEffect(invocation)
		_, typed := GrantForInvocation(invocation)
		if !r.DisableAutoReview && permissionAction == ActionAsk &&
			!repositoryAsk && grant.Action != ActionAsk && typed &&
			effect.Risk == RiskMedium &&
			(effect.Kind == EffectNetworkRead || effect.Kind == EffectAgentLifecycle) {
			decision = Decision{
				Action: ActionAllow, Code: "auto_review_allowed",
				Reason: "bounded medium-risk effect has an exact typed grant",
			}
		}
	}
	decision = ApplySurfaceTightening(
		decision, ClassifySurface(invocation.Tool, invocation.Capability), r.Granular,
	)
	return decision
}

func deny(code, reason string) Decision {
	return Decision{Action: ActionDeny, Code: code, Reason: reason}
}

func decisionFromError(err error) Decision {
	var decision *DecisionError
	if errors.As(err, &decision) {
		return deny(decision.Code, decision.Reason)
	}
	return deny("policy_denied", err.Error())
}

func modeDecision(mode Mode, capability tool.Capability) error {
	switch mode {
	case ModePlan:
		if capability != tool.CapabilityRead {
			return decisionError("mode_denied", "plan mode is read-only")
		}
	case ModeAct, ModeOperate:
	default:
		return decisionError("mode_unknown", "unknown mode is denied")
	}
	return nil
}

func permissionDecision(permission Permission, capability tool.Capability, risk RiskLevel) (Action, error) {
	if permission != PermissionSuggest && permission != PermissionAuto &&
		permission != PermissionBypass && permission != PermissionNever {
		return ActionDeny, decisionError("permission_unknown", "unknown permission is denied")
	}
	if permission == PermissionNever {
		if capability == tool.CapabilityRead {
			return ActionAllow, nil
		}
		return ActionDeny, decisionError("permission_denied", "never posture denies side effects")
	}
	if risk == RiskCritical {
		return ActionDeny, decisionError("permission_denied", "critical-risk execution is denied")
	}
	if permission == PermissionBypass || capability == tool.CapabilityRead || risk == RiskLow {
		return ActionAllow, nil
	}
	return ActionAsk, nil
}

func strongestMatch(rules []Rule, invocation Invocation) (Rule, bool) {
	var strongest Rule
	found := false
	for _, rule := range rules {
		if ruleMatches(rule, invocation) &&
			(!found || actionPriority(rule.Action) > actionPriority(strongest.Action)) {
			strongest, found = rule, true
		}
	}
	return strongest, found
}

func ruleMatches(rule Rule, invocation Invocation) bool {
	if rule.Tool != "" && rule.Tool != "*" && rule.Tool != invocation.Tool {
		return false
	}
	if rule.Resource != "" && rule.Resource != "*" {
		matched := false
		for _, resource := range invocation.Resources {
			value := resource.Path
			if value == "" {
				value = resource.ID
			}
			value = filepath.ToSlash(filepath.Clean(value))
			pattern := filepath.ToSlash(filepath.Clean(rule.Resource))
			if value == pattern || strings.HasPrefix(value, strings.TrimSuffix(pattern, "/")+"/") {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if rule.CommandPrefix != "" {
		var input struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(invocation.Arguments, &input) != nil ||
			!commandRuleMatches(input.Command, rule.CommandPrefix, rule.Action) {
			return false
		}
	}
	if rule.GrantKey != "" {
		grant, ok := GrantForInvocation(invocation)
		if !ok || grant.Key != rule.GrantKey {
			return false
		}
	}
	return true
}

func actionPriority(action Action) int {
	return map[Action]int{
		ActionAllow: 1, ActionAsk: 2, ActionDeny: 3, ActionHold: 4,
	}[action]
}

func decisionError(code, reason string) error {
	return &DecisionError{Code: code, Reason: reason}
}

func Validate(runtime *Runtime) error {
	if runtime == nil {
		return errors.New("runtime is required")
	}
	if err := modeDecision(runtime.Mode, tool.CapabilityRead); err != nil {
		return fmt.Errorf("mode: %w", err)
	}
	if _, err := permissionDecision(runtime.Permission, tool.CapabilityRead, RiskLow); err != nil {
		return fmt.Errorf("permission: %w", err)
	}
	if err := ValidateRules(SourceManaged, runtime.Grants); err != nil {
		return err
	}
	if err := ValidateRules(SourceUser, runtime.User); err != nil {
		return err
	}
	if err := ValidateRules(SourceRepository, runtime.Repository); err != nil {
		return err
	}
	return nil
}
