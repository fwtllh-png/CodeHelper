package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/tracecontext"
	workbudget "github.com/fwtllh-png/CodeHelper/internal/orchestration/budget"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/kernel"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/model"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// Runtime compiles a Workflow into WorkGraph and dispatches only nodes the
// Kernel marks Ready. Dependency and terminal state are never owned here.
type Runtime struct {
	mu         sync.Mutex
	running    bool
	controller GraphController
	ledger     *workbudget.Ledger
}

func NewRuntime() *Runtime {
	return NewRuntimeWithControllerAndBudget(
		newMemoryController(),
		workbudget.NewLedger(),
	)
}

func NewRuntimeWithController(controller GraphController) *Runtime {
	return NewRuntimeWithControllerAndBudget(controller, workbudget.NewLedger())
}

func NewRuntimeWithControllerAndBudget(
	controller GraphController,
	ledger *workbudget.Ledger,
) *Runtime {
	if ledger == nil {
		ledger = workbudget.NewLedger()
	}
	return &Runtime{controller: controller, ledger: ledger}
}

type RunOptions struct {
	ID           string
	Spec         Spec
	Driver       Driver
	Now          func() time.Time
	SessionID    string
	Workspace    string
	RootThreadID protocol.ThreadID
	LaneID       string
	Sleep        func(ctx context.Context, delay time.Duration) error
}

func (r *Runtime) Run(ctx context.Context, options RunOptions) (Run, error) {
	if options.Driver == nil {
		return Run{}, errors.New("workflow driver is required")
	}
	if traced, err := tracecontext.NewRoot(ctx); err == nil {
		ctx = traced
	}
	now := time.Now().UTC()
	if options.Now != nil {
		now = options.Now().UTC()
	}
	id := options.ID
	if id == "" {
		id = fmt.Sprintf("wf_%d", now.UnixNano())
	}
	runID := protocol.RunID(id)
	compiled, err := Compile(options.Spec, CompileOptions{
		RunID: runID, SessionID: options.SessionID,
		Workspace: options.Workspace, RootThreadID: options.RootThreadID,
	})
	if err != nil {
		return Run{}, err
	}
	if r == nil || r.controller == nil {
		return Run{}, errors.New("workflow WorkGraph controller is required")
	}
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return Run{}, errors.New("workflow runtime is single-threaded")
	}
	r.running = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
		_ = options.Driver.CancelAll()
	}()

	started := Run{
		ID: id, SpecID: options.Spec.ID, Goal: options.Spec.Goal,
		Status: RunRunning, CreatedAt: now, UpdatedAt: now,
	}
	execution := &graphExecution{
		runID: runID, spec: options.Spec, ordered: compiled.Ordered,
		driver: options.Driver, controller: r.controller,
		laneID: options.LaneID,
		sleep:  options.Sleep,
		clock:  options.Now, logicalNow: now,
		budget:  options.Spec.Budget.WithDefaults(),
		ledger:  r.ledger,
		resumed: make(map[protocol.NodeID]bool),
	}
	if err := execution.prepareBudget(options.Workspace, options.SessionID); err != nil {
		started.Status, started.Error = RunFailed, err.Error()
		return started, err
	}
	if execution.sleep == nil {
		execution.sleep = sleepFor
	}
	graph, err := execution.prepare(ctx, compiled.Submit)
	if err != nil {
		if errors.Is(err, ErrBudgetExhausted) {
			started.Nodes = execution.results(graph)
			started.Result = summarize(started.Nodes)
			started.Status, started.Error = RunBlocked, err.Error()
			started.UpdatedAt = execution.now()
			return started, err
		}
		started.Status, started.Error = RunFailed, err.Error()
		return started, err
	}
	graph, runErr := execution.dispatch(ctx, graph)
	nodes := execution.results(graph)
	started.Nodes = nodes
	started.Result = summarize(nodes)
	started.UpdatedAt = execution.now()
	switch {
	case runErr != nil && (errors.Is(runErr, context.Canceled) ||
		errors.Is(runErr, context.DeadlineExceeded)):
		started.Status, started.Error = RunCanceled, runErr.Error()
	case errors.Is(runErr, ErrBudgetExhausted) ||
		graph.Run.State == protocol.RunStateBlocked:
		started.Status, started.Error = RunBlocked, graph.Run.Reason
		if started.Error == "" && runErr != nil {
			started.Error = runErr.Error()
		}
	case runErr != nil:
		started.Status, started.Error = RunFailed, runErr.Error()
	case graph.Run.State == protocol.RunStateCompleted:
		started.Status = RunCompleted
	case graph.Run.State == protocol.RunStateCanceled:
		started.Status, started.Error = RunCanceled, graph.Run.Reason
	default:
		started.Status, started.Error = RunFailed, graph.Run.Reason
		runErr = execution.failure(nodes)
	}
	return started, runErr
}

