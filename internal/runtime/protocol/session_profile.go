package protocol

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

const SessionProfileVersion = 1

type SessionProfile struct {
	Version             int      `json:"version"`
	Revision            uint64   `json:"revision"`
	Mode                string   `json:"mode"`
	Provider            string   `json:"provider"`
	Model               string   `json:"model"`
	ReasoningEffort     string   `json:"reasoning_effort,omitempty"`
	EnabledToolIDs      []string `json:"enabled_tool_ids,omitempty"`
	ApprovalPosture     string   `json:"approval_posture"`
	ExecutionTarget     string   `json:"execution_target"`
	MaxSteps            int      `json:"max_steps"`
	PromptCacheRevision uint64   `json:"prompt_cache_revision"`
}

type SessionProfilePatch struct {
	Mode            *string   `json:"mode,omitempty"`
	Provider        *string   `json:"provider,omitempty"`
	Model           *string   `json:"model,omitempty"`
	ReasoningEffort *string   `json:"reasoning_effort,omitempty"`
	EnabledToolIDs  *[]string `json:"enabled_tool_ids,omitempty"`
	ApprovalPosture *string   `json:"approval_posture,omitempty"`
	ExecutionTarget *string   `json:"execution_target,omitempty"`
	MaxSteps        *int      `json:"max_steps,omitempty"`
}

type ModelCapabilities struct {
	DisplayName            string   `json:"display_name"`
	ContextWindow          uint64   `json:"context_window"`
	MaxOutputTokens        uint64   `json:"max_output_tokens"`
	Streaming              bool     `json:"streaming"`
	Reasoning              bool     `json:"reasoning"`
	ToolCalls              bool     `json:"tool_calls"`
	ParallelToolCalls      string   `json:"parallel_tool_calls"`
	NativeSearch           bool     `json:"native_search"`
	Vision                 bool     `json:"vision"`
	ImageInput             bool     `json:"image_input"`
	PromptCache            bool     `json:"prompt_cache"`
	ReasoningEfforts       []string `json:"reasoning_efforts,omitempty"`
	DefaultReasoningEffort string   `json:"default_reasoning_effort,omitempty"`
	CredentialStatus       string   `json:"credential_status"`
	Availability           string   `json:"availability"`
	UnavailableReason      string   `json:"unavailable_reason,omitempty"`
	SelectionMode          string   `json:"selection_mode"`
}

type SessionProfileCapabilities struct {
	Provider          string            `json:"provider"`
	Model             string            `json:"model"`
	ModelCapabilities ModelCapabilities `json:"model_capabilities"`
	MutableFields     []string          `json:"mutable_fields"`
}

type SessionProfileSnapshot struct {
	Profile      SessionProfile             `json:"profile"`
	Capabilities SessionProfileCapabilities `json:"capabilities"`
}

type SessionProfileUpdateResult struct {
	Profile          SessionProfile `json:"profile"`
	PromptCacheReset bool           `json:"prompt_cache_reset"`
	ResetReason      string         `json:"reset_reason,omitempty"`
}

