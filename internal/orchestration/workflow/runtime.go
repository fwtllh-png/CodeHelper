package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Runtime executes validated Workflow specs. One run at a time per Runtime: the
// driver seam is a single host connection, and two runs sharing it would
// interleave their side effects.
type Runtime struct {
	mu      sync.Mutex
	running bool
}

func NewRuntime() *Runtime {
	return &Runtime{}
}

type RunOptions struct {
	ID     string
	Spec   Spec
	Driver Driver
	Now    func() time.Time
	// Checkpoint is optional. Without it a run has no memory: an interrupted run
	// starts over, which is why the durable hosts pass one.
	Checkpoint Checkpoint
	// Sleep waits out a node's retry backoff. Tests replace it to keep hermetic.
	Sleep func(ctx context.Context, delay time.Duration) error
}

// Run executes the spec's nodes in dependency order, running independent nodes
// concurrently, and returns the run with one result per node.
func (r *Runtime) Run(ctx context.Context, options RunOptions) (Run, error) {
	if err := options.Spec.Validate(); err != nil {
		return Run{}, err
	}
	if options.Driver == nil {
		return Run{}, errors.New("workflow driver is required")
	}
	ordered, err := options.Spec.order()
	if err != nil {
		return Run{}, err
	}
	now := time.Now().UTC()
	if options.Now != nil {
		now = options.Now().UTC()
	}
	id := options.ID
	if id == "" {
		id = fmt.Sprintf("wf_%d", now.UnixNano())
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

	run := Run{
		ID: id, SpecID: options.Spec.ID, Goal: options.Spec.Goal,
		Status: RunRunning, CreatedAt: now, UpdatedAt: now,
	}
	execution := &execution{
		run:      id,
		spec:     options.Spec,
		driver:   options.Driver,
		record:   options.Checkpoint,
		sleep:    options.Sleep,
		clock:    options.Now,
		budget:   options.Spec.Budget.WithDefaults(),
		statuses: make(map[string]NodeStatus, len(ordered)),
		results:  make(map[string]NodeResult, len(ordered)),
	}
	if execution.sleep == nil {
		execution.sleep = sleepFor
	}
	if err := execution.resume(ctx); err != nil {
		return run, err
	}

	runErr := execution.walk(ctx, ordered)
	run.Nodes = execution.ordered(ordered)
	run.UpdatedAt = execution.now()
	run.Result = summarize(run.Nodes)
	switch {
	case runErr != nil && errors.Is(runErr, context.Canceled),
		runErr != nil && errors.Is(runErr, context.DeadlineExceeded):
		run.Status, run.Error = RunCanceled, runErr.Error()
	case runErr != nil:
		run.Status, run.Error = RunFailed, runErr.Error()
	default:
		run.Status = RunCompleted
	}
	return run, runErr
}

type execution struct {
	run    string
	spec   Spec
	driver Driver
	record Checkpoint
	sleep  func(context.Context, time.Duration) error
	clock  func() time.Time
	budget Budget

	mu       sync.Mutex
	statuses map[string]NodeStatus
	results  map[string]NodeResult
	steps    int
}

func (e *execution) now() time.Time {
	if e.clock != nil {
		return e.clock().UTC()
	}
	return time.Now().UTC()
}

// resume adopts node outcomes an earlier process already recorded. Only
// completed nodes are adopted: a node left `running` by a crash may have done
// half its work, and the honest move is to run it again rather than assume.
func (e *execution) resume(ctx context.Context) error {
	if e.record == nil {
		return nil
	}
	known, err := e.record.LoadNodes(ctx, e.run)
	if err != nil {
		return fmt.Errorf("load workflow checkpoint: %w", err)
	}
	for id, node := range known {
		if node.Status != NodeStatusCompleted && node.Status != NodeStatusSkipped {
			continue
		}
		e.statuses[id] = node.Status
		e.results[id] = NodeResult{
			ID: id, Status: node.Status, Attempt: node.Attempt,
			Reason: node.Reason, Content: node.Content, Resumed: true,
		}
	}
	return nil
}

// walk runs the graph in waves. Every node whose dependencies are terminal goes
// in the same wave, which is what makes independent nodes concurrent without a
// separate "parallel" construct.
func (e *execution) walk(ctx context.Context, ordered []Node) error {
	remaining := make([]Node, 0, len(ordered))
	for _, node := range ordered {
		if _, done := e.statuses[node.ID]; !done {
			remaining = append(remaining, node)
		}
	}
	for len(remaining) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		wave := make([]Node, 0, len(remaining))
		waiting := make([]Node, 0, len(remaining))
		for _, node := range remaining {
			if e.ready(node) {
				wave = append(wave, node)
				continue
			}
			waiting = append(waiting, node)
		}
		if len(wave) == 0 {
			// The graph is acyclic and every dependency is in it, so a wave with
			// nothing ready cannot happen; treating it as a failure beats looping.
			return fmt.Errorf("%w: no runnable node among %d remaining", ErrInvalidSpec, len(remaining))
		}
		if err := e.runWave(ctx, wave); err != nil {
			return err
		}
		remaining = waiting
	}
	return e.failure()
}