type graphExecution struct {
	runID       protocol.RunID
	spec        Spec
	ordered     []Node
	driver      Driver
	controller  GraphController
	laneID      string
	sleep       func(context.Context, time.Duration) error
	clock       func() time.Time
	logicalNow  time.Time
	budget      Budget
	ledger      *workbudget.Ledger
	budgetScope string
	steps       int
	resumed     map[protocol.NodeID]bool
}

type claimedNode struct {
	node          Node
	attemptID     protocol.AttemptID
	effectID      protocol.EffectID
	epoch         uint64
	attempt       int
	reservationID string
}

type nodeCompletion struct {
	claim  claimedNode
	result NodeResult
	fatal  error
}

func (e *graphExecution) now() time.Time {
	value := time.Now().UTC()
	if e.clock != nil {
		value = e.clock().UTC()
	}
	if value.Before(e.logicalNow) {
		return e.logicalNow
	}
	e.logicalNow = value
	return value
}

func (e *graphExecution) prepareBudget(workspace, sessionID string) error {
	if e.ledger == nil {
		return errors.New("workflow budget ledger is required")
	}
	if workspace == "" {
		workspace = "_"
	}
	if sessionID == "" {
		sessionID = "_"
	}
	workspaceScope := "workspace:" + workspace
	sessionScope := workspaceScope + "/session:" + sessionID
	e.budgetScope = sessionScope + "/run:" + string(e.runID)
	if err := e.ledger.EnsureScope(
		workspaceScope,
		"",
		workbudget.Limits{},
	); err != nil {
		return err
	}
	if err := e.ledger.EnsureScope(
		sessionScope,
		workspaceScope,
		workbudget.Limits{},
	); err != nil {
		return err
	}
	return e.ledger.EnsureScope(
		e.budgetScope,
		sessionScope,
		workbudget.Limits{
			MaxTokens:     e.budget.MaxTokens,
			MaxCostMicros: budgetCostMicrounits(e.budget.MaxCostUSD),
			MaxSlots:      e.budget.MaxParallel,
		},
	)
}

func (e *graphExecution) reserveAttempt(id string) error {
	snapshot, err := e.ledger.Snapshot(e.budgetScope)
	if err != nil {
		return err
	}
	availableSlots := e.budget.MaxParallel - snapshot.Reserved.Slots
	if availableSlots <= 0 {
		return workbudget.ErrExhausted
	}
	amount := workbudget.Usage{Slots: 1}
	if e.budget.MaxTokens > 0 {
		used := snapshot.Reserved.Tokens + snapshot.Spent.Tokens
		if used >= e.budget.MaxTokens {
			return workflowBudgetExhausted(
				protocol.BudgetResourceTokens,
				e.budgetScope,
				used,
				e.budget.MaxTokens,
				false,
				ErrBudgetExhausted,
			)
		}
		amount.Tokens = divideCeil(
			e.budget.MaxTokens-used,
			uint64(availableSlots),
		)
	}
	maxCost := budgetCostMicrounits(e.budget.MaxCostUSD)
	if maxCost > 0 {
		used := snapshot.Reserved.CostMicros + snapshot.Spent.CostMicros
		if used >= maxCost {
			return workflowBudgetExhausted(
				protocol.BudgetResourceCostMicrounits,
				e.budgetScope,
				used,
				maxCost,
				false,
				ErrBudgetExhausted,
			)
		}
		amount.CostMicros = divideCeil(maxCost-used, uint64(availableSlots))
	}
	err = e.ledger.Reserve(workbudget.Reservation{
		ID: id, ScopeID: e.budgetScope, Amount: amount,
	})
	return resumableWorkflowBudgetError(err)
}

func divideCeil(value, divisor uint64) uint64 {
	if divisor == 0 || value == 0 {
		return 0
	}
	return 1 + (value-1)/divisor
}

func budgetCostMicrounits(costUSD float64) uint64 {
	if costUSD <= 0 {
		return 0
	}
	return max(uint64(1), uint64(math.Ceil(costUSD*1e6)))
}

