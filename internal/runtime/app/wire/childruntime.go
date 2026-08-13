package wire

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/rlm"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// childRuntime runs spawned agents as first-class runtime turns on their own
// threads. It deliberately does not shortcut around Runtime: a child
// turn goes through Submit, so every tool call, approval and receipt it produces
// is an ordinary event that the eventlog, replay and SSE already carry.
//
// It is constructed before the Runtime exists — the agent tool has to be
// registered while the tool registry is still being built — and bound afterwards.
type childRuntime struct {
	limits config.Subagent
	root   string
	// governor is the fleet-wide ledger: the per-child Engine budget stops one
	// runaway child mid-turn, this stops all children together once the session
	// pot is spent. Real numbers only exist after a turn produces a receipt, so
	// admission reads the ledger and settlement charges it.
	governor *rlm.Governor
	// tools owns the isolated tool planes; a closed child's is dropped here
	// because this is where a child's lifetime ends.
	tools *childToolsets

	mu      sync.Mutex
	runtime *app.Runtime
	threads *app.ThreadManager
	manager *subagent.AgentControl
	turns   map[protocol.ThreadID]*childTurn
	bound   bool

	pumpOnce sync.Once
	stop     context.CancelFunc
	done     chan struct{}
}

// childTurn accumulates what a child turn observed until its terminal event
// says how to settle it.
type childTurn struct {
	agentID        string
	turnID         protocol.TurnID
	startOperation protocol.OperationID
	started        bool
	receipt        *protocol.ExecutionReceiptData
	verify         *protocol.TurnVerificationData
	text           string
	notes          []string
	deadline       context.CancelFunc
	timedOut       bool
	startedAt      time.Time
	lease          rlm.Lease
	leased         bool
}

func newChildRuntime(
	limits config.Subagent, workspace string, governor *rlm.Governor, tools *childToolsets,
) *childRuntime {
	if governor == nil {
		governor = rlm.NewGovernor(rlm.Limits{})
	}
	return &childRuntime{
		limits: limits, root: workspace, governor: governor, tools: tools,
		turns: make(map[protocol.ThreadID]*childTurn),
		done:  make(chan struct{}),
	}
}

func newChildGovernor(limits config.Subagent) *rlm.Governor {
	return rlm.NewGovernor(rlm.Limits{
		MaxTokens: limits.MaxTokens, MaxCostUSD: limits.MaxCostUSD,
		MaxDepth: limits.MaxDepth, MaxConcurrency: limits.MaxParallel,
	})
}

func childOrchestrationRoot(state *buildState) string {
	if state.options.PersistentStore != nil {
		return filepath.Join(
			state.options.PersistentStore.Root(),
			"orchestration",
		)
	}
	return filepath.Join(state.config.execution.Workspace, ".codehelper")
}

// bind attaches the pieces that only exist once the Runtime is constructed.
func (c *childRuntime) bind(
	runtime *app.Runtime, threads *app.ThreadManager, manager *subagent.AgentControl,
) error {
	c.mu.Lock()
	c.runtime = runtime
	c.threads = threads
	c.manager = manager
	c.bound = runtime != nil && threads != nil && manager != nil
	bound := c.bound
	c.mu.Unlock()
	if !bound {
		return errors.New("child runtime dependencies are incomplete")
	}
	var recovered []struct {
		threadID protocol.ThreadID
		turnID   protocol.TurnID
	}
	for _, agent := range manager.List(subagent.ListFilter{}) {
		switch agent.Status {
		case subagent.StatusRequested, subagent.StatusStarting,
			subagent.StatusRunning, subagent.StatusWaiting:
		default:
			continue
		}
		threadID := protocol.ThreadID(agent.ThreadID)
		if _, registered := threads.ChildSpecFor(threadID); !registered {
			spec, err := c.specFor(agent)
			if err != nil {
				return fmt.Errorf("restore child authority for %s: %w", agent.ID, err)
			}
			if err := threads.RegisterChild(threadID, spec); err != nil {
				return fmt.Errorf("restore child thread %s: %w", threadID, err)
			}
		}
		if agent.TurnID == "" ||
			(agent.Status != subagent.StatusStarting &&
				agent.Status != subagent.StatusRunning &&
				agent.Status != subagent.StatusWaiting) {
			continue
		}
		turnID := protocol.TurnID(agent.TurnID)
		c.mu.Lock()
		if _, tracked := c.turns[threadID]; !tracked {
			c.turns[threadID] = &childTurn{
				agentID: agent.ID, turnID: turnID, startedAt: time.Now(),
			}
			recovered = append(recovered, struct {
				threadID protocol.ThreadID
				turnID   protocol.TurnID
			}{threadID: threadID, turnID: turnID})
		}
		c.mu.Unlock()
	}
	if len(recovered) > 0 {
		// Runtime has not started replaying interrupted operations yet. Subscribe
		// now so approval and terminal events from recovery cannot pass between
		// durable graph hydration and child bookkeeping.
		c.ensurePump(context.Background())
		for _, turn := range recovered {
			c.armDeadline(turn.threadID, turn.turnID)
		}
	}
	return nil
}

