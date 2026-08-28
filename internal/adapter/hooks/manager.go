package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type Manager struct {
	config   Config
	executor *executor
}

type hookResponse struct {
	Decision     Action            `json:"decision,omitempty"`
	Reason       string            `json:"reason,omitempty"`
	UpdatedInput json.RawMessage   `json:"updatedInput,omitempty"`
	Environment  map[string]string `json:"env,omitempty"`
}

func New(config Config, options Options) (*Manager, error) {
	config = cloneConfig(config)
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if options.DefaultTimeout < 0 {
		return nil, errors.New("default hook timeout must be positive")
	}
	if options.MaxOutputBytes < 0 {
		return nil, errors.New("default hook output limit must be positive")
	}
	workspace := options.Workspace
	if strings.TrimSpace(workspace) == "" {
		workspace = "."
	}
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve hooks workspace: %w", err)
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve hooks workspace: %w", err)
	}
	options.Workspace = absolute
	for event, configured := range config.Hooks {
		for index := range configured {
			directory, err := resolveWorkingDirectory(absolute, configured[index].WorkingDirectory)
			if err != nil {
				return nil, fmt.Errorf("hook %q: %w", configured[index].ID, err)
			}
			configured[index].WorkingDirectory = directory
		}
		config.Hooks[event] = configured
	}
	return &Manager{config: config, executor: newExecutor(options)}, nil
}

func cloneConfig(config Config) Config {
	cloned := Config{Version: config.Version, Hooks: make(map[Event][]HookConfig, len(config.Hooks))}
	for event, configured := range config.Hooks {
		hooks := make([]HookConfig, len(configured))
		copy(hooks, configured)
		for index := range hooks {
			hooks[index].Args = append([]string(nil), hooks[index].Args...)
			hooks[index].Env = append([]string(nil), hooks[index].Env...)
		}
		cloned.Hooks[event] = hooks
	}
	return cloned
}

func resolveWorkingDirectory(workspace, configured string) (string, error) {
	if strings.TrimSpace(configured) == "" {
		return workspace, nil
	}
	candidate := configured
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(workspace, candidate)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(candidate))
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	relative, err := filepath.Rel(workspace, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("working directory escapes workspace")
	}
	return resolved, nil
}

func (m *Manager) SessionStart(ctx context.Context, input SessionStartInput) {
	m.runObservers(ctx, SessionStart, input)
}

func (m *Manager) MessageSubmit(ctx context.Context, input MessageSubmitInput) error {
	inputKeys := keysOf(input)
	for _, hook := range m.config.Hooks[MessageSubmit] {
		result := m.executor.run(ctx, MessageSubmit, hook, input)
		if result.err != nil || result.exitCode != 0 {
			outcome, code := "continued", result.errCode
			if code == "" {
				code = "nonzero_exit"
				result.errCode = code
			}
			if !hook.ContinueOnError {
				outcome = "blocked"
			}
			m.executor.audit(ctx, MessageSubmit, hook.ID, inputKeys, nil, result, outcome, "")
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !hook.ContinueOnError {
				return protocol.NewFault(
					protocol.CodeConflict,
					fmt.Sprintf(
						"message submit hook %q rejected the turn (%s)",
						hook.ID,
						code,
					),
					false,
					protocol.FaultMetadata{
						Origin:      protocol.FaultOriginHook,
						Disposition: protocol.FaultReject,
						SideEffects: protocol.SideEffectNone,
					},
					result.err,
				)
			}
			continue
		}
		m.executor.audit(ctx, MessageSubmit, hook.ID, inputKeys, nil, result, "succeeded", "")
	}
	return nil
}