func (e *graphExecution) restoreBudget(graph model.Graph) error {
	snapshot, err := e.ledger.Snapshot(e.budgetScope)
	if err != nil {
		return err
	}
	if snapshot.Spent.Tokens != 0 || snapshot.Spent.CostMicros != 0 {
		return nil
	}
	for _, node := range graph.Nodes {
		if len(node.Result) == 0 {
			continue
		}
		var result NodeResult
		if decodeErr := json.Unmarshal(node.Result, &result); decodeErr != nil {
			return fmt.Errorf("restore workflow budget for %s: %w", node.ID, decodeErr)
		}
		if result.Usage == (WorkUsage{}) {
			continue
		}
		reservationID := "workflow:budget:restore:" +
			string(e.runID) + ":" + string(node.ID)
		if err := e.ledger.Reserve(workbudget.Reservation{
			ID: reservationID, ScopeID: e.budgetScope,
		}); err != nil {
			return err
		}
		if err := e.ledger.Settle(reservationID, workbudget.Usage{
			Tokens:     result.Usage.Tokens,
			CostMicros: result.Usage.CostMicros,
		}); err != nil {
			if errors.Is(err, workbudget.ErrExhausted) {
				return resumableWorkflowBudgetError(err)
			}
			return err
		}
	}
	return nil
}

func (e *graphExecution) prepare(
	ctx context.Context,
	submit kernel.SubmitData,
) (model.Graph, error) {
	graph, err := e.controller.Load(ctx, e.runID)
	newGraph := errors.Is(err, kernel.ErrNotFound)
	if err != nil && !newGraph {
		return model.Graph{}, err
	}
	if newGraph {
		result, err := e.controller.Execute(ctx, kernel.Command{
			ID:   "workflow:submit:" + string(e.runID),
			Kind: kernel.CommandSubmit, RunID: e.runID,
			At: e.now(), Submit: &submit,
		})
		if err != nil {
			return model.Graph{}, err
		}
		graph = result.Graph
	} else if graph.Run.DefinitionDigest != e.spec.Fingerprint() {
		return model.Graph{}, fmt.Errorf(
			"%w: run %s started from %s",
			ErrSpecChanged,
			e.runID,
			graph.Run.DefinitionDigest,
		)
	}
	if err := e.restoreBudget(graph); err != nil {
		return graph, err
	}
	for id, node := range graph.Nodes {
		if node.State == protocol.NodeStateSucceeded ||
			(node.State == protocol.NodeStateSkipped &&
				graph.Run.State == protocol.RunStateCompleted) {
			e.resumed[id] = true
		}
	}
	if runTerminalState(graph.Run.State) &&
		graph.Run.State != protocol.RunStateCompleted {
		for _, node := range e.ordered {
			current := graph.Nodes[protocol.NodeID(node.ID)]
			if current.State != protocol.NodeStateFailed &&
				current.State != protocol.NodeStateSkipped &&
				current.State != protocol.NodeStateCanceled &&
				current.State != protocol.NodeStateBlocked {
				continue
			}
			result, err := e.controller.Execute(ctx, kernel.Command{
				ID: fmt.Sprintf(
					"workflow:resume:%s:%s:%d",
					e.runID,
					node.ID,
					graph.Run.Revision,
				),
				Kind: kernel.CommandRetryNode, RunID: e.runID,
				NodeID:           protocol.NodeID(node.ID),
				ExpectedRevision: graph.Run.Revision, At: e.now(),
			})
			if err != nil {
				return model.Graph{}, err
			}
			graph = result.Graph
		}
	}
	for {
		attempt, found := activeAttempt(graph)
		if !found {
			break
		}
		result, err := e.controller.Execute(ctx, kernel.Command{
			ID: fmt.Sprintf(
				"workflow:interrupt:%s:%s:%d",
				e.runID,
				attempt.ID,
				graph.Run.Revision,
			),
			Kind: kernel.CommandReleaseAttempt, RunID: e.runID,
			AttemptID: attempt.ID, ExpectedRevision: graph.Run.Revision,
			At: e.now(), LeaseOwner: attempt.LeaseOwner,
			LeaseEpoch: attempt.LeaseEpoch, Reason: "interrupted",
		})
		if err != nil {
			return model.Graph{}, err
		}
		graph = result.Graph
	}
	if graph.Run.State == protocol.RunStateCanceling {
		canceled, err := e.controller.Execute(ctx, kernel.Command{
			ID: fmt.Sprintf(
				"workflow:finish-cancel:%s:%d",
				e.runID,
				graph.Run.Revision,
			),
			Kind: kernel.CommandCancel, RunID: e.runID,
			ExpectedRevision: graph.Run.Revision, At: e.now(),
			Reason: firstNonEmpty(graph.Run.Reason, "interrupted"),
		})
		if err != nil {
			return model.Graph{}, err
		}
		graph = canceled.Graph
		for _, node := range e.ordered {
			current := graph.Nodes[protocol.NodeID(node.ID)]
			if current.State != protocol.NodeStateCanceled &&
				current.State != protocol.NodeStateSkipped {
				continue
			}
			retried, err := e.controller.Execute(ctx, kernel.Command{
				ID: fmt.Sprintf(
					"workflow:restart-canceled:%s:%s:%d",
					e.runID,
					node.ID,
					graph.Run.Revision,
				),
				Kind: kernel.CommandRetryNode, RunID: e.runID,
				NodeID:           protocol.NodeID(node.ID),
				ExpectedRevision: graph.Run.Revision, At: e.now(),
			})
			if err != nil {
				return model.Graph{}, err
			}
			graph = retried.Graph
		}
	}
	return graph, nil
}

