package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
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
	CallID     string
	Tool       string
	Arguments  json.RawMessage
	Resources  []tool.Resource
	Capability tool.Capability
	Validated  bool
}

type Rule struct {
	Tool          string `json:"tool"`
	Resource      string `json:"resource,omitempty"`
	CommandPrefix string `json:"command_prefix,omitempty"`
	Action        Action `json:"action"`
	Code          string `json:"code,omitempty"`
}

type Runtime struct {
	Mode       Mode
	Permission Permission
	Grants     []Rule
	Repository []Rule
	Approvals  *ApprovalCache
	Granular   Granular
	Now        func() time.Time
}

type Decision struct {
	Action Action
	Code   string
	Reason string
}

type DecisionError struct {
	Code   string
	Reason string
}

func (e *DecisionError) Error() string {
	return e.Code + ": " + e.Reason
}

func DefaultRuntime(mode Mode, permission Permission) *Runtime {
	return &Runtime{
		Mode: mode, Permission: permission,
		Grants:    append([]Rule{{Tool: "*", Resource: "*", Action: ActionAllow}}, LifecycleGrants()...),
		Approvals: NewApprovalCache(), Now: time.Now,
	}
}

// TightenPermission applies a fixed authority ceiling to a requested posture.
// Unknown values fail closed. The ordering reflects the maximum automatic
// authority each posture can exercise.
func TightenPermission(requested, ceiling Permission) Permission {
	rank := func(value Permission) int {
		switch value {
		case PermissionNever:
			return 0
		case PermissionSuggest:
			return 1
		case PermissionAuto:
			return 2
		case PermissionBypass:
			return 3
		default:
			return -1
		}
	}
	requestedRank, ceilingRank := rank(requested), rank(ceiling)
	if requestedRank < 0 || ceilingRank < 0 {
		return PermissionNever
	}
	if requestedRank > ceilingRank {
		return ceiling
	}
	return requested
}

// CloneSampling returns a turn-local copy of Mode/Permission and rule lists.
// Approvals and Now are shared with the session so grants stay coherent; host
// Mode/Permission mutations on the parent Runtime do not affect the clone.
func (r *Runtime) CloneSampling() *Runtime {
	if r == nil {
		return nil
	}
	return &Runtime{
		Mode: r.Mode, Permission: r.Permission,
		Grants:     append([]Rule(nil), r.Grants...),
		Repository: append([]Rule(nil), r.Repository...),
		Approvals:  r.Approvals, Granular: r.Granular, Now: r.Now,
	}
}

// LifecycleGrants are default ask rules for write/lifecycle tools that land in
// later product-parity phases. Specific ask wins over the wildcard allow grant.
func LifecycleGrants() []Rule {
	names := []string{
		"task_cancel",
		"automation_create", "automation_update", "automation_pause",
		"automation_resume", "automation_delete", "automation_run",
		"github_comment", "github_close_issue", "github_close_pr",
		"spawn_agent", "send_message", "followup_task",
		"interrupt_agent", "close_agent",
	}
	rules := make([]Rule, 0, len(names))
	for _, name := range names {
		rules = append(rules, Rule{
			Tool: name, Resource: "*", Action: ActionAsk, Code: "lifecycle_approval_required",
		})
	}
	return rules
}

func (r *Runtime) Authorize(ctx context.Context, invocation Invocation) error {
	_ = ctx
	decision := r.Evaluate(invocation)
	if decision.Action == ActionAllow {
		return nil
	}
	return decisionError(decision.Code, decision.Reason)
}

