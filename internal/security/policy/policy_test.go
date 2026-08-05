package policy

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

func TestPolicyTruthTableAndDenyPrecedence(t *testing.T) {
	now := time.Unix(1000, 0)
	tests := []struct {
		name       string
		mode       Mode
		permission Permission
		tool       string
		wantCode   string
	}{
		{name: "plan read", mode: ModePlan, permission: PermissionSuggest, tool: "file_read"},
		{name: "plan write denied", mode: ModePlan, permission: PermissionBypass, tool: "file_write", wantCode: "mode_denied"},
		{name: "plan request_user_input", mode: ModePlan, permission: PermissionSuggest, tool: "request_user_input"},
		{name: "act auto write", mode: ModeAct, permission: PermissionAuto, tool: "file_write"},
		{name: "act auto process denied", mode: ModeAct, permission: PermissionAuto, tool: "shell_run", wantCode: "permission_denied"},
		{name: "operate auto process", mode: ModeOperate, permission: PermissionAuto, tool: "shell_run"},
		{name: "never write denied", mode: ModeAct, permission: PermissionNever, tool: "file_write", wantCode: "permission_denied"},
		{name: "unknown denied", mode: ModeAct, permission: PermissionBypass, tool: "future_tool", wantCode: "policy_unknown_capability"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := DefaultRuntime(test.mode, test.permission)
			runtime.Now = func() time.Time { return now }
			err := runtime.Authorize(t.Context(), invocation(test.tool, "call-1", `{}`))
			assertDecisionCode(t, err, test.wantCode)
		})
	}

	t.Run("operate auto network asks", func(t *testing.T) {
		runtime := DefaultRuntime(ModeOperate, PermissionAuto)
		call := invocation("file_read", "net-1", `{}`)
		call.Capability = CapabilityNetwork
		err := runtime.Authorize(t.Context(), call)
		assertDecisionCode(t, err, "approval_required")
	})
	t.Run("operate auto plugin asks", func(t *testing.T) {
		runtime := DefaultRuntime(ModeOperate, PermissionAuto)
		call := invocation("file_read", "plugin-1", `{}`)
		call.Capability = CapabilityPlugin
		err := runtime.Authorize(t.Context(), call)
		assertDecisionCode(t, err, "approval_required")
	})
	t.Run("act auto network denied", func(t *testing.T) {
		runtime := DefaultRuntime(ModeAct, PermissionAuto)
		call := invocation("file_read", "net-act", `{}`)
		call.Capability = CapabilityNetwork
		err := runtime.Authorize(t.Context(), call)
		assertDecisionCode(t, err, "permission_denied")
	})

	runtime := DefaultRuntime(ModeAct, PermissionBypass)
	runtime.Repository = []Rule{
		{Tool: "file_write", Resource: "protected", Action: ActionDeny},
		{Tool: "file_write", Resource: "protected", Action: ActionAllow},
	}
	err := runtime.Authorize(t.Context(), invocation("file_write", "call-2", `{"path":"protected/value"}`))
	assertDecisionCode(t, err, "repository_rule_denied")

	runtime.Repository = []Rule{{Tool: "file_write", Action: ActionHold, Code: "release_hold"}}
	err = runtime.Authorize(t.Context(), invocation("file_write", "call-3", `{"path":"value"}`))
	assertDecisionCode(t, err, "release_hold")

	runtime = DefaultRuntime(ModeAct, PermissionSuggest)
	runtime.Repository = []Rule{{Tool: "file_write", Resource: "notes.txt", Action: ActionAllow}}
	err = runtime.Authorize(t.Context(), invocation("file_write", "call-allow", `{"path":"notes.txt"}`))
	assertDecisionCode(t, err, "")

	runtime = DefaultRuntime(ModePlan, PermissionSuggest)
	runtime.Repository = []Rule{{Tool: "file_write", Resource: "notes.txt", Action: ActionAllow}}
	err = runtime.Authorize(t.Context(), invocation("file_write", "call-plan", `{"path":"notes.txt"}`))
	assertDecisionCode(t, err, "mode_denied")
}

func TestLifecycleGrantsForceAskUnderAutoAndBypass(t *testing.T) {
	for _, permission := range []Permission{PermissionAuto, PermissionBypass} {
		runtime := DefaultRuntime(ModeAct, permission)
		call := Invocation{
			CallID: "lifecycle-1", Tool: "task_cancel", Arguments: json.RawMessage(`{}`),
			Capability: CapabilityWrite, Validated: true,
			Resources: []tool.Resource{{Kind: "task", ID: "task-1", Access: tool.AccessWrite}},
		}
		err := runtime.Authorize(t.Context(), call)
		assertDecisionCode(t, err, "approval_required")
	}
	runtime := DefaultRuntime(ModeAct, PermissionAuto)
	err := runtime.Authorize(t.Context(), invocation("file_write", "normal-write", `{"path":"a.txt"}`))
	assertDecisionCode(t, err, "")
}