func (e *graphExecution) dispatch(
	ctx context.Context,
	graph model.Graph,
) (model.Graph, error) {
	maxParallel := e.budget.MaxParallel
	if maxParallel <= 0 {
		maxParallel = 1
	}
	completed := make(chan nodeCompletion, maxParallel)
	running := make(map[protocol.NodeID]claimedNode)
	budgetPending := false
	for {
		if err := ctx.Err(); err != nil {
			return e.cancel(ctx, graph, err)
		}
		if budgetPending && len(running) == 0 {
			return e.block(ctx, graph, ErrBudgetExhausted)
		}
		if runTerminalState(graph.Run.State) && len(running) == 0 {
			if graph.Run.State == protocol.RunStateBlocked {
				return graph, ErrBudgetExhausted
			}
			return graph, e.failure(e.results(graph))
		}
		launched := false
		for len(running) < maxParallel {
			node, found := e.nextReady(graph, running, e.now())
			if !found {
				break
			}
			if e.budget.MaxSteps > 0 && e.steps >= e.budget.MaxSteps {
				budgetPending = true
				break
			}
			e.steps++
			claim, next, err := e.claim(ctx, graph, node)
			if err != nil {
				if errors.Is(err, ErrBudgetExhausted) {
					e.refundRunning(running)
					return e.block(ctx, graph, err)
				}
				return graph, err
			}
			graph = next
			running[protocol.NodeID(node.ID)] = claim
			launched = true
			states := joinStates(graph, node)
			go func(states map[protocol.NodeID]protocol.NodeState, claim claimedNode) {
				result, fatal := e.executeNode(ctx, states, claim)
				completed <- nodeCompletion{
					claim: claim, result: result, fatal: fatal,
				}
			}(states, claim)
		}
		if len(running) == 0 {
			if runTerminalState(graph.Run.State) {
				continue
			}
			retryAt, found := earliestRetry(graph)
			if found {
				delay := retryAt.Sub(e.now())
				if delay > 0 {
					if err := e.sleep(ctx, delay); err != nil {
						return e.cancel(ctx, graph, err)
					}
				}
				if retryAt.After(e.logicalNow) {
					e.logicalNow = retryAt
				}
				continue
			}
			if !launched {
				return graph, fmt.Errorf(
					"%w: no ready WorkGraph node",
					ErrInvalidSpec,
				)
			}
		}
		select {
		case <-ctx.Done():
			e.refundRunning(running)
			return e.cancel(ctx, graph, ctx.Err())
		case completion := <-completed:
			delete(running, protocol.NodeID(completion.claim.node.ID))
			if completion.fatal != nil {
				_ = e.ledger.Refund(completion.claim.reservationID)
				e.refundRunning(running)
				return e.cancel(ctx, graph, completion.fatal)
			}
			next, err := e.complete(ctx, graph, completion)
			if err != nil {
				e.refundRunning(running)
				if errors.Is(err, ErrBudgetExhausted) {
					return e.block(ctx, next, err)
				}
				return next, err
			}
			graph = next
		}
	}
}