func (c *childRuntime) close() {
	c.mu.Lock()
	stop := c.stop
	for _, turn := range c.turns {
		if turn.deadline != nil {
			turn.deadline()
		}
		if turn.leased {
			c.governor.Release(turn.lease)
			turn.leased = false
		}
	}
	c.mu.Unlock()
	if stop == nil {
		return
	}
	stop()
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
	}
}

// StartTurn submits a real turn for the child agent and returns as soon as the
// runtime accepted it. Blocking until the child finishes would make wait_agent
// pointless and would deadlock the parent turn that called the agent tool.
func (c *childRuntime) StartTurn(ctx context.Context, agentID, prompt string) (string, error) {
	c.mu.Lock()
	runtime, threads, manager, bound := c.runtime, c.threads, c.manager, c.bound
	c.mu.Unlock()
	if !bound {
		return "", protocol.NewProblem(
			protocol.CodeUnavailable, "child agent runtime is not bound to a session", false, nil,
		)
	}
	agent, ok := manager.Agent(agentID)
	if !ok {
		return "", fmt.Errorf("agent %s is unavailable", agentID)
	}
	spec, err := c.specFor(agent)
	if err != nil {
		return "", err
	}
	threadID := protocol.ThreadID(subagent.ThreadIDFor(agentID))
	if _, registered := threads.ChildSpecFor(threadID); !registered {
		if err := threads.RegisterChild(threadID, spec); err != nil {
			return "", err
		}
	}
	if runtime.SessionProfilesAvailable() && agent.SessionID != "" {
		if _, err := runtime.RestoreSessionProfile(
			ctx, agent.SessionID, threadID,
		); err != nil {
			return "", fmt.Errorf("restore child session profile: %w", err)
		}
	}
	turnID, err := protocol.NewTurnID()
	if err != nil {
		return "", err
	}
	itemID, err := protocol.NewItemID()
	if err != nil {
		return "", err
	}
	operation, err := protocol.NewOperation(&protocol.StartTurnPayload{
		ThreadID: threadID, TurnID: turnID, ItemID: itemID, Prompt: prompt,
		Intent: childTurnIntent(agent.Role, spec.ReadOnly),
	})
	if err != nil {
		return "", err
	}

	lease, err := c.admit(agent.Depth)
	if err != nil {
		return "", err
	}

	c.ensurePump(ctx)
	c.mu.Lock()
	c.turns[threadID] = &childTurn{
		agentID: agentID, turnID: turnID, startOperation: operation.ID,
		startedAt: time.Now(),
		lease:     lease, leased: true,
	}
	c.mu.Unlock()

	if err := runtime.Submit(ctx, operation); err != nil {
		c.mu.Lock()
		delete(c.turns, threadID)
		c.mu.Unlock()
		c.governor.Release(lease)
		return "", err
	}
	c.armDeadline(threadID, turnID)
	return string(turnID), nil
}