func (r *Runtime) Evaluate(invocation Invocation) Decision {
	if invocation.CallID == "" || invocation.Tool == "" {
		return deny("policy_invalid_invocation", "call id and tool are required")
	}
	if !invocation.Validated {
		return deny("policy_unvalidated_invocation", "schema and resources must be validated before policy")
	}
	if invocation.Capability == "" {
		return deny("policy_unknown_capability", "descriptor capability is required")
	}
	if r == nil {
		return deny("policy_unavailable", "security runtime is required")
	}

	repositoryAsk := false
	repositoryAllow := false
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
			repositoryAllow = true
		}
	}
	grant, ok := strongestMatch(r.Grants, invocation)
	if !ok {
		return deny("tool_grant_missing", "no matching tool grant")
	}
	if grant.Action == ActionDeny || grant.Action == ActionHold {
		return deny("tool_grant_denied", "tool grant denied this invocation")
	}
	if err := modeDecision(r.Mode, invocation.Capability); err != nil {
		return decisionFromError(err)
	}
	permissionAction, err := permissionDecision(r.Mode, r.Permission, invocation.Capability)
	if err != nil {
		return decisionFromError(err)
	}
	if repositoryAllow {
		return ApplySurfaceTightening(
			Decision{Action: ActionAllow},
			ClassifySurface(invocation.Tool, invocation.Capability), r.Granular,
		)
	}
	// A workspace-bound file_write still passes schema/resource validation,
	// repository policy, read-before-write, journaling, and atomic commit. Under
	// suggest posture it does not need an additional interactive confirmation.
	if r.Permission == PermissionSuggest && invocation.Tool == "file_write" {
		permissionAction = ActionAllow
	}
	needsApproval := repositoryAsk || permissionAction == ActionAsk || grant.Action == ActionAsk
	decision := Decision{Action: ActionAllow}
	if needsApproval {
		decision = Decision{Action: ActionAsk, Code: "approval_required", Reason: "approval is required"}
	}
	return ApplySurfaceTightening(
		decision, ClassifySurface(invocation.Tool, invocation.Capability), r.Granular,
	)
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

func permissionDecision(mode Mode, permission Permission, capability tool.Capability) (Action, error) {
	switch permission {
	case PermissionSuggest:
		if capability == tool.CapabilityRead {
			return ActionAllow, nil
		}
		return ActionAsk, nil
	case PermissionAuto:
		switch capability {
		case tool.CapabilityRead, tool.CapabilityWrite:
			return ActionAllow, nil
		case tool.CapabilityProcess:
			if mode == ModeOperate {
				return ActionAllow, nil
			}
			return ActionAsk, nil
		case tool.CapabilityNetwork, tool.CapabilityPlugin:
			return ActionAsk, nil
		default:
			return ActionDeny, decisionError("permission_denied", "auto posture denies high-risk unapproved execution")
		}
	case PermissionBypass:
		return ActionAllow, nil
	case PermissionNever:
		if capability == tool.CapabilityRead {
			return ActionAllow, nil
		}
		return ActionDeny, decisionError("permission_denied", "never posture denies side effects")
	default:
		return ActionDeny, decisionError("permission_unknown", "unknown permission posture is denied")
	}
}

func strongestMatch(rules []Rule, invocation Invocation) (Rule, bool) {
	var matches []Rule
	for _, rule := range rules {
		if ruleMatches(rule, invocation) {
			matches = append(matches, rule)
		}
	}
	if len(matches) == 0 {
		return Rule{}, false
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return actionPriority(matches[i].Action) > actionPriority(matches[j].Action)
	})
	return matches[0], true
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
	return true
}

func commandRuleMatches(command, prefix string, action Action) bool {
	segments := shellCommandSegments(command)
	if len(segments) == 0 {
		return false
	}
	if action == ActionDeny || action == ActionHold {
		for _, segment := range segments {
			if commandPrefixMatches(segment, prefix) {
				return true
			}
		}
		return false
	}
	return len(segments) == 1 && commandPrefixMatches(segments[0], prefix)
}

func commandPrefixMatches(command, prefix string) bool {
	command = strings.Join(strings.Fields(command), " ")
	prefix = strings.Join(strings.Fields(prefix), " ")
	if command == prefix {
		return true
	}
	return strings.HasPrefix(command, prefix+" ")
}

func shellCommandSegments(command string) []string {
	var segments []string
	start := 0
	quote := rune(0)
	escaped := false
	runes := []rune(command)
	appendSegment := func(end int) {
		if segment := strings.TrimSpace(string(runes[start:end])); segment != "" {
			segments = append(segments, segment)
		}
	}
	for index, current := range runes {
		if escaped {
			escaped = false
			continue
		}
		if current == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' || current == '`' {
			quote = current
			continue
		}
		if current == ';' || current == '\n' || current == '|' || current == '&' {
			appendSegment(index)
			start = index + 1
		}
	}
	appendSegment(len(runes))
	return segments
}

func actionPriority(action Action) int {
	switch action {
	case ActionHold:
		return 4
	case ActionDeny:
		return 3
	case ActionAsk:
		return 2
	case ActionAllow:
		return 1
	default:
		return 5
	}
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
	if _, err := permissionDecision(runtime.Mode, runtime.Permission, tool.CapabilityRead); err != nil {
		return fmt.Errorf("permission: %w", err)
	}
	return nil
}