func (m *Manager) ToolCallBefore(
	ctx context.Context,
	input ToolCallBeforeInput,
) (ToolCallBeforeResult, error) {
	current := bytes.Clone(input.Input)
	if len(current) == 0 {
		current = json.RawMessage(`{}`)
	}
	if !isJSONObject(current) {
		return ToolCallBeforeResult{}, errors.New("tool hook input must be a JSON object")
	}
	inputKeys := jsonObjectKeys(current)
	lastUpdater := ""
	updated := false
	for _, hook := range m.config.Hooks[ToolCallBefore] {
		hookInputKeys := append([]string(nil), inputKeys...)
		var outputKeys []string
		input.Input = current
		execution := m.executor.run(ctx, ToolCallBefore, hook, input)
		if hook.Mode == ModeObserve {
			outcome := "observed"
			if execution.err != nil || execution.exitCode != 0 ||
				execution.stdoutTruncated {
				outcome = "observed_failure"
			}
			m.executor.audit(
				ctx, ToolCallBefore, hook.ID, hookInputKeys, nil,
				execution, outcome, "",
			)
			continue
		}
		if execution.exitCode == 2 {
			result := ToolCallBeforeResult{Action: ActionDeny, HookID: hook.ID}
			m.executor.audit(
				ctx, ToolCallBefore, hook.ID, hookInputKeys, nil,
				execution, "denied", ActionDeny,
			)
			return result, nil
		}
		if execution.err != nil || execution.exitCode != 0 || execution.stdoutTruncated {
			if execution.errCode == "" {
				if execution.stdoutTruncated {
					execution.errCode = "output_truncated"
				} else {
					execution.errCode = "nonzero_exit"
				}
			}
			outcome := "blocked"
			if hook.ContinueOnError {
				outcome = "continued"
			}
			m.executor.audit(ctx, ToolCallBefore, hook.ID, hookInputKeys, nil, execution, outcome, "")
			if ctx.Err() != nil {
				return ToolCallBeforeResult{}, ctx.Err()
			}
			if hook.ContinueOnError {
				continue
			}
			return ToolCallBeforeResult{}, fmt.Errorf(
				"tool before hook %q failed (%s)", hook.ID, execution.errCode,
			)
		}
		response, err := decodeResponse(execution.stdout)
		if err != nil {
			execution.errCode = "invalid_response"
			outcome := "blocked"
			if hook.ContinueOnError {
				outcome = "continued"
			}
			m.executor.audit(ctx, ToolCallBefore, hook.ID, hookInputKeys, nil, execution, outcome, "")
			if hook.ContinueOnError {
				continue
			}
			return ToolCallBeforeResult{}, fmt.Errorf("tool before hook %q returned invalid JSON", hook.ID)
		}
		switch response.Decision {
		case "", ActionAllow:
		case ActionDeny, ActionAsk:
		default:
			execution.errCode = "invalid_decision"
			m.executor.audit(ctx, ToolCallBefore, hook.ID, hookInputKeys, nil, execution, "blocked", "")
			if hook.ContinueOnError {
				continue
			}
			return ToolCallBeforeResult{}, fmt.Errorf(
				"tool before hook %q returned invalid decision", hook.ID,
			)
		}
		if len(response.UpdatedInput) != 0 {
			if !isJSONObject(response.UpdatedInput) {
				execution.errCode = "invalid_updated_input"
				if response.Decision == ActionDeny || response.Decision == ActionAsk {
					var updatedInput json.RawMessage
					if updated {
						updatedInput = bytes.Clone(current)
					}
					result := ToolCallBeforeResult{
						Action: response.Decision, HookID: hook.ID, Reason: response.Reason,
						UpdatedInput: updatedInput,
					}
					m.executor.audit(
						ctx, ToolCallBefore, hook.ID, hookInputKeys, nil,
						execution, string(response.Decision), response.Decision,
					)
					return result, nil
				}
				m.executor.audit(
					ctx, ToolCallBefore, hook.ID, hookInputKeys, nil,
					execution, "blocked", "",
				)
				if hook.ContinueOnError {
					continue
				}
				return ToolCallBeforeResult{}, fmt.Errorf(
					"tool before hook %q returned invalid updatedInput", hook.ID,
				)
			}
			current = bytes.Clone(response.UpdatedInput)
			outputKeys = jsonObjectKeys(current)
			inputKeys = outputKeys
			lastUpdater = hook.ID
			updated = true
		}
		if response.Decision == ActionDeny || response.Decision == ActionAsk {
			var updatedInput json.RawMessage
			if updated {
				updatedInput = bytes.Clone(current)
			}
			result := ToolCallBeforeResult{
				Action: response.Decision, HookID: hook.ID, Reason: response.Reason,
				UpdatedInput: updatedInput,
			}
			m.executor.audit(
				ctx, ToolCallBefore, hook.ID, hookInputKeys, outputKeys,
				execution, string(response.Decision), response.Decision,
			)
			return result, nil
		}
		m.executor.audit(
			ctx, ToolCallBefore, hook.ID, hookInputKeys, outputKeys,
			execution, "allowed", ActionAllow,
		)
	}
	if !updated {
		current = nil
	}
	return ToolCallBeforeResult{
		Action: ActionAllow, HookID: lastUpdater, UpdatedInput: current,
	}, nil
}