func (e *execution) ready(node Node) bool {
	for _, need := range node.dependencies() {
		status, known := e.statuses[need]
		if !known || !status.Terminal() {
			return false
		}
	}
	return true
}

func (e *execution) runWave(ctx context.Context, wave []Node) error {
	limit := e.budget.MaxParallel
	if limit <= 0 || limit > len(wave) {
		limit = len(wave)
	}
	slots := make(chan struct{}, limit)
	var (
		group sync.WaitGroup
		fatal error
		once  sync.Once
	)
	for _, node := range wave {
		if err := e.charge(); err != nil {
			once.Do(func() { fatal = err })
			break
		}
		group.Add(1)
		go func(node Node) {
			defer group.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			if err := e.runNode(ctx, node); err != nil {
				// A node's own failure is graph state, not a fatal error; only a
				// broken host or a canceled run stops the whole walk.
				once.Do(func() { fatal = err })
			}
		}(node)
	}
	group.Wait()
	return fatal
}

func (e *execution) charge() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.steps++
	if e.budget.MaxSteps > 0 && e.steps > e.budget.MaxSteps {
		return ErrBudgetExhausted
	}
	return nil
}

// runNode decides whether a node runs at all, then runs it with its own retry
// and timeout policy. It returns an error only for conditions that end the run.
func (e *execution) runNode(ctx context.Context, node Node) error {
	if skip, reason := e.skipReason(node); skip {
		return e.settle(ctx, node, NodeResult{
			ID: node.ID, Status: NodeStatusSkipped, Reason: reason,
		})
	}
	// A denied capability is a decision about the spec, not a flake, so it fails
	// the node once instead of burning the node's attempts on the same refusal.
	if err := e.permitted(node); err != nil {
		return e.settle(ctx, node, NodeResult{
			ID: node.ID, Status: NodeStatusFailed, Reason: err.Error(),
		})
	}
	attempts := node.attempts()
	var last NodeResult
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.record != nil {
			started := NodeRecord{
				ID: node.ID, Status: NodeStatusRunning,
				Attempt: attempt, StartedAt: e.now(),
			}
			if err := e.record.NodeStarted(ctx, e.run, started); err != nil {
				return fmt.Errorf("checkpoint node %q: %w", node.ID, err)
			}
		}
		result, fatal := e.execute(ctx, node)
		if fatal != nil {
			return fatal
		}
		result.ID, result.Attempt = node.ID, attempt
		last = result
		if result.Status == NodeStatusCompleted || attempt == attempts {
			break
		}
		if delay := node.backoff(); delay > 0 {
			if err := e.sleep(ctx, delay); err != nil {
				return err
			}
		}
	}
	return e.settle(ctx, node, last)
}

// skipReason applies the node's condition. Without one, a node needs all of its
// dependencies to have completed; a condition is what lets a node run on an
// upstream failure instead.
func (e *execution) skipReason(node Node) (bool, string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if node.When != nil {
		actual := e.statuses[node.When.Node]
		if actual == node.When.Status {
			return false, ""
		}
		return true, fmt.Sprintf("condition not met: %s is %s, want %s",
			node.When.Node, actual, node.When.Status)
	}
	for _, need := range node.dependencies() {
		if status := e.statuses[need]; status != NodeStatusCompleted {
			return true, fmt.Sprintf("dependency %s is %s", need, status)
		}
	}
	return false, ""
}