func (e *graphExecution) refundRunning(
	running map[protocol.NodeID]claimedNode,
) {
	for _, claim := range running {
		_ = e.ledger.Refund(claim.reservationID)
	}
}

func (e *graphExecution) claim(
	ctx context.Context,
	graph model.Graph,
	node Node,
) (claimedNode, model.Graph, error) {
	nodeID := protocol.NodeID(node.ID)
	epoch := graph.Run.Revision + 1
	attemptID := protocol.AttemptID(fmt.Sprintf(
		"attempt_%s_%s_%d",
		e.runID,
		node.ID,
		epoch,
	))
	effectID := protocol.EffectID("effect_" + string(attemptID))
	reservationID := "workflow:budget:" + string(attemptID)
	if err := e.reserveAttempt(reservationID); err != nil {
		return claimedNode{}, graph, err
	}
	now := e.now()
	expires := now.Add(24 * time.Hour)
	claimed, err := e.controller.Execute(ctx, kernel.Command{
		ID:   "workflow:claim:" + string(attemptID),
		Kind: kernel.CommandClaimNode, RunID: e.runID, NodeID: nodeID,
		AttemptID: attemptID, EffectID: effectID,
		ExpectedRevision: graph.Run.Revision, At: now,
		LeaseOwner: "workflow:" + string(e.runID),
		LeaseEpoch: epoch, LeaseExpiresAt: &expires,
		ExpectedAuthorityDigest: workflowAuthorityDigest(e.spec, node),
	})
	if err != nil {
		_ = e.ledger.Refund(reservationID)
		return claimedNode{}, graph, err
	}
	bound, err := e.controller.Execute(ctx, kernel.Command{
		ID:    "workflow:bind:" + string(attemptID),
		Kind:  kernel.CommandBindExecution,
		RunID: e.runID, AttemptID: attemptID,
		ExpectedRevision: claimed.Graph.Run.Revision, At: now,
		LeaseOwner: "workflow:" + string(e.runID), LeaseEpoch: epoch,
		Execution: &model.ExecutionRef{
			Kind: "workflow_node", EffectID: effectID,
			ProcessID: "workflow:" + string(e.runID) + ":" + node.ID,
			LaneID:    e.laneID,
		},
	})
	if err != nil {
		_ = e.ledger.Refund(reservationID)
		return claimedNode{}, graph, err
	}
	attempt := bound.Graph.Attempts[attemptID]
	return claimedNode{
		node: node, attemptID: attemptID, effectID: effectID,
		epoch: epoch, attempt: attempt.Number,
		reservationID: reservationID,
	}, bound.Graph, nil
}

func (e *graphExecution) complete(
	ctx context.Context,
	graph model.Graph,
	completion nodeCompletion,
) (model.Graph, error) {
	result := completion.result
	result.ID = completion.claim.node.ID
	result.Attempt = completion.claim.attempt
	budgetErr := e.ledger.Settle(
		completion.claim.reservationID,
		workbudget.Usage{
			Tokens:     result.Usage.Tokens,
			CostMicros: result.Usage.CostMicros,
		},
	)
	if budgetErr != nil {
		if !errors.Is(budgetErr, workbudget.ErrExhausted) {
			return graph, budgetErr
		}
		budgetErr = resumableWorkflowBudgetError(budgetErr)
		result.Status = NodeStatusBlocked
		result.Reason = budgetErr.Error()
		result.retryable = false
	}
	if result.Status == "" {
		result.Status, result.Reason =
			NodeStatusFailed, firstNonEmpty(result.Reason, "node produced no status")
		result.retryable = true
	}
	if result.Status == NodeStatusFailed && result.retryable &&
		completion.claim.attempt < completion.claim.node.attempts() {
		retryAt := e.now().Add(completion.claim.node.backoff())
		released, err := e.controller.Execute(ctx, kernel.Command{
			ID: fmt.Sprintf(
				"workflow:retry:%s:%s:%d",
				e.runID,
				completion.claim.node.ID,
				completion.claim.epoch,
			),
			Kind:  kernel.CommandReleaseAttempt,
			RunID: e.runID, AttemptID: completion.claim.attemptID,
			ExpectedRevision: graph.Run.Revision, At: e.now(),
			LeaseOwner: "workflow:" + string(e.runID),
			LeaseEpoch: completion.claim.epoch,
			Reason:     "workflow_retry", RetryAt: &retryAt,
			ConsumeAttempt: true,
		})
		return released.Graph, err
	}
	state := protocol.NodeStateSucceeded
	switch result.Status {
	case NodeStatusFailed:
		state = protocol.NodeStateFailed
	case NodeStatusBlocked:
		state = protocol.NodeStateBlocked
	case NodeStatusSkipped:
		state = protocol.NodeStateSkipped
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return graph, err
	}
	settled, err := e.controller.Execute(ctx, kernel.Command{
		ID: fmt.Sprintf(
			"workflow:settle:%s:%s:%d",
			e.runID,
			completion.claim.node.ID,
			completion.claim.epoch,
		),
		Kind:  kernel.CommandSettleExecution,
		RunID: e.runID, AttemptID: completion.claim.attemptID,
		ExpectedRevision: graph.Run.Revision, At: e.now(),
		LeaseOwner: "workflow:" + string(e.runID),
		LeaseEpoch: completion.claim.epoch,
		Settlement: &kernel.SettlementData{
			State: state,
			ResultRef: fmt.Sprintf(
				"workgraph://%s/nodes/%s",
				e.runID,
				completion.claim.node.ID,
			),
			Result: encoded, Reason: result.Reason,
			PermissionDigests: append(
				[]string(nil),
				result.PermissionDigests...,
			),
		},
	})
	if err != nil {
		return graph, err
	}
	return settled.Graph, budgetErr
}