// PermissionRequest runs before the interactive approval UI (N20).
// Untrusted hooks may deny or require review, but cannot widen an Ask into
// Allow. Only a builtin hook with builtin trust may return an allowing answer.
func (m *Manager) PermissionRequest(
	ctx context.Context,
	input ToolCallBeforeInput,
) (ToolCallBeforeResult, error) {
	if m == nil {
		return ToolCallBeforeResult{}, nil
	}
	hooks := m.config.Hooks[PermissionRequest]
	if len(hooks) == 0 {
		return ToolCallBeforeResult{}, nil
	}
	current := bytes.Clone(input.Input)
	if len(current) == 0 {
		current = json.RawMessage(`{}`)
	}
	inputKeys := jsonObjectKeys(current)
	sawAllow := false
	lastAllowID := ""
	lastAllowReason := ""
	for _, hook := range hooks {
		input.Input = current
		execution := m.executor.run(ctx, PermissionRequest, hook, input)
		if hook.Mode == ModeObserve {
			outcome := "observed"
			if execution.err != nil || execution.exitCode != 0 ||
				execution.stdoutTruncated {
				outcome = "observed_failure"
			}
			m.executor.audit(
				ctx, PermissionRequest, hook.ID, inputKeys, nil,
				execution, outcome, "",
			)
			continue
		}
		if execution.exitCode == 2 {
			m.executor.audit(
				ctx, PermissionRequest, hook.ID, inputKeys, nil,
				execution, "denied", ActionDeny,
			)
			return ToolCallBeforeResult{Action: ActionDeny, HookID: hook.ID}, nil
		}
		if execution.err != nil || execution.exitCode != 0 || execution.stdoutTruncated {
			if execution.errCode == "" {
				if execution.stdoutTruncated {
					execution.errCode = "output_truncated"
				} else {
					execution.errCode = "nonzero_exit"
				}
			}
			outcome := "blocked"
			if hook.ContinueOnError {
				outcome = "continued"
			}
			m.executor.audit(ctx, PermissionRequest, hook.ID, inputKeys, nil, execution, outcome, "")
			if ctx.Err() != nil {
				return ToolCallBeforeResult{}, ctx.Err()
			}
			if hook.ContinueOnError {
				continue
			}
			return ToolCallBeforeResult{}, fmt.Errorf(
				"permission hook %q failed (%s)", hook.ID, execution.errCode,
			)
		}
		response, err := decodeResponse(execution.stdout)
		if err != nil {
			execution.errCode = "invalid_response"
			outcome := "blocked"
			if hook.ContinueOnError {
				outcome = "continued"
			}
			m.executor.audit(ctx, PermissionRequest, hook.ID, inputKeys, nil, execution, outcome, "")
			if hook.ContinueOnError {
				continue
			}
			return ToolCallBeforeResult{}, fmt.Errorf("permission hook %q returned invalid JSON", hook.ID)
		}
		switch response.Decision {
		case ActionDeny:
			m.executor.audit(
				ctx, PermissionRequest, hook.ID, inputKeys, nil,
				execution, "denied", ActionDeny,
			)
			return ToolCallBeforeResult{
				Action: ActionDeny, HookID: hook.ID, Reason: response.Reason,
			}, nil
		case ActionAllow:
			if hook.Source != SourceBuiltin || hook.Trust != TrustBuiltin {
				m.executor.audit(
					ctx, PermissionRequest, hook.ID, inputKeys, nil,
					execution, "ask", ActionAsk,
				)
				continue
			}
			sawAllow = true
			lastAllowID = hook.ID
			lastAllowReason = response.Reason
			m.executor.audit(
				ctx, PermissionRequest, hook.ID, inputKeys, nil,
				execution, "allowed", ActionAllow,
			)
		case "", ActionAsk:
			m.executor.audit(
				ctx, PermissionRequest, hook.ID, inputKeys, nil,
				execution, "ask", ActionAsk,
			)
		default:
			execution.errCode = "invalid_decision"
			m.executor.audit(ctx, PermissionRequest, hook.ID, inputKeys, nil, execution, "blocked", "")
			if hook.ContinueOnError {
				continue
			}
			return ToolCallBeforeResult{}, fmt.Errorf(
				"permission hook %q returned invalid decision", hook.ID,
			)
		}
	}
	if sawAllow {
		return ToolCallBeforeResult{
			Action: ActionAllow, HookID: lastAllowID, Reason: lastAllowReason,
		}, nil
	}
	return ToolCallBeforeResult{Action: ActionAsk}, nil
}

