package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	agenttool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/agent"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/handle"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/rlm"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

type recordingGate struct {
	calls int
}

func (g *recordingGate) Execute(
	_ context.Context, _, name string, _ json.RawMessage,
) (tool.Result, error) {
	g.calls++
	return tool.Result{Content: "gated:" + name}, nil
}

type dualRuntime struct {
	turns []string
}

func (r *dualRuntime) StartTurn(_ context.Context, agentID, prompt string) (string, error) {
	turn := "child-turn:" + agentID + ":" + prompt
	r.turns = append(r.turns, turn)
	return turn, nil
}

func (r *dualRuntime) CancelTurn(context.Context, string, string) error { return nil }

func TestAgentSpawnReceiptAndHandleRead(t *testing.T) {
	root := t.TempDir()
	handles := handle.NewStore()
	gate := &recordingGate{}
	runtime := &dualRuntime{}
	registry := tool.NewRegistry(nil, nil)
	if err := handle.Register(registry, handles); err != nil {
		t.Fatal(err)
	}
	if err := agenttool.Register(registry, agenttool.Options{
		Handles: handles, SessionID: "session-1", Root: root, Gate: gate,
		Governor: rlm.NewGovernor(rlm.Limits{MaxDepth: 3, MaxConcurrency: 4}),
		Runtime:  runtime, Budget: subagent.Budget{MaxDepth: 3, MaxParallel: 4},
	}); err != nil {
		t.Fatal(err)
	}

	parent := execute(t, registry, "agent", map[string]any{
		"prompt": "parent work", "role": "general",
	})
	var parentBody map[string]any
	if err := json.Unmarshal([]byte(parent.Content), &parentBody); err != nil {
		t.Fatal(err)
	}
	parentID, _ := parentBody["agent_id"].(string)
	threadID, _ := parentBody["thread_id"].(string)
	if parentID == "" || !strings.HasPrefix(threadID, "thread-") {
		t.Fatalf("parent = %+v", parentBody)
	}
	runReceipt, _ := parentBody["run_receipt"].(map[string]any)
	usage, _ := runReceipt["usage"].(map[string]any)
	verification, _ := runReceipt["verification"].(map[string]any)
	// Spawn only submits the child turn; usage and verification are pending until
	// the child settles with a real result.
	if usage["status"] != "pending" || verification["status"] != "pending" {
		t.Fatalf("run_receipt = %+v", runReceipt)
	}
	if len(runtime.turns) != 1 {
		t.Fatalf("parent turns = %#v", runtime.turns)
	}

	child := execute(t, registry, "agent", map[string]any{
		"prompt": "child work", "role": "reviewer", "parent_id": parentID,
	})
	var childBody map[string]any
	if err := json.Unmarshal([]byte(child.Content), &childBody); err != nil {
		t.Fatal(err)
	}
	if childBody["depth"] != float64(1) || childBody["parent_id"] != parentID {
		t.Fatalf("child = %+v", childBody)
	}
	if childBody["role"] != "review" || childBody["stance"] != "read_only" {
		t.Fatalf("child role/stance = %+v", childBody)
	}
	if len(runtime.turns) != 2 {
		t.Fatalf("dual turns = %#v", runtime.turns)
	}

	handleRaw, err := json.Marshal(childBody["transcript_handle"])
	if err != nil {
		t.Fatal(err)
	}
	read := execute(t, registry, "handle_read", map[string]any{
		"handle": json.RawMessage(handleRaw), "mode": "head", "max_bytes": 512,
	})
	if !strings.Contains(read.Content, "agent_id=") || !strings.Contains(read.Content, "child work") {
		t.Fatalf("handle_read = %+v", read)
	}

	receipt, _ := childBody["receipt"].(map[string]any)
	if receipt["sequence"] != float64(2) || receipt["to"] != parentID {
		t.Fatalf("receipt = %+v", receipt)
	}
}