func childTurnIntent(role subagent.Role, readOnly bool) protocol.TurnIntent {
	switch role {
	case subagent.RolePlan:
		return protocol.TurnIntentPlan
	case subagent.RoleImplementer, subagent.RoleGeneral:
		if !readOnly {
			return protocol.TurnIntentWorkspaceChange
		}
		return protocol.TurnIntentAnswer
	default:
		return protocol.TurnIntentAnswer
	}
}

// CancelTurn interrupts a child turn through the same cancel operation a host
// would use, so a child's cancellation is as auditable as any other.
func (c *childRuntime) CancelTurn(ctx context.Context, agentID, turnID string) error {
	c.mu.Lock()
	runtime, bound := c.runtime, c.bound
	c.mu.Unlock()
	if !bound {
		return nil
	}
	itemID, err := protocol.NewItemID()
	if err != nil {
		return err
	}
	operation, err := protocol.NewOperation(&protocol.CancelTurnPayload{
		ThreadID: protocol.ThreadID(subagent.ThreadIDFor(agentID)),
		TurnID:   protocol.TurnID(turnID),
		ItemID:   itemID,
		Reason:   protocol.CancelReasonHostInterrupted,
	})
	if err != nil {
		return err
	}
	return runtime.Submit(ctx, operation)
}