func (e *graphExecution) executeNode(
	ctx context.Context,
	states map[protocol.NodeID]protocol.NodeState,
	claim claimedNode,
) (NodeResult, error) {
	node := claim.node
	if err := e.permitted(node); err != nil {
		return failedNode(err.Error(), false), nil
	}
	attemptCtx := ctx
	if timeout := node.timeout(); timeout > 0 {
		var cancel context.CancelFunc
		attemptCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	switch node.Kind {
	case NodePhase:
		if err := e.driver.Progress(ProgressEvent{
			Kind: ProgressPhase, Message: node.Prompt,
		}); err != nil {
			return NodeResult{}, err
		}
		return NodeResult{Status: NodeStatusCompleted}, nil
	case NodeParallel:
		for _, child := range node.Children {
			if states[protocol.NodeID(child)] != protocol.NodeStateSucceeded {
				return failedNode(
					fmt.Sprintf(
						"child %s is %s",
						child,
						states[protocol.NodeID(child)],
					),
					true,
				), nil
			}
		}
		return NodeResult{Status: NodeStatusCompleted}, nil
	case NodeTask:
		taskCtx := attemptCtx
		if traced, traceErr := tracecontext.Child(attemptCtx); traceErr == nil {
			taskCtx = traced
		}
		carrier := make(map[string]string, 2)
		tracecontext.InjectMap(taskCtx, carrier)
		taskResult, err := e.driver.SpawnTask(taskCtx, TaskRequest{
			RunID: string(e.runID), NodeID: node.ID,
			Attempt: claim.attempt, Role: node.Role,
			TraceParent: carrier[tracecontext.HeaderTraceParent],
			TraceState:  carrier[tracecontext.HeaderTraceState],
			Prompt:      firstNonEmpty(node.Prompt, e.spec.Goal),
			Profile:     node.Profile, Schema: node.Schema,
		})
		if attemptCtx.Err() != nil {
			fault := workflowNodeFault(
				attemptCtx.Err(),
				node.ID,
				node.timeout(),
			)
			result := failedNode(
				fmt.Sprintf("node %s: %v", node.ID, fault),
				workflowRetryable(
					fault,
					claim.attempt,
					node.attempts(),
					node.Retry != nil && node.Retry.Idempotent,
				),
			)
			result.Usage, result.PermissionDigests =
				taskResult.Usage,
				append([]string(nil), taskResult.PermissionDigests...)
			return result, nil
		}
		if err != nil {
			fault := workflowNodeFault(err, node.ID, 0)
			result := failedNode(
				fault.Error(),
				workflowRetryable(
					fault,
					claim.attempt,
					node.attempts(),
					node.Retry != nil && node.Retry.Idempotent,
				),
			)
			result.Usage, result.PermissionDigests =
				taskResult.Usage,
				append([]string(nil), taskResult.PermissionDigests...)
			return result, nil
		}
		if !taskResult.Success {
			result := failedNode(
				firstNonEmpty(taskResult.Error, "task failed"),
				node.Retry != nil && node.Retry.Idempotent,
			)
			result.Usage, result.PermissionDigests =
				taskResult.Usage,
				append([]string(nil), taskResult.PermissionDigests...)
			return result, nil
		}
		return NodeResult{
			Status: NodeStatusCompleted, Content: taskResult.Content,
			Usage: taskResult.Usage,
			PermissionDigests: append(
				[]string(nil),
				taskResult.PermissionDigests...,
			),
		}, nil
	default:
		return NodeResult{}, fmt.Errorf(
			"%w: unsupported node kind %q",
			ErrInvalidSpec,
			node.Kind,
		)
	}
}

func failedNode(reason string, retryable bool) NodeResult {
	return NodeResult{
		Status: NodeStatusFailed, Reason: reason, retryable: retryable,
	}
}

func workflowNodeFault(
	err error,
	nodeID string,
	timeout time.Duration,
) error {
	problem := protocol.ProblemOf(err)
	retryable := problem != nil && problem.Retryable &&
		problem.Fault != nil &&
		problem.Fault.SideEffects != protocol.SideEffectUnknown
	code := protocol.CodeOf(err)
	if errors.Is(err, context.DeadlineExceeded) {
		code, retryable = protocol.CodeDeadlineExceeded, true
	}
	metadata := protocol.FaultMetadata{
		Origin:      protocol.FaultOriginRuntime,
		Stage:       protocol.FaultStageWorkflowNode,
		OperationID: nodeID,
		RetryOwner:  protocol.FaultRetryOwnerWorkflow,
		ResumeHint:  protocol.FaultResumeRetryStep,
		Disposition: protocol.FaultRetryStep,
		SideEffects: protocol.SideEffectUnchanged,
	}
	if code == protocol.CodeDeadlineExceeded {
		metadata.Deadline = &protocol.DeadlineMetadata{
			Scope:     protocol.DeadlineWorkflowNode,
			TimeoutMS: uint64(timeout / time.Millisecond),
		}
	}
	return protocol.NewFault(
		code,
		err.Error(),
		retryable,
		metadata,
		err,
	)
}

func workflowRetryable(
	err error,
	attempt int,
	maxAttempts int,
	idempotent bool,
) bool {
	decision := protocol.DecideRecovery(err, protocol.RecoveryContext{
		Owner:       protocol.FaultRetryOwnerWorkflow,
		Idempotent:  idempotent,
		Attempt:     attempt,
		MaxAttempts: maxAttempts,
	})
	return decision.Action == protocol.RecoveryRetry ||
		decision.Action == protocol.RecoveryWait
}

func (e *graphExecution) permitted(node Node) error {
	if node.Kind != NodeTask {
		return nil
	}
	for _, capability := range []string{"filesystem", "shell", "network"} {
		if node.Role != capability {
			continue
		}
		if err := e.spec.AssertAllowed(node, capability); err != nil {
			return err
		}
	}
	return nil
}

func (e *graphExecution) nextReady(
	graph model.Graph,
	running map[protocol.NodeID]claimedNode,
	now time.Time,
) (Node, bool) {
	for _, node := range e.ordered {
		id := protocol.NodeID(node.ID)
		current := graph.Nodes[id]
		if current.State != protocol.NodeStateReady {
			continue
		}
		if current.RetryAt != nil && now.Before(*current.RetryAt) {
			continue
		}
		if _, exists := running[id]; exists {
			continue
		}
		return node, true
	}
	return Node{}, false
}

func joinStates(
	graph model.Graph,
	node Node,
) map[protocol.NodeID]protocol.NodeState {
	states := make(map[protocol.NodeID]protocol.NodeState, len(node.Children))
	for _, child := range node.Children {
		id := protocol.NodeID(child)
		states[id] = graph.Nodes[id].State
	}
	return states
}

func (e *graphExecution) results(graph model.Graph) []NodeResult {
	results := make([]NodeResult, 0, len(e.ordered))
	for _, node := range e.ordered {
		current, exists := graph.Nodes[protocol.NodeID(node.ID)]
		if !exists || !terminalNodeState(current.State) {
			continue
		}
		results = append(results, e.nodeResult(graph, node))
	}
	return results
}

func (e *graphExecution) nodeResult(
	graph model.Graph,
	node Node,
) NodeResult {
	current := graph.Nodes[protocol.NodeID(node.ID)]
	result := NodeResult{
		ID: node.ID, Status: protocolWorkflowStatus(current.State),
		Attempt: current.AttemptsConsumed, Reason: current.Reason,
		Resumed: e.resumed[protocol.NodeID(node.ID)],
	}
	if len(current.Result) != 0 {
		var stored NodeResult
		if json.Unmarshal(current.Result, &stored) == nil {
			result.Content = stored.Content
			result.Usage = stored.Usage
			result.PermissionDigests = append(
				[]string(nil),
				stored.PermissionDigests...,
			)
			result.Attempt = stored.Attempt
			result.Reason = firstNonEmpty(stored.Reason, result.Reason)
		}
	}
	for _, attempt := range graph.Attempts {
		if attempt.NodeID == current.ID && attempt.Number > result.Attempt {
			result.Attempt = attempt.Number
		}
	}
	return result
}

func (e *graphExecution) cancel(
	ctx context.Context,
	graph model.Graph,
	cause error,
) (model.Graph, error) {
	if !runTerminalState(graph.Run.State) {
		result, err := e.controller.Execute(
			context.WithoutCancel(ctx),
			kernel.Command{
				ID: fmt.Sprintf(
					"workflow:cancel:%s:%d",
					e.runID,
					graph.Run.Revision,
				),
				Kind: kernel.CommandCancel, RunID: e.runID,
				ExpectedRevision: graph.Run.Revision,
				At:               e.now(), Reason: cause.Error(),
			},
		)
		if err == nil {
			graph = result.Graph
		}
	}
	return graph, cause
}

func (e *graphExecution) block(
	ctx context.Context,
	graph model.Graph,
	cause error,
) (model.Graph, error) {
	if graph.Run.State != protocol.RunStateBlocked {
		result, err := e.controller.Execute(
			context.WithoutCancel(ctx),
			kernel.Command{
				ID: fmt.Sprintf(
					"workflow:block:%s:%d",
					e.runID,
					graph.Run.Revision,
				),
				Kind: kernel.CommandBlock, RunID: e.runID,
				ExpectedRevision: graph.Run.Revision,
				At:               e.now(), Reason: cause.Error(),
			},
		)
		if err != nil {
			return graph, errors.Join(cause, err)
		}
		graph = result.Graph
	}
	return graph, cause
}

func (e *graphExecution) failure(results []NodeResult) error {
	var failed []string
	for _, result := range results {
		if result.Status == NodeStatusFailed {
			failed = append(
				failed,
				result.ID+": "+firstNonEmpty(result.Reason, "failed"),
			)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	sort.Strings(failed)
	return errors.New(failed[0])
}

func activeAttempt(graph model.Graph) (model.Attempt, bool) {
	for _, attempt := range graph.Attempts {
		if !attempt.State.Terminal() {
			return attempt, true
		}
	}
	return model.Attempt{}, false
}

func earliestRetry(graph model.Graph) (time.Time, bool) {
	var earliest time.Time
	for _, node := range graph.Nodes {
		if node.State != protocol.NodeStateReady || node.RetryAt == nil {
			continue
		}
		if earliest.IsZero() || node.RetryAt.Before(earliest) {
			earliest = *node.RetryAt
		}
	}
	return earliest, !earliest.IsZero()
}

func protocolWorkflowStatus(state protocol.NodeState) NodeStatus {
	switch state {
	case protocol.NodeStateSucceeded:
		return NodeStatusCompleted
	case protocol.NodeStateSkipped:
		return NodeStatusSkipped
	case protocol.NodeStateBlocked:
		return NodeStatusBlocked
	case protocol.NodeStateFailed, protocol.NodeStateCanceled:
		return NodeStatusFailed
	default:
		return NodeStatusPending
	}
}

func terminalNodeState(state protocol.NodeState) bool {
	return state == protocol.NodeStateSucceeded ||
		state == protocol.NodeStateFailed ||
		state == protocol.NodeStateSkipped ||
		state == protocol.NodeStateCanceled ||
		state == protocol.NodeStateBlocked
}

func runTerminalState(state protocol.RunState) bool {
	return state == protocol.RunStateCompleted ||
		state == protocol.RunStateFailed ||
		state == protocol.RunStateCanceled ||
		state == protocol.RunStateBlocked
}

func summarize(results []NodeResult) json.RawMessage {
	content := ""
	for _, result := range results {
		if result.Status == NodeStatusCompleted && result.Content != "" {
			content = result.Content
		}
	}
	if content != "" {
		encoded, _ := json.Marshal(content)
		return encoded
	}
	return json.RawMessage(`{"ok":true}`)
}

func sleepFor(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