func TestCloneSamplingIsolatesModePermissionAndRules(t *testing.T) {
	parent := DefaultRuntime(ModeAct, PermissionSuggest)
	parent.Repository = []Rule{{Tool: "write", Resource: "*", Action: ActionAsk}}
	clone := parent.CloneSampling()
	if clone == nil || clone == parent {
		t.Fatal("clone must be a distinct runtime")
	}
	if clone.Approvals != parent.Approvals {
		t.Fatal("approvals cache should be shared")
	}
	parent.Mode = ModePlan
	parent.Permission = PermissionBypass
	parent.Repository[0].Action = ActionDeny
	if clone.Mode != ModeAct || clone.Permission != PermissionSuggest {
		t.Fatalf("clone mode/permission = %s/%s", clone.Mode, clone.Permission)
	}
	if clone.Repository[0].Action != ActionAsk {
		t.Fatal("repository rules must be copied")
	}
}

func TestPolicyCompleteModePermissionCapabilityTruthTable(t *testing.T) {
	modes := []Mode{ModePlan, ModeAct, ModeOperate}
	permissions := []Permission{
		PermissionSuggest, PermissionAuto, PermissionBypass, PermissionNever,
	}
	capabilities := []Capability{
		CapabilityRead, CapabilityWrite, CapabilityProcess, CapabilityNetwork, CapabilityPlugin,
	}
	for _, mode := range modes {
		for _, permission := range permissions {
			for _, capability := range capabilities {
				name := string(mode) + "/" + string(permission) + "/" + string(capability)
				t.Run(name, func(t *testing.T) {
					runtime := DefaultRuntime(mode, permission)
					call := invocation("file_read", "truth-table", `{}`)
					call.Capability = capability
					err := runtime.Authorize(t.Context(), call)
					want := ""
					switch {
					case mode == ModePlan && capability != CapabilityRead:
						want = "mode_denied"
					case permission == PermissionSuggest && capability != CapabilityRead:
						want = "approval_required"
					case permission == PermissionAuto && mode == ModeOperate:
						switch capability {
						case CapabilityRead, CapabilityWrite, CapabilityProcess:
							want = ""
						case CapabilityNetwork, CapabilityPlugin:
							want = "approval_required"
						default:
							want = "permission_denied"
						}
					case permission == PermissionAuto &&
						capability != CapabilityRead && capability != CapabilityWrite:
						want = "permission_denied"
					case permission == PermissionNever && capability != CapabilityRead:
						want = "permission_denied"
					}
					assertDecisionCode(t, err, want)
				})
			}
		}
	}
}

func TestRepositoryCommandRulesInspectEveryShellSegment(t *testing.T) {
	for _, command := range []string{
		"rm target",
		"echo safe; rm target",
		"echo safe && rm target",
		"echo safe || rm target",
		"printf safe | rm target",
		"echo safe\nrm target",
	} {
		runtime := DefaultRuntime(ModeAct, PermissionBypass)
		runtime.Repository = []Rule{
			{Tool: "shell_run", CommandPrefix: "rm", Action: ActionDeny},
		}
		raw, err := json.Marshal(map[string]string{"command": command})
		if err != nil {
			t.Fatal(err)
		}
		err = runtime.Authorize(t.Context(), invocation("shell_run", command, string(raw)))
		assertDecisionCode(t, err, "repository_rule_denied")
	}

	runtime := DefaultRuntime(ModeAct, PermissionBypass)
	runtime.Repository = []Rule{
		{Tool: "shell_run", CommandPrefix: "rm", Action: ActionDeny},
	}
	err := runtime.Authorize(t.Context(), invocation(
		"shell_run", "quoted", `{"command":"printf '%s' 'echo safe; rm target'"}`,
	))
	assertDecisionCode(t, err, "")
}

func TestToolGrantMissingAndDenyCannotBeOverridden(t *testing.T) {
	runtime := DefaultRuntime(ModeAct, PermissionBypass)
	runtime.Grants = []Rule{{Tool: "file_read", Action: ActionAllow}}
	err := runtime.Authorize(t.Context(), invocation("file_write", "call-1", `{"path":"a"}`))
	assertDecisionCode(t, err, "tool_grant_missing")

	runtime.Grants = []Rule{
		{Tool: "file_write", Resource: "*", Action: ActionAllow},
		{Tool: "file_write", Resource: "a", Action: ActionDeny},
	}
	err = runtime.Authorize(t.Context(), invocation("file_write", "call-2", `{"path":"a"}`))
	assertDecisionCode(t, err, "tool_grant_denied")
}