func (p SessionProfile) Validate() error {
	if p.Version != SessionProfileVersion {
		return fmt.Errorf("unsupported session profile version %d", p.Version)
	}
	if p.Revision == 0 || p.PromptCacheRevision == 0 {
		return errors.New("session profile revisions must be positive")
	}
	switch p.Mode {
	case "plan", "act", "operate":
	default:
		return errors.New("session profile mode must be plan, act, or operate")
	}
	if !validProfileIdentifier(p.Provider) || !validProfileIdentifier(p.Model) {
		return errors.New("session profile provider and model are invalid")
	}
	switch p.ReasoningEffort {
	case "", "minimal", "low", "medium", "high", "max", "xhigh":
	default:
		return errors.New("session profile reasoning_effort is invalid")
	}
	switch p.ApprovalPosture {
	case "never", "suggest", "auto", "bypass":
	default:
		return errors.New("session profile approval_posture is invalid")
	}
	if p.ExecutionTarget != "local" {
		return errors.New("session profile execution_target must be local")
	}
	if p.MaxSteps < 0 {
		return errors.New("session profile max_steps must be non-negative")
	}
	if len(p.EnabledToolIDs) > 512 {
		return errors.New("session profile accepts at most 512 enabled tools")
	}
	seen := make(map[string]struct{}, len(p.EnabledToolIDs))
	for _, id := range p.EnabledToolIDs {
		if !validProfileIdentifier(id) {
			return errors.New("session profile enabled tool id is invalid")
		}
		if _, exists := seen[id]; exists {
			return errors.New("session profile enabled tool ids must be unique")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func (p SessionProfilePatch) Validate() error {
	if p.Mode == nil && p.Provider == nil && p.Model == nil &&
		p.ReasoningEffort == nil && p.EnabledToolIDs == nil &&
		p.ApprovalPosture == nil && p.ExecutionTarget == nil &&
		p.MaxSteps == nil {
		return errors.New("session profile patch must change at least one field")
	}
	return nil
}

func ApplySessionProfilePatch(
	current SessionProfile,
	patch SessionProfilePatch,
) (SessionProfileUpdateResult, error) {
	if err := current.Validate(); err != nil {
		return SessionProfileUpdateResult{}, err
	}
	if err := patch.Validate(); err != nil {
		return SessionProfileUpdateResult{}, err
	}
	next := current
	var cacheReasons []string
	setCacheReason := func(changed bool, field string) {
		if changed {
			cacheReasons = append(cacheReasons, field)
		}
	}
	if patch.Mode != nil {
		setCacheReason(next.Mode != *patch.Mode, "mode")
		next.Mode = *patch.Mode
	}
	if patch.Provider != nil {
		setCacheReason(next.Provider != *patch.Provider, "provider")
		next.Provider = *patch.Provider
	}
	if patch.Model != nil {
		setCacheReason(next.Model != *patch.Model, "model")
		next.Model = *patch.Model
	}
	if patch.ReasoningEffort != nil {
		setCacheReason(next.ReasoningEffort != *patch.ReasoningEffort, "reasoning_effort")
		next.ReasoningEffort = *patch.ReasoningEffort
	}
	if patch.EnabledToolIDs != nil {
		tools := append([]string(nil), (*patch.EnabledToolIDs)...)
		slices.Sort(tools)
		setCacheReason(!slices.Equal(next.EnabledToolIDs, tools), "enabled_tool_ids")
		next.EnabledToolIDs = tools
	}
	if patch.ApprovalPosture != nil {
		next.ApprovalPosture = *patch.ApprovalPosture
	}
	if patch.ExecutionTarget != nil {
		next.ExecutionTarget = *patch.ExecutionTarget
	}
	if patch.MaxSteps != nil {
		next.MaxSteps = *patch.MaxSteps
	}
	if err := next.Validate(); err != nil {
		return SessionProfileUpdateResult{}, err
	}
	if equalSessionProfile(next, current) {
		return SessionProfileUpdateResult{Profile: current}, nil
	}
	next.Revision++
	if len(cacheReasons) != 0 {
		next.PromptCacheRevision++
	}
	return SessionProfileUpdateResult{
		Profile: next, PromptCacheReset: len(cacheReasons) != 0,
		ResetReason: strings.Join(cacheReasons, ","),
	}, nil
}

func (c SessionProfileCapabilities) Validate(profile SessionProfile) error {
	if c.Provider != profile.Provider || c.Model != profile.Model {
		return errors.New("session profile capabilities do not match the profile route")
	}
	model := c.ModelCapabilities
	if strings.TrimSpace(model.DisplayName) == "" ||
		len(model.DisplayName) > 256 ||
		strings.ContainsAny(model.DisplayName, "\x00\r\n") ||
		model.ContextWindow == 0 ||
		model.MaxOutputTokens == 0 ||
		model.MaxOutputTokens > model.ContextWindow {
		return errors.New("session model capability identity or limits are invalid")
	}
	switch model.ParallelToolCalls {
	case "supported", "unsupported", "unknown":
	default:
		return errors.New("session model parallel tool capability is invalid")
	}
	switch model.CredentialStatus {
	case "configured", "missing", "invalid", "unknown":
	default:
		return errors.New("session model credential status is invalid")
	}
	switch model.Availability {
	case "available", "unavailable":
	default:
		return errors.New("session model availability is invalid")
	}
	if model.Availability == "unavailable" &&
		strings.TrimSpace(model.UnavailableReason) == "" {
		return errors.New("unavailable session model requires a reason")
	}
	switch model.SelectionMode {
	case "hot", "restart_required", "fixed":
	default:
		return errors.New("session model selection mode is invalid")
	}
	for _, field := range c.MutableFields {
		switch field {
		case "mode", "provider", "model", "reasoning_effort",
			"enabled_tool_ids", "approval_posture", "execution_target", "max_steps":
		default:
			return fmt.Errorf("unknown mutable session profile field %q", field)
		}
	}
	if !model.Reasoning &&
		(len(model.ReasoningEfforts) != 0 ||
			model.DefaultReasoningEffort != "") {
		return errors.New("non-reasoning model cannot advertise reasoning efforts")
	}
	if model.DefaultReasoningEffort != "" &&
		!slices.Contains(model.ReasoningEfforts, model.DefaultReasoningEffort) {
		return errors.New("default reasoning effort is not advertised")
	}
	return nil
}

func validProfileIdentifier(value string) bool {
	return value != "" && len(value) <= 256 &&
		!strings.ContainsAny(value, "\x00\r\n")
}

func equalSessionProfile(left, right SessionProfile) bool {
	return left.Version == right.Version &&
		left.Revision == right.Revision &&
		left.Mode == right.Mode &&
		left.Provider == right.Provider &&
		left.Model == right.Model &&
		left.ReasoningEffort == right.ReasoningEffort &&
		slices.Equal(left.EnabledToolIDs, right.EnabledToolIDs) &&
		left.ApprovalPosture == right.ApprovalPosture &&
		left.ExecutionTarget == right.ExecutionTarget &&
		left.MaxSteps == right.MaxSteps &&
		left.PromptCacheRevision == right.PromptCacheRevision
}