func TestAgentForkContextInheritsParentMarker(t *testing.T) {
	root := t.TempDir()
	handles := handle.NewStore()
	runtime := &dualRuntime{}
	registry := tool.NewRegistry(nil, nil)
	if err := handle.Register(registry, handles); err != nil {
		t.Fatal(err)
	}
	if err := agenttool.Register(registry, agenttool.Options{
		Handles: handles, SessionID: "session-1", Root: root, Gate: &recordingGate{},
		Governor: rlm.NewGovernor(rlm.Limits{}), Runtime: runtime,
		ParentContext: func() string { return "PARENT_MARKER_ALPHA decisions=keep-plan" },
	}); err != nil {
		t.Fatal(err)
	}
	forked := execute(t, registry, "agent", map[string]any{
		"prompt": "continue review", "role": "explore", "fork_context": true,
	})
	var body map[string]any
	if err := json.Unmarshal([]byte(forked.Content), &body); err != nil {
		t.Fatal(err)
	}
	if body["fork_context"] != true || body["fork_context_empty"] != false {
		t.Fatalf("fork flags = %+v", body)
	}
	handleRaw, err := json.Marshal(body["transcript_handle"])
	if err != nil {
		t.Fatal(err)
	}
	read := execute(t, registry, "handle_read", map[string]any{
		"handle": json.RawMessage(handleRaw), "mode": "head", "max_bytes": 2048,
	})
	if !strings.Contains(read.Content, "PARENT_MARKER_ALPHA") ||
		!strings.Contains(read.Content, "fork_context=true") {
		t.Fatalf("fork transcript = %s", read.Content)
	}
	if len(runtime.turns) != 1 || !strings.Contains(runtime.turns[0], "PARENT_MARKER_ALPHA") {
		t.Fatalf("runtime turns = %#v", runtime.turns)
	}

	fresh := execute(t, registry, "agent", map[string]any{
		"prompt": "fresh explore", "role": "explore",
	})
	var freshBody map[string]any
	_ = json.Unmarshal([]byte(fresh.Content), &freshBody)
	freshHandle, _ := json.Marshal(freshBody["transcript_handle"])
	freshRead := execute(t, registry, "handle_read", map[string]any{
		"handle": json.RawMessage(freshHandle), "mode": "head", "max_bytes": 2048,
	})
	if strings.Contains(freshRead.Content, "PARENT_MARKER_ALPHA") {
		t.Fatalf("fresh child should not inherit: %s", freshRead.Content)
	}
}

func TestAgentUnsupportedRoleFailClosed(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := agenttool.Register(registry, agenttool.Options{
		Handles: handle.NewStore(), SessionID: "s", Root: t.TempDir(), Gate: &recordingGate{},
		Governor: rlm.NewGovernor(rlm.Limits{}),
	}); err != nil {
		t.Fatal(err)
	}
	_, err := registry.Execute(t.Context(), tool.Call{
		Name: "agent", Arguments: mustJSON(map[string]any{
			"prompt": "x", "role": "nope",
		}), Authorized: true,
	})
	if err == nil {
		t.Fatal("expected unsupported role rejection")
	}
	if !strings.Contains(err.Error(), "role") && !strings.Contains(err.Error(), "jsonschema") {
		t.Fatalf("err = %v", err)
	}
}

func TestAgentDepthAndConcurrencyFailClosed(t *testing.T) {
	root := t.TempDir()
	handles := handle.NewStore()
	registry := tool.NewRegistry(nil, nil)
	if err := agenttool.Register(registry, agenttool.Options{
		Handles: handles, SessionID: "session-1", Root: root, Gate: &recordingGate{},
		Governor: rlm.NewGovernor(rlm.Limits{MaxDepth: 0, MaxConcurrency: 1}),
		Budget:   subagent.Budget{MaxDepth: 0, MaxParallel: 1},
	}); err != nil {
		t.Fatal(err)
	}
	first := execute(t, registry, "agent", map[string]any{"prompt": "one"})
	var body map[string]any
	_ = json.Unmarshal([]byte(first.Content), &body)
	parentID, _ := body["agent_id"].(string)
	_, err := registry.Execute(t.Context(), tool.Call{
		Name: "agent", Arguments: mustJSON(map[string]any{
			"prompt": "too-deep", "parent_id": parentID,
		}), Authorized: true,
	})
	if err == nil {
		t.Fatal("expected depth fail-closed")
	}

	registry2 := tool.NewRegistry(nil, nil)
	handles2 := handle.NewStore()
	if err := agenttool.Register(registry2, agenttool.Options{
		Handles: handles2, SessionID: "session-1", Root: t.TempDir(), Gate: &recordingGate{},
		Governor: rlm.NewGovernor(rlm.Limits{MaxDepth: 2, MaxConcurrency: 1}),
		Budget:   subagent.Budget{MaxDepth: 2, MaxParallel: 1},
	}); err != nil {
		t.Fatal(err)
	}
	_ = execute(t, registry2, "agent", map[string]any{"prompt": "hold"})
	_, err = registry2.Execute(t.Context(), tool.Call{
		Name: "agent", Arguments: mustJSON(map[string]any{"prompt": "overflow"}), Authorized: true,
	})
	if err == nil {
		t.Fatal("expected concurrency fail-closed")
	}
}