func TestApprovalIsBoundToCallArgumentsResourcesScopeAndExpiry(t *testing.T) {
	now := time.Unix(2000, 0)
	base := invocation("shell_run", "call-1", `{"cwd":".","command":"go test ./..."}`)
	request, err := NewApprovalRequest(base, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	reordered, err := NewApprovalRequest(invocation(
		"shell_run", "call-1", `{"command":"go test ./...","cwd":"."}`,
	), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if request.Fingerprint != reordered.Fingerprint {
		t.Fatalf("canonical fingerprints differ: %s != %s", request.Fingerprint, reordered.Fingerprint)
	}
	differentExpiry, err := NewApprovalRequest(base, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if request.Fingerprint == differentExpiry.Fingerprint {
		t.Fatal("expiry change retained approval fingerprint")
	}

	cache := NewApprovalCache()
	if err := cache.Add(request, ApprovalOnce); err != nil {
		t.Fatal(err)
	}
	if !cache.Match(reordered, now) || cache.Match(reordered, now) {
		t.Fatal("once approval was not consumed exactly once")
	}
	sessionRequest, err := NewApprovalRequestForScope(base, ApprovalSession, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Add(sessionRequest, ApprovalSession); err != nil {
		t.Fatal(err)
	}
	if !cache.Match(sessionRequest, now) || !cache.Match(sessionRequest, now.Add(30*time.Second)) {
		t.Fatal("session approval was not reusable before expiry")
	}
	if cache.Match(sessionRequest, now.Add(2*time.Minute)) {
		t.Fatal("expired approval matched")
	}

	for _, changed := range []Invocation{
		invocation("shell_run", "call-1", `{"cwd":".","command":"rm -rf ."}`),
		invocation("file_write", "call-1", `{"path":"."}`),
	} {
		other, err := NewApprovalRequest(changed, now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if other.Fingerprint == request.Fingerprint {
			t.Fatalf("changed invocation retained fingerprint: %+v", changed)
		}
	}
}

func TestSuggestApprovalRequiresAsyncHost(t *testing.T) {
	runtime := DefaultRuntime(ModeAct, PermissionSuggest)
	call := invocation("file_write", "call-approval", `{"path":"a","content":"x"}`)
	assertDecisionCode(t, runtime.Authorize(t.Context(), call), "approval_required")
}

func TestApprovalCacheIsBoundedAndEvictsOldest(t *testing.T) {
	now := time.Unix(4000, 0)
	cache := NewApprovalCacheWithLimit(2)
	requests := make([]ApprovalRequest, 3)
	for index := range requests {
		request, err := NewApprovalRequestForScope(
			invocation(
				"file_write", string(rune('a'+index)),
				`{"path":"`+string(rune('a'+index))+`"}`,
			),
			ApprovalSession,
			now.Add(time.Hour),
		)
		if err != nil {
			t.Fatal(err)
		}
		requests[index] = request
		if err := cache.Add(request, ApprovalSession); err != nil {
			t.Fatal(err)
		}
	}
	if len(cache.entries) != 2 {
		t.Fatalf("cache size = %d, want 2", len(cache.entries))
	}
	if cache.Match(requests[0], now) {
		t.Fatal("oldest approval remained after bounded eviction")
	}
	if !cache.Match(requests[1], now) || !cache.Match(requests[2], now) {
		t.Fatal("new approvals were unexpectedly evicted")
	}
}

func invocation(toolName, callID, arguments string) Invocation {
	raw := json.RawMessage(arguments)
	capability := map[string]Capability{
		"file_read": CapabilityRead, "file_write": CapabilityWrite,
		"shell_run": CapabilityProcess, "request_user_input": CapabilityRead,
		"update_plan": CapabilityWrite,
	}[toolName]
	var resources []tool.Resource
	var value struct {
		Path string `json:"path"`
		CWD  string `json:"cwd"`
	}
	_ = json.Unmarshal(raw, &value)
	if value.Path != "" {
		resources = append(resources, tool.Resource{
			Kind: "file", Path: value.Path, Access: tool.AccessWrite,
		})
	}
	if value.CWD != "" {
		resources = append(resources, tool.Resource{
			Kind: "repo", Path: value.CWD, Access: tool.AccessWrite, Tree: true,
		})
	}
	return Invocation{
		CallID: callID, Tool: toolName, Arguments: raw,
		Resources: resources, Capability: capability, Validated: true,
	}
}

func assertDecisionCode(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("Authorize() error = %v", err)
		}
		return
	}
	var decision *DecisionError
	if !errors.As(err, &decision) || decision.Code != want {
		t.Fatalf("Authorize() error = %v, want code %s", err, want)
	}
}