func (m *Manager) ToolCallAfter(ctx context.Context, input ToolCallAfterInput) {
	m.runObservers(ctx, ToolCallAfter, input)
}

func (m *Manager) ShellEnv(ctx context.Context, input ShellEnvInput) map[string]string {
	environment := make(map[string]string)
	inputKeys := keysOf(input)
	for _, hook := range m.config.Hooks[ShellEnv] {
		execution := m.executor.run(ctx, ShellEnv, hook, input)
		if execution.err != nil || execution.exitCode != 0 || execution.stdoutTruncated {
			if execution.errCode == "" {
				if execution.stdoutTruncated {
					execution.errCode = "output_truncated"
				} else {
					execution.errCode = "nonzero_exit"
				}
			}
			m.executor.audit(ctx, ShellEnv, hook.ID, inputKeys, nil, execution, "ignored", "")
			if ctx.Err() != nil {
				break
			}
			continue
		}
		response, err := decodeResponse(execution.stdout)
		if err != nil {
			execution.errCode = "invalid_response"
			m.executor.audit(ctx, ShellEnv, hook.ID, inputKeys, nil, execution, "ignored", "")
			continue
		}
		keys := make([]string, 0, len(response.Environment))
		valid := true
		for name := range response.Environment {
			if !environmentName.MatchString(name) {
				valid = false
				break
			}
			keys = append(keys, name)
		}
		sort.Strings(keys)
		if !valid {
			execution.errCode = "invalid_environment_name"
			m.executor.audit(ctx, ShellEnv, hook.ID, inputKeys, nil, execution, "ignored", "")
			continue
		}
		for name, value := range response.Environment {
			environment[name] = value
		}
		m.executor.audit(ctx, ShellEnv, hook.ID, inputKeys, keys, execution, "applied", "")
	}
	return environment
}

func (m *Manager) TurnEnd(ctx context.Context, input TurnEndInput) {
	m.runObservers(ctx, TurnEnd, input)
}

// PreCompact runs before history compaction. A failing hook with
// ContinueOnError=false blocks compaction (W5.4).
func (m *Manager) PreCompact(ctx context.Context, input CompactInput) error {
	inputKeys := keysOf(input)
	for _, hook := range m.config.Hooks[PreCompact] {
		result := m.executor.run(ctx, PreCompact, hook, input)
		if result.err != nil || result.exitCode != 0 {
			outcome, code := "continued", result.errCode
			if code == "" {
				code = "nonzero_exit"
				result.errCode = code
			}
			if !hook.ContinueOnError {
				outcome = "blocked"
			}
			m.executor.audit(ctx, PreCompact, hook.ID, inputKeys, nil, result, outcome, "")
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !hook.ContinueOnError {
				return fmt.Errorf("pre-compact hook %q failed (%s)", hook.ID, code)
			}
			continue
		}
		m.executor.audit(ctx, PreCompact, hook.ID, inputKeys, nil, result, "succeeded", "")
	}
	return nil
}

// PostCompact observes successful compaction (W5.4).
func (m *Manager) PostCompact(ctx context.Context, input CompactInput) {
	m.runObservers(ctx, PostCompact, input)
}

func (m *Manager) runObservers(ctx context.Context, event Event, input any) {
	inputKeys := keysOf(input)
	for _, hook := range m.config.Hooks[event] {
		execution := m.executor.run(ctx, event, hook, input)
		outcome := "observed"
		if execution.err != nil || execution.exitCode != 0 {
			outcome = "ignored"
			if execution.errCode == "" {
				execution.errCode = "nonzero_exit"
			}
		}
		m.executor.audit(ctx, event, hook.ID, inputKeys, nil, execution, outcome, "")
		if ctx.Err() != nil {
			return
		}
	}
}

func decodeResponse(data []byte) (hookResponse, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return hookResponse{}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var response hookResponse
	if err := decoder.Decode(&response); err != nil {
		return hookResponse{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return hookResponse{}, errors.New("trailing JSON value")
	}
	return response, nil
}

func isJSONObject(data []byte) bool {
	var value map[string]json.RawMessage
	return json.Unmarshal(data, &value) == nil && value != nil
}

func keysOf(value any) []string {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return jsonObjectKeys(data)
}