func TestAgentWorktreeCleanupLeavesSibling(t *testing.T) {
	root := t.TempDir()
	handles := handle.NewStore()
	gate := &recordingGate{}
	manager, err := subagent.Open(subagent.Options{
		Root: root, Gate: gate, Budget: subagent.Budget{MaxParallel: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry(nil, nil)
	if err := agenttool.Register(registry, agenttool.Options{
		Manager: manager, Handles: handles, SessionID: "session-1",
		Governor: rlm.NewGovernor(rlm.Limits{}),
	}); err != nil {
		t.Fatal(err)
	}
	a := execute(t, registry, "agent", map[string]any{"prompt": "a"})
	b := execute(t, registry, "agent", map[string]any{"prompt": "b"})
	var bodyA, bodyB map[string]any
	_ = json.Unmarshal([]byte(a.Content), &bodyA)
	_ = json.Unmarshal([]byte(b.Content), &bodyB)
	idA, _ := bodyA["agent_id"].(string)
	pathB, _ := bodyB["worktree"].(string)
	if err := manager.Close(idA); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(pathB, ".codehelper-worktree")
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) == "" {
		t.Fatal("sibling worktree marker missing after cleanup")
	}
}

func TestAgentToolGateForced(t *testing.T) {
	root := t.TempDir()
	handles := handle.NewStore()
	gate := &recordingGate{}
	manager, err := subagent.Open(subagent.Options{Root: root, Gate: gate})
	if err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry(nil, nil)
	if err := agenttool.Register(registry, agenttool.Options{
		Manager: manager, Handles: handles, SessionID: "session-1",
		Governor: rlm.NewGovernor(rlm.Limits{}),
	}); err != nil {
		t.Fatal(err)
	}
	created := execute(t, registry, "agent", map[string]any{"prompt": "gate"})
	var body map[string]any
	_ = json.Unmarshal([]byte(created.Content), &body)
	id, _ := body["agent_id"].(string)
	result, err := manager.ExecuteTool(t.Context(), id, "call-1", "shell_run", json.RawMessage(`{"command":"true"}`))
	if err != nil {
		t.Fatal(err)
	}
	if gate.calls != 1 || result.Content != "gated:shell_run" {
		t.Fatalf("gate calls=%d result=%+v", gate.calls, result)
	}
}

func TestAgentAskFailClosedWithoutApprovalHost(t *testing.T) {
	root := t.TempDir()
	handles := handle.NewStore()
	registry := tool.NewRegistry(nil, nil)
	if err := agenttool.Register(registry, agenttool.Options{
		Handles: handles, SessionID: "session-1", Root: root, Gate: &recordingGate{},
		Governor: rlm.NewGovernor(rlm.Limits{}),
	}); err != nil {
		t.Fatal(err)
	}
	guard, err := toolguard.New(toolguard.Options{
		Registry: registry, Policy: policy.DefaultRuntime(policy.ModeAct, policy.PermissionAuto),
		Workspace: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = guard.Execute(t.Context(), "call-1", "agent", mustJSON(map[string]any{
		"prompt": "needs approval",
	}))
	var decision *policy.DecisionError
	if !errors.As(err, &decision) || decision.Code != "approval_host_unavailable" {
		t.Fatalf("err = %v", err)
	}
}

func TestAgentSpawnWaitCloseHermetic(t *testing.T) {
	root := t.TempDir()
	handles := handle.NewStore()
	gate := &recordingGate{}
	manager, err := subagent.Open(subagent.Options{
		Root: root, Gate: gate, Runtime: &dualRuntime{},
		Budget: subagent.Budget{MaxDepth: 3, MaxParallel: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry(nil, nil)
	if err := handle.Register(registry, handles); err != nil {
		t.Fatal(err)
	}
	if err := agenttool.Register(registry, agenttool.Options{
		Manager: manager, Handles: handles, SessionID: "session-1",
		Governor: rlm.NewGovernor(rlm.Limits{}),
	}); err != nil {
		t.Fatal(err)
	}

	spawned := execute(t, registry, "agent", map[string]any{
		"prompt": "hermetic work", "role": "explore",
	})
	var body map[string]any
	if err := json.Unmarshal([]byte(spawned.Content), &body); err != nil {
		t.Fatal(err)
	}
	agentID, _ := body["agent_id"].(string)
	worktree, _ := body["worktree"].(string)
	if agentID == "" || worktree == "" {
		t.Fatalf("spawn = %+v", body)
	}

	listed := execute(t, registry, "agent_list", map[string]any{})
	var listBody map[string]any
	_ = json.Unmarshal([]byte(listed.Content), &listBody)
	if listBody["count"] != float64(1) {
		t.Fatalf("list = %+v", listBody)
	}

	waitErr := make(chan error, 1)
	waitResult := make(chan tool.Result, 1)
	go func() {
		result, err := registry.Execute(context.Background(), tool.Call{
			Name: "agent_wait", Arguments: mustJSON(map[string]any{
				"agent_ids": []string{agentID},
			}), Authorized: true,
		})
		waitResult <- result
		waitErr <- err
	}()
	time.Sleep(20 * time.Millisecond)
	if err := manager.Complete(agentID, "done"); err != nil {
		t.Fatal(err)
	}
	if err := <-waitErr; err != nil {
		t.Fatal(err)
	}
	waited := <-waitResult
	var waitBody map[string]any
	if err := json.Unmarshal([]byte(waited.Content), &waitBody); err != nil {
		t.Fatal(err)
	}
	if waitBody["timed_out"] != false {
		t.Fatalf("wait = %+v", waitBody)
	}
	agents, _ := waitBody["agents"].([]any)
	if len(agents) != 1 {
		t.Fatalf("wait agents = %+v", waitBody)
	}
	first, _ := agents[0].(map[string]any)
	if first["status"] != "completed" {
		t.Fatalf("wait agent = %+v", first)
	}

	closed := execute(t, registry, "agent_close", map[string]any{"agent_id": agentID})
	var closeBody map[string]any
	_ = json.Unmarshal([]byte(closed.Content), &closeBody)
	if closeBody["closed"] != true || closeBody["status"] != "shutdown" {
		t.Fatalf("close = %+v", closeBody)
	}
	if _, err := os.Stat(filepath.Join(worktree, ".codehelper-worktree")); !os.IsNotExist(err) {
		t.Fatalf("worktree should be removed after close: %v", err)
	}
	after := execute(t, registry, "agent_list", map[string]any{})
	var afterBody map[string]any
	_ = json.Unmarshal([]byte(after.Content), &afterBody)
	if afterBody["count"] != float64(0) {
		t.Fatalf("list after close = %+v", afterBody)
	}
}

func TestUnifiedAgentCloseCompatibility(t *testing.T) {
	root := t.TempDir()
	handles := handle.NewStore()
	manager, err := subagent.Open(subagent.Options{
		Root: root, Gate: &recordingGate{}, Runtime: &dualRuntime{},
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry(nil, nil)
	if err := agenttool.Register(registry, agenttool.Options{
		Manager: manager, Handles: handles, SessionID: "session-1",
		Governor: rlm.NewGovernor(rlm.Limits{}),
	}); err != nil {
		t.Fatal(err)
	}
	spawned := execute(t, registry, "agent", map[string]any{
		"op": "spawn", "prompt": "compatibility child",
	})
	var body map[string]any
	if err := json.Unmarshal([]byte(spawned.Content), &body); err != nil {
		t.Fatal(err)
	}
	agentID, _ := body["agent_id"].(string)
	if agentID == "" {
		t.Fatalf("spawn = %+v", body)
	}

	closed := execute(t, registry, "agent", map[string]any{
		"op": "close", "agent_id": agentID,
	})
	var closeBody map[string]any
	if err := json.Unmarshal([]byte(closed.Content), &closeBody); err != nil {
		t.Fatal(err)
	}
	if closeBody["closed"] != true || closeBody["agent_id"] != agentID {
		t.Fatalf("close = %+v", closeBody)
	}

	_, err = registry.Execute(context.Background(), tool.Call{
		Name: "agent", Arguments: mustJSON(map[string]any{"op": "close"}), Authorized: true,
	})
	if !errors.Is(err, tool.ErrInvalidArguments) {
		t.Fatalf("missing agent_id error = %v, want invalid arguments", err)
	}
}

func TestAgentWaitDefersSerializedChildUntilCallingTurnEnds(t *testing.T) {
	root := t.TempDir()
	manager, err := subagent.Open(subagent.Options{
		Root: root, Gate: &recordingGate{}, Runtime: &dualRuntime{},
		Worktrees: fixedWorktrees{path: root, serialized: true},
		Budget:    subagent.Budget{MaxDepth: 3, MaxParallel: 4},
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry(nil, nil)
	if err := agenttool.Register(registry, agenttool.Options{
		Manager: manager, Handles: handle.NewStore(), SessionID: "session-1",
		Governor: rlm.NewGovernor(rlm.Limits{}),
	}); err != nil {
		t.Fatal(err)
	}
	spawned := execute(t, registry, "agent", map[string]any{
		"prompt": "edit host workspace", "role": "implementer",
	})
	var spawnBody map[string]any
	if err := json.Unmarshal([]byte(spawned.Content), &spawnBody); err != nil {
		t.Fatal(err)
	}
	agentID, _ := spawnBody["agent_id"].(string)
	if spawnBody["serialized"] != true || agentID == "" {
		t.Fatalf("spawn = %+v", spawnBody)
	}
	started := time.Now()
	waited := execute(t, registry, "agent_wait", map[string]any{
		"agent_ids": []string{agentID}, "timeout_ms": 10_000,
	})
	if time.Since(started) > time.Second {
		t.Fatal("serialized agent_wait blocked while caller held the workspace")
	}
	var waitBody map[string]any
	if err := json.Unmarshal([]byte(waited.Content), &waitBody); err != nil {
		t.Fatal(err)
	}
	if waitBody["deferred"] != true || waitBody["timed_out"] != false {
		t.Fatalf("wait = %+v", waitBody)
	}
}

func TestAgentInterruptFollowUpViaTools(t *testing.T) {
	root := t.TempDir()
	handles := handle.NewStore()
	runtime := &dualRuntime{}
	manager, err := subagent.Open(subagent.Options{
		Root: root, Gate: &recordingGate{}, Runtime: runtime,
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry(nil, nil)
	if err := agenttool.Register(registry, agenttool.Options{
		Manager: manager, Handles: handles, SessionID: "session-1",
		Governor: rlm.NewGovernor(rlm.Limits{}),
	}); err != nil {
		t.Fatal(err)
	}
	spawned := execute(t, registry, "agent", map[string]any{"prompt": "run"})
	var body map[string]any
	_ = json.Unmarshal([]byte(spawned.Content), &body)
	agentID, _ := body["agent_id"].(string)

	interrupted := execute(t, registry, "agent_interrupt", map[string]any{"agent_id": agentID})
	var interruptBody map[string]any
	_ = json.Unmarshal([]byte(interrupted.Content), &interruptBody)
	if interruptBody["status"] != "interrupted" || interruptBody["previous_status"] != "running" {
		t.Fatalf("interrupt = %+v", interruptBody)
	}
	follow := execute(t, registry, "agent_followup", map[string]any{
		"agent_id": agentID, "prompt": "resume please",
	})
	var followBody map[string]any
	_ = json.Unmarshal([]byte(follow.Content), &followBody)
	if followBody["follow_up"] != true || followBody["status"] != "running" {
		t.Fatalf("followup = %+v", followBody)
	}
	if len(runtime.turns) != 2 {
		t.Fatalf("turns = %#v", runtime.turns)
	}
}

func execute(t *testing.T, registry *tool.Registry, name string, input map[string]any) tool.Result {
	t.Helper()
	result, err := registry.Execute(t.Context(), tool.Call{
		Name: name, Arguments: mustJSON(input), Authorized: true,
	})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}

func mustJSON(value map[string]any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