// permitted checks the capability a task node's role implies against the spec's
// permissions, which default to denying the host entirely.
func (e *execution) permitted(node Node) error {
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

// execute performs one attempt. The returned error is reserved for failures of
// the host itself; a failed task is a NodeResult so the graph can react to it.
func (e *execution) execute(ctx context.Context, node Node) (NodeResult, error) {
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
		// The children are ordinary nodes that already ran in an earlier wave;
		// this node is the join, so its outcome is whether they all completed.
		return e.join(node), nil
	case NodeTask:
		return e.spawn(attemptCtx, node)
	default:
		return NodeResult{}, fmt.Errorf("%w: unsupported node kind %q", ErrInvalidSpec, node.Kind)
	}
}

// spawn runs a task node with the attempt context. A timeout therefore reaches
// the underlying turn before this attempt returns and a retry can begin.
func (e *execution) spawn(ctx context.Context, node Node) (NodeResult, error) {
	result, err := e.driver.SpawnTask(ctx, TaskRequest{
		Role: node.Role, Prompt: firstNonEmpty(node.Prompt, e.spec.Goal),
		Profile: node.Profile, Schema: node.Schema,
	})
	if ctx.Err() != nil {
		return NodeResult{
			Status: NodeStatusFailed,
			Reason: fmt.Sprintf("node %s: %v", node.ID, ctx.Err()),
		}, nil
	}
	if err != nil {
		return NodeResult{Status: NodeStatusFailed, Reason: err.Error()}, nil
	}
	if !result.Success {
		return NodeResult{
			Status: NodeStatusFailed,
			Reason: firstNonEmpty(result.Error, "task failed"),
		}, nil
	}
	return NodeResult{
		Status: NodeStatusCompleted, Content: result.Content,
	}, nil
}

func (e *execution) join(node Node) NodeResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, child := range node.Children {
		if status := e.statuses[child]; status != NodeStatusCompleted {
			return NodeResult{
				Status: NodeStatusFailed,
				Reason: fmt.Sprintf("child %s is %s", child, status),
			}
		}
	}
	return NodeResult{Status: NodeStatusCompleted}
}

func (e *execution) settle(ctx context.Context, node Node, result NodeResult) error {
	if result.ID == "" {
		result.ID = node.ID
	}
	if result.Status == "" {
		result.Status = NodeStatusFailed
		result.Reason = firstNonEmpty(result.Reason, "node produced no status")
	}
	e.mu.Lock()
	e.statuses[node.ID] = result.Status
	e.results[node.ID] = result
	e.mu.Unlock()

	if e.record == nil {
		return nil
	}
	at := e.now()
	err := e.record.NodeSettled(ctx, e.run, NodeRecord{
		ID: node.ID, Status: result.Status, Attempt: result.Attempt,
		Reason: result.Reason, Content: result.Content, StartedAt: at, EndedAt: at,
	})
	if err != nil {
		return fmt.Errorf("checkpoint node %q: %w", node.ID, err)
	}
	return nil
}

// failure turns node outcomes into the run's verdict. A skipped node is not a
// failure: skipping is what a condition asked for.
func (e *execution) failure() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	failed := make([]string, 0, len(e.results))
	for id, result := range e.results {
		if result.Status == NodeStatusFailed {
			failed = append(failed, id+": "+firstNonEmpty(result.Reason, "failed"))
		}
	}
	if len(failed) == 0 {
		return nil
	}
	sort.Strings(failed)
	return errors.New(failed[0])
}

func (e *execution) ordered(nodes []Node) []NodeResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	results := make([]NodeResult, 0, len(nodes))
	for _, node := range nodes {
		if result, ok := e.results[node.ID]; ok {
			results = append(results, result)
		}
	}
	return results
}

// summarize reports the last content a node produced in dependency order.
// Reading it off the ordered results rather than off whichever goroutine
// finished last keeps a concurrent run's output reproducible.
func summarize(results []NodeResult) json.RawMessage {
	content := ""
	for _, result := range results {
		if result.Status == NodeStatusCompleted && result.Content != "" {
			content = result.Content
		}
	}
	if content != "" {
		return json.RawMessage(strconvQuote(content))
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

func strconvQuote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}