// release drops a closed child's thread engine so its history and guard are not
// retained for the rest of the process.
func (c *childRuntime) release(agentID string) {
	threadID := protocol.ThreadID(subagent.ThreadIDFor(agentID))
	c.mu.Lock()
	active := c.turns[threadID]
	c.mu.Unlock()
	// Close may target a serialized child still waiting for the parent workspace.
	// Cancel its runtime turn before forgetting the thread, otherwise it could
	// acquire the gate later and run after the operator already closed it.
	if active != nil {
		if !c.waitForStart(threadID, 2*time.Second) {
			go func() {
				if c.waitForStart(threadID, c.limits.WallTime) {
					c.release(agentID)
				}
			}()
			return
		}
		c.mu.Lock()
		active = c.turns[threadID]
		c.mu.Unlock()
		if active == nil {
			c.releaseThread(threadID)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = c.CancelTurn(ctx, agentID, string(active.turnID))
		cancel()
		if !c.waitForTerminal(threadID, 2*time.Second) {
			go func() {
				if c.waitForTerminal(threadID, c.limits.WallTime) {
					c.releaseThread(threadID)
				}
			}()
			return
		}
	}
	c.releaseThread(threadID)
}

func (c *childRuntime) waitForStart(
	threadID protocol.ThreadID,
	timeout time.Duration,
) bool {
	deadline := time.Now().Add(timeout)
	for {
		c.mu.Lock()
		turn := c.turns[threadID]
		ready := turn == nil || turn.started
		c.mu.Unlock()
		if ready {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (c *childRuntime) waitForTerminal(
	threadID protocol.ThreadID,
	timeout time.Duration,
) bool {
	deadline := time.Now().Add(timeout)
	for {
		c.mu.Lock()
		active := c.turns[threadID] != nil
		c.mu.Unlock()
		if !active {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (c *childRuntime) releaseThread(threadID protocol.ThreadID) {
	c.mu.Lock()
	threads := c.threads
	if turn := c.turns[threadID]; turn != nil {
		if turn.deadline != nil {
			turn.deadline()
		}
		if turn.leased {
			c.governor.Release(turn.lease)
			turn.leased = false
		}
	}
	delete(c.turns, threadID)
	c.mu.Unlock()
	if threads == nil {
		return
	}
	// The spec has to be read before the thread is released, because releasing it
	// is what forgets which isolated root this child was using.
	if spec, ok := threads.ChildSpecFor(threadID); ok &&
		!spec.ReadOnly && !spec.Serialized && c.tools != nil {
		c.tools.release(spec.Workspace)
	}
	threads.Release(threadID)
}

// specFor resolves where an agent runs and what it may do there. It fails closed:
// a child that needs to write but has nowhere isolated to write is rejected
// rather than pointed at the parent workspace.
func (c *childRuntime) specFor(agent subagent.Agent) (app.ChildSpec, error) {
	role, err := c.manager.RoleSpec(agent.Role)
	if err != nil {
		return app.ChildSpec{}, err
	}
	spec := app.ChildSpec{
		AgentID: agent.ID, AgentPath: agent.Path, ParentPath: agent.ParentPath,
		Role: string(agent.Role), Stance: string(agent.Stance),
		Workspace: c.root, HostWorkspace: agent.Workspace, SessionID: agent.SessionID,
		ReadOnly:     true,
		AllowedTools: append([]string(nil), role.AllowedTools...),
		CanDelegate:  role.CanDelegate,
		MaxSteps:     agent.Budget.MaxSteps, MaxTokens: agent.Budget.MaxTokens,
		MaxCostUSD: agent.Budget.MaxCostUSD,
	}
	if spec.MaxSteps == 0 {
		spec.MaxSteps = c.limits.MaxSteps
	}
	if spec.MaxTokens == 0 {
		spec.MaxTokens = c.limits.MaxTokens
	}
	if spec.MaxCostUSD == 0 {
		spec.MaxCostUSD = c.limits.MaxCostUSD
	}
	if spec.HostWorkspace == "" {
		spec.HostWorkspace = c.root
	}
	if c.limits.Workspace == config.SubagentWorkspaceSerialized {
		if !agent.Serialized || strings.TrimSpace(agent.Worktree) != c.root {
			return app.ChildSpec{}, protocol.NewProblem(
				protocol.CodeUnavailable,
				"serialized child does not own the configured host workspace lease",
				false, nil,
			)
		}
		spec.Serialized = true
		spec.ReadOnly = agent.Stance == subagent.StanceReadOnly
		return spec, nil
	}
	if c.limits.Workspace == config.SubagentWorkspaceReadOnly ||
		agent.Stance == subagent.StanceReadOnly {
		// Read-only children share the host workspace and get no journal: they
		// change nothing, so there is nothing to roll back.
		if strings.TrimSpace(agent.ExecutionRoot) != "" {
			spec.Workspace = agent.ExecutionRoot
		}
		return spec, nil
	}
	if !agent.Isolated || strings.TrimSpace(agent.Worktree) == "" {
		return app.ChildSpec{}, protocol.NewProblem(
			protocol.CodeUnavailable,
			fmt.Sprintf(
				"child agents with stance %q need an isolated worktree, which this workspace "+
					"could not provide; spawn an explore or review agent, or set "+
					"execution.subagent.workspace = %q to run this child read-only",
				agent.Stance, config.SubagentWorkspaceReadOnly,
			),
			false, nil,
		)
	}
	spec.Workspace, spec.ReadOnly = agent.Worktree, false
	return spec, nil
}

// admit refuses a child turn that the shared budget can no longer pay for, and
// takes a lease held until the turn settles.
//
// The lease is what makes in-flight child turns countable: subagent.Manager caps
// how many children may be alive at once, which is usually the limit an operator
// hits first, but it counts residents rather than running turns. Follow-up turns
// on resident children go through here without touching that cap, so this is the
// only place that knows how many children are actually running.
func (c *childRuntime) admit(depth int) (rlm.Lease, error) {
	limits := c.governor.Limits()
	spent := c.governor.Snapshot()
	if limits.MaxTokens > 0 && spent.SpentTokens >= limits.MaxTokens {
		return rlm.Lease{}, protocol.NewProblem(
			protocol.CodeResourceExhausted,
			fmt.Sprintf(
				"child agents have spent their shared token budget (%d of %d); "+
					"raise execution.subagent.max_tokens to run more children",
				spent.SpentTokens, limits.MaxTokens,
			),
			false, nil,
		)
	}
	if limits.MaxCostUSD > 0 && spent.SpentCostUSD >= limits.MaxCostUSD {
		return rlm.Lease{}, protocol.NewProblem(
			protocol.CodeResourceExhausted,
			fmt.Sprintf(
				"child agents have spent their shared cost budget ($%.6f of $%.6f); "+
					"raise execution.subagent.max_cost_usd to run more children",
				spent.SpentCostUSD, limits.MaxCostUSD,
			),
			false, nil,
		)
	}
	lease, err := c.governor.Admit(depth, 0, 0)
	if err == nil {
		return lease, nil
	}
	return rlm.Lease{}, protocol.NewProblem(
		protocol.CodeResourceExhausted,
		fmt.Sprintf("child agent at depth %d was not admitted: %s", depth, err),
		// Concurrency frees up on its own; depth and spend do not.
		errors.Is(err, rlm.ErrConcurrency), nil,
	)
}

// charge records what the child actually spent. It runs at settlement because
// the receipt is the first place real usage exists, which means the pot can be
// overdrawn by one turn — the next child is the one that gets refused.
func (c *childRuntime) charge(turn *childTurn, result *subagent.Result) {
	if turn.leased {
		c.governor.Release(turn.lease)
		turn.leased = false
	}
	tokens, cost := result.Usage.Tokens(), result.Usage.CostUSD()
	if tokens == 0 && cost == 0 {
		return
	}
	if err := c.governor.Record(tokens, cost); err != nil {
		result.Unresolved = append(result.Unresolved, fmt.Sprintf(
			"this child overdrew the shared child budget (%s); further children are refused", err,
		))
	}
}

func (c *childRuntime) armDeadline(threadID protocol.ThreadID, turnID protocol.TurnID) {
	wallTime := c.limits.WallTime
	if wallTime <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	turn := c.turns[threadID]
	if turn == nil || turn.turnID != turnID {
		c.mu.Unlock()
		cancel()
		return
	}
	turn.deadline = cancel
	c.mu.Unlock()

	go func() {
		timer := time.NewTimer(wallTime)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		c.mu.Lock()
		current := c.turns[threadID]
		if current == nil || current.turnID != turnID {
			c.mu.Unlock()
			return
		}
		current.timedOut = true
		agentID := current.agentID
		c.mu.Unlock()
		// Still running past its wall clock: cancel through the normal path. The
		// pump turns the resulting terminal event into an errored child with the
		// timeout recorded as unresolved.
		_ = c.CancelTurn(context.Background(), agentID, string(turnID))
	}()
}

func (c *childRuntime) ensurePump(ctx context.Context) {
	c.pumpOnce.Do(func() {
		c.mu.Lock()
		runtime := c.runtime
		c.mu.Unlock()
		if runtime == nil {
			close(c.done)
			return
		}
		cursor := runtime.Snapshot(ctx).LastSequence
		pumpCtx, cancel := context.WithCancel(context.Background())
		c.mu.Lock()
		c.stop = cancel
		c.mu.Unlock()
		go c.pump(pumpCtx, runtime, cursor)
	})
}

// pump is the single subscription that translates child turn events into
// subagent status transitions. Without it a child would run to completion and
// stay "running" forever, which is what the stubbed runtime did.
func (c *childRuntime) pump(ctx context.Context, runtime *app.Runtime, cursor protocol.Cursor) {
	defer close(c.done)
	for {
		if ctx.Err() != nil {
			return
		}
		events, err := runtime.Events(ctx, cursor)
		if err != nil {
			if errors.Is(err, app.ErrClosed) {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(20 * time.Millisecond):
				continue
			}
		}
		for event := range events {
			cursor = event.Sequence
			c.observe(event)
		}
		// The channel closes when this subscriber was dropped for being slow, or
		// when the runtime shut down. Resubscribing from the last sequence seen
		// keeps a burst of child events from stranding an agent in "running".
	}
}

func (c *childRuntime) observe(event protocol.Event) {
	if event.ThreadID == "" {
		return
	}
	c.mu.Lock()
	turn := c.turns[event.ThreadID]
	if turn == nil || (event.TurnID != "" && event.TurnID != turn.turnID) {
		c.mu.Unlock()
		return
	}
	settle := false
	status := subagent.StatusCompleted
	var waitRequest, resumeRequest string
	switch data := event.Data.(type) {
	case *protocol.TurnStartedData:
		turn.started = true
	case *protocol.ExecutionReceiptData:
		copied := *data
		turn.receipt = &copied
	case *protocol.TurnVerificationData:
		copied := *data
		turn.verify = &copied
	case *protocol.ApprovalRequiredData:
		waitRequest = data.RequestID
	case *protocol.ApprovalResolvedData:
		resumeRequest = data.RequestID
	case *protocol.TurnCompletedData:
		if text := strings.TrimSpace(data.Text); text != "" {
			turn.text = text
		}
		settle, status = true, subagent.StatusCompleted
	case *protocol.TurnFailedData:
		turn.notes = append(turn.notes, fmt.Sprintf("%s: %s", data.Code, data.Message))
		settle, status = true, subagent.StatusErrored
	case *protocol.TurnCanceledData:
		settle, status = true, subagent.StatusInterrupted
		if turn.timedOut {
			status = subagent.StatusErrored
		}
	case *protocol.OperationRejectedData:
		turn.notes = append(turn.notes, fmt.Sprintf(
			"operation rejected: %s",
			data.Message,
		))
		if event.OperationID == turn.startOperation {
			settle, status = true, subagent.StatusErrored
		}
	}
	if !settle {
		manager := c.manager
		agentID := turn.agentID
		c.mu.Unlock()
		var transitionErr error
		if manager != nil && waitRequest != "" {
			transitionErr = manager.AwaitApproval(agentID, waitRequest)
		}
		if manager != nil && resumeRequest != "" {
			transitionErr = manager.ResumeApproval(agentID, resumeRequest)
		}
		if transitionErr != nil {
			c.mu.Lock()
			if current := c.turns[event.ThreadID]; current != nil {
				current.notes = append(current.notes, transitionErr.Error())
			}
			c.mu.Unlock()
		}
		return
	}
	if turn.deadline != nil {
		turn.deadline()
		turn.deadline = nil
	}
	result := turn.result(event.ThreadID, status)
	c.charge(turn, &result)
	manager := c.manager
	delete(c.turns, event.ThreadID)
	c.mu.Unlock()

	if manager != nil {
		_ = manager.Settle(result)
	}
}

func (t *childTurn) result(threadID protocol.ThreadID, status subagent.Status) subagent.Result {
	result := subagent.Result{
		AgentID: t.agentID, ThreadID: string(threadID), TurnID: string(t.turnID),
		Status: status, Summary: t.text,
	}
	if t.timedOut {
		result.Unresolved = append(result.Unresolved, fmt.Sprintf(
			"child agent exceeded its wall-clock budget after %s and was canceled",
			time.Since(t.startedAt).Round(time.Second),
		))
	}
	result.Unresolved = append(result.Unresolved, t.notes...)
	if receipt := t.receipt; receipt != nil {
		result.Evidence = receipt.Evidence
		result.Diff = receipt.Changes
		result.Verification = receipt.Verification
		result.Unresolved = append(result.Unresolved, receipt.UnresolvedIssues...)
		result.Usage = subagent.ResultUsage{
			InputTokens: receipt.InputTokens, OutputTokens: receipt.OutputTokens,
			ReasoningTokens: receipt.ReasoningTokens, CachedTokens: receipt.CachedTokens,
			CostMicrounits: receipt.CostMicrounits, CostKnown: receipt.CostKnown,
		}
	} else {
		// No receipt means the turn never reached its own accounting: say so
		// instead of reporting an all-zero, all-passed result.
		result.Verification = protocol.ReceiptVerification{
			Diagnostics: protocol.ReceiptNotEvaluated,
			Tests:       protocol.ReceiptNotEvaluated,
			Verify:      protocol.ReceiptNotEvaluated,
		}
	}
	if t.verify != nil && t.verify.Status != "" {
		result.Verification.Verify = t.verify.Status
	}
	return result
}
