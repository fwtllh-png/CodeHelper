// Package worker executes durable tasks. It is the one place that claims work
// from the task repository: leases, heartbeats, reclaim and retry all live here
// so that no second scheduler can disagree with this one.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/tracecontext"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/automation"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
)

// Outcome is what an executor made of a task.
type Outcome struct {
	// State must be completed, failed, or waiting. Waiting means the task needs a
	// person, which is different from needing another attempt.
	State  task.State
	Result json.RawMessage
	Reason string
	// Retryable asks for another attempt when the task has attempts left. An
	// executor that cannot tell whether its side effects already landed must
	// leave this false: a retry that repeats an external action is worse than a
	// failure someone reads.
	Retryable         bool
	ThreadID          string
	TurnID            string
	PermissionDigests []string
}

// Executor runs one kind of task to completion.
type Executor interface {
	// Name is the executor column value this implementation answers to.
	Name() string
	// Execute blocks until the task is done or ctx is canceled. Cancellation is
	// not a failure: the scheduler returns the task to the queue.
	Execute(ctx context.Context, value task.Task) (Outcome, error)
}

// Options configures a Scheduler.
type Options struct {
	Tasks *task.Repository
	// Automations is optional. When set, the scheduler is what makes a schedule
	// mean something: before this existed, automations were ticked once at
	// session open and never again.
	Automations *automation.Repository
	// Owner identifies this scheduler in the lease columns. It must be unique per
	// process, because fencing compares it literally.
	Owner              string
	Executors          []Executor
	SessionID          string
	WorkspaceRoot      string
	Lease              time.Duration
	Heartbeat          time.Duration
	ClaimInterval      time.Duration
	ReclaimInterval    time.Duration
	AutomationInterval time.Duration
	MaxParallel        int
	Backoff            task.Backoff
	Clock              func() time.Time
	Logger             *slog.Logger
	DrainEffects       func(context.Context) error
}

// Scheduler claims tasks, keeps their leases alive while they run, settles their
// outcomes, and puts back what it could not finish.
type Scheduler struct {
	tasks         *task.Repository
	automations   *automation.Repository
	owner         string
	sessionID     string
	workspaceRoot string
	executors     map[string]Executor
	names         []string

	lease              time.Duration
	heartbeat          time.Duration
	claimInterval      time.Duration
	reclaimInterval    time.Duration
	automationInterval time.Duration
	maxParallel        int
	backoff            task.Backoff
	clock              func() time.Time
	logger             *slog.Logger
	drainEffects       func(context.Context) error

	mu       sync.Mutex
	running  map[string]context.CancelFunc
	draining bool

	inFlight sync.WaitGroup
	loops    sync.WaitGroup
	stop     context.CancelFunc
	started  bool
}

// New validates options and returns a scheduler that has not started yet.
func New(options Options) (*Scheduler, error) {
	if options.Tasks == nil {
		return nil, errors.New("worker scheduler requires a task repository")
	}
	if options.Owner == "" {
		return nil, errors.New("worker scheduler requires an owner")
	}
	if options.WorkspaceRoot == "" {
		return nil, errors.New("worker scheduler requires a workspace root")
	}
	scheduler := &Scheduler{
		tasks:         options.Tasks,
		automations:   options.Automations,
		owner:         options.Owner,
		sessionID:     options.SessionID,
		workspaceRoot: options.WorkspaceRoot,
		executors:     make(map[string]Executor, len(options.Executors)),
		lease:         options.Lease,
		heartbeat:     options.Heartbeat,
		maxParallel:   options.MaxParallel,
		backoff:       options.Backoff,
		clock:         options.Clock,
		logger:        options.Logger,
		drainEffects:  options.DrainEffects,
		running:       make(map[string]context.CancelFunc),

		claimInterval:      options.ClaimInterval,
		reclaimInterval:    options.ReclaimInterval,
		automationInterval: options.AutomationInterval,
	}
	for _, executor := range options.Executors {
		if executor == nil {
			return nil, errors.New("worker scheduler received a nil executor")
		}
		name := executor.Name()
		if name == "" {
			return nil, errors.New("worker executor name is required")
		}
		if _, duplicate := scheduler.executors[name]; duplicate {
			return nil, fmt.Errorf("worker executor %q is registered twice", name)
		}
		scheduler.executors[name] = executor
		scheduler.names = append(scheduler.names, name)
	}
	scheduler.applyDefaults()
	return scheduler, nil
}

func (s *Scheduler) applyDefaults() {
	if s.lease <= 0 {
		s.lease = 30 * time.Second
	}
	if s.heartbeat <= 0 {
		// A third of the lease leaves room for two missed beats before another
		// worker is entitled to take the task away.
		s.heartbeat = s.lease / 3
	}
	if s.heartbeat <= 0 {
		s.heartbeat = time.Second
	}
	if s.claimInterval <= 0 {
		s.claimInterval = time.Second
	}
	if s.reclaimInterval <= 0 {
		s.reclaimInterval = s.lease
	}
	if s.automationInterval <= 0 {
		s.automationInterval = 30 * time.Second
	}
	if s.maxParallel <= 0 {
		s.maxParallel = 2
	}
	if s.clock == nil {
		s.clock = func() time.Time { return time.Now().UTC() }
	}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
}

// Start launches the claim, reclaim and automation loops. It returns as soon as
// they are running; Close stops them and drains what they started.
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("worker scheduler is already started")
	}
	if s.draining {
		s.mu.Unlock()
		return errors.New("worker scheduler is closed")
	}
	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.stop, s.started = cancel, true
	s.mu.Unlock()

	s.loop(loopCtx, s.claimInterval, func(ctx context.Context) {
		if _, err := s.Dispatch(ctx); err != nil {
			s.logger.Warn("claim tasks", "error", err)
		}
	})
	s.loop(loopCtx, s.reclaimInterval, func(ctx context.Context) {
		if _, err := s.Reclaim(ctx); err != nil {
			s.logger.Warn("reclaim tasks", "error", err)
		}
	})
	if s.automations != nil {
		s.loop(loopCtx, s.automationInterval, func(ctx context.Context) {
			if _, err := s.TickAutomations(ctx); err != nil {
				s.logger.Warn("tick automations", "error", err)
			}
		})
	}
	return nil
}

func (s *Scheduler) loop(ctx context.Context, interval time.Duration, step func(context.Context)) {
	s.loops.Add(1)
	go func() {
		defer s.loops.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		// The first iteration runs immediately so that a restart picks up work
		// that is already due instead of waiting out a full interval.
		step(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				step(ctx)
			}
		}
	}()
}

// Close stops claiming, interrupts what is running, and waits for those tasks to
// return to the queue. A drained task is queued rather than failed: a clean stop
// should leave work for the next process, not a list of failures to sort out.
func (s *Scheduler) Close() error {
	s.mu.Lock()
	if s.draining {
		s.mu.Unlock()
		return nil
	}
	s.draining = true
	stop := s.stop
	cancels := make([]context.CancelFunc, 0, len(s.running))
	for _, cancel := range s.running {
		cancels = append(cancels, cancel)
	}
	s.mu.Unlock()

	if stop != nil {
		stop()
	}
	s.loops.Wait()
	for _, cancel := range cancels {
		cancel()
	}
	s.inFlight.Wait()
	return nil
}

// Dispatch claims what this scheduler has room for and starts running it.
// It returns the number of tasks it started.
func (s *Scheduler) Dispatch(ctx context.Context) (int, error) {
	capacity := s.capacity()
	if capacity <= 0 || len(s.names) == 0 {
		return 0, nil
	}
	claimed, err := s.tasks.Claim(ctx, task.ClaimRequest{
		Owner: s.owner, Executors: s.names, Lease: s.lease,
		Limit: capacity, Now: s.clock(), SessionID: s.sessionID,
		WorkspaceRoot: s.workspaceRoot,
	})
	if err != nil {
		return 0, err
	}
	started := 0
	for _, value := range claimed {
		if s.start(ctx, value) {
			started++
		}
	}
	return started, nil
}

// Reclaim requeues tasks whose lease expired, including this scheduler's own if
// it ever stalls long enough to lose one.
func (s *Scheduler) Reclaim(ctx context.Context) (int, error) {
	reclaimed, err := s.tasks.Reclaim(ctx, s.clock(), s.backoff)
	if err != nil {
		return 0, err
	}
	for _, value := range reclaimed {
		s.logger.Info("reclaimed task with an expired lease",
			"task", value.ID, "attempt", value.Attempt, "state", string(value.State))
	}
	return len(reclaimed), nil
}

// TickAutomations enqueues every automation slot that has come due.
func (s *Scheduler) TickAutomations(ctx context.Context) (int, error) {
	if s.automations == nil {
		return 0, nil
	}
	runs, err := s.automations.Tick(ctx, s.clock())
	if err != nil {
		return 0, err
	}
	return len(runs), nil
}

// Wait blocks until nothing is running. Tests use it to observe settled state.
func (s *Scheduler) Wait() { s.inFlight.Wait() }

// Owner is the identity written into the lease columns. An operator reading two
// workers' leases needs this to tell which process holds what.
func (s *Scheduler) Owner() string { return s.owner }

// Executors lists the task kinds this scheduler claims. A task whose executor is
// not here waits for a worker that runs it.
func (s *Scheduler) Executors() []string {
	names := make([]string, len(s.names))
	copy(names, s.names)
	return names
}

// MaxParallel reports how many tasks may run at once.
func (s *Scheduler) MaxParallel() int { return s.maxParallel }

// InFlight reports how many tasks this scheduler is running.
func (s *Scheduler) InFlight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.running)
}

// WorkspaceRoot reports the filesystem authority this scheduler may claim for.
func (s *Scheduler) WorkspaceRoot() string {
	if s == nil {
		return ""
	}
	return s.workspaceRoot
}

func (s *Scheduler) capacity() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.draining {
		return 0
	}
	return s.maxParallel - len(s.running)
}

func (s *Scheduler) start(ctx context.Context, value task.Task) bool {
	executor, ok := s.executors[value.Executor]
	if !ok {
		// Claimed by executor name, so this cannot happen unless the registry
		// changed underneath us. Put it back rather than hold a lease nobody will
		// honor.
		s.requeue(ctx, value, task.ReasonDraining)
		return false
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.mu.Lock()
	if s.draining {
		s.mu.Unlock()
		cancel()
		s.requeue(ctx, value, task.ReasonDraining)
		return false
	}
	s.running[value.ID] = cancel
	s.mu.Unlock()

	s.inFlight.Add(1)
	go func() {
		defer s.inFlight.Done()
		defer func() {
			s.mu.Lock()
			delete(s.running, value.ID)
			s.mu.Unlock()
			cancel()
		}()
		s.run(runCtx, cancel, executor, value)
	}()
	return true
}

func (s *Scheduler) run(
	ctx context.Context, cancel context.CancelFunc, executor Executor, value task.Task,
) {
	stopHeartbeat := s.beat(ctx, cancel, value)

	if traced, traceErr := tracecontext.Child(ctx); traceErr == nil {
		ctx = traced
	}
	outcome, err := executor.Execute(ctx, value)
	stopHeartbeat()
	// Cancellation means the process is stopping or the lease was lost, and in
	// both cases the outcome belongs to whoever holds the task next.
	if ctx.Err() != nil {
		s.requeue(context.WithoutCancel(ctx), value, task.ReasonDraining)
		return
	}
	if err != nil {
		s.settleFailure(ctx, value, err)
		return
	}
	s.settle(ctx, value, outcome)
}

// beat keeps the lease alive while the executor works, and cancels the work if
// the lease is gone: two workers running the same task is the one outcome the
// lease exists to prevent.
func (s *Scheduler) beat(
	ctx context.Context,
	cancel context.CancelFunc,
	value task.Task,
) func() {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(s.heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				err := s.tasks.HeartbeatAttempt(
					ctx,
					value.ID,
					s.owner,
					value.LeaseEpoch,
					s.clock().Add(s.lease),
				)
				if err == nil {
					continue
				}
				if errors.Is(err, task.ErrClaimLost) {
					s.logger.Warn("lost the lease on a running task", "task", value.ID)
					cancel()
					return
				}
				s.logger.Warn("heartbeat task", "task", value.ID, "error", err)
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(done) })
		<-stopped
	}
}

func (s *Scheduler) settle(ctx context.Context, value task.Task, outcome Outcome) {
	if outcome.Retryable && outcome.State == task.StateFailed &&
		value.Attempt < value.MaxAttempts {
		s.requeueWithBackoff(ctx, value, task.ReasonRetry)
		return
	}
	state := outcome.State
	if state == "" {
		state = task.StateCompleted
	}
	reason := outcome.Reason
	if state == task.StateFailed && reason == "" {
		reason = "executor reported failure without a reason"
	}
	if outcome.ThreadID != "" || outcome.TurnID != "" {
		if err := s.tasks.RecordAttemptExecution(
			ctx,
			value.ID,
			s.owner,
			value.LeaseEpoch,
			outcome.ThreadID,
			outcome.TurnID,
		); err != nil && !errors.Is(err, task.ErrClaimLost) {
			s.logger.Warn("record task attempt turn", "task", value.ID, "error", err)
		}
	}
	if _, err := s.tasks.SettleAttempt(
		ctx, value.ID, s.owner, value.LeaseEpoch, task.Transition{
			State: state, Result: outcome.Result, Reason: reason, At: s.clock(),
			PermissionDigests: append(
				[]string(nil),
				outcome.PermissionDigests...,
			),
		}); err != nil && !errors.Is(err, task.ErrClaimLost) {
		s.logger.Warn("settle task", "task", value.ID, "error", err)
	} else if err == nil {
		s.drainWorkGraphEffects(ctx)
	}
}

func (s *Scheduler) settleFailure(ctx context.Context, value task.Task, cause error) {
	if _, err := s.tasks.SettleAttempt(
		ctx, value.ID, s.owner, value.LeaseEpoch, task.Transition{
			State: task.StateFailed, Reason: cause.Error(), At: s.clock(),
		}); err != nil && !errors.Is(err, task.ErrClaimLost) {
		s.logger.Warn("settle failed task", "task", value.ID, "error", err)
	} else if err == nil {
		s.drainWorkGraphEffects(ctx)
	}
}

func (s *Scheduler) drainWorkGraphEffects(ctx context.Context) {
	if s.drainEffects == nil {
		return
	}
	if err := s.drainEffects(ctx); err != nil {
		s.logger.Warn("drain WorkGraph effects", "error", err)
	}
}

func (s *Scheduler) requeue(ctx context.Context, value task.Task, reason string) {
	if _, err := s.tasks.ReleaseAttempt(
		ctx, value.ID, s.owner, value.LeaseEpoch, reason, s.clock(), 0,
	); err != nil && !errors.Is(err, task.ErrClaimLost) {
		s.logger.Warn("requeue task", "task", value.ID, "reason", reason, "error", err)
	} else if err == nil {
		s.drainWorkGraphEffects(ctx)
	}
}

func (s *Scheduler) requeueWithBackoff(ctx context.Context, value task.Task, reason string) {
	if _, err := s.tasks.ReleaseAttempt(
		ctx,
		value.ID,
		s.owner,
		value.LeaseEpoch,
		reason,
		s.clock(),
		s.backoff.Delay(value.Attempt),
	); err != nil && !errors.Is(err, task.ErrClaimLost) {
		s.logger.Warn("requeue task", "task", value.ID, "reason", reason, "error", err)
	} else if err == nil {
		s.drainWorkGraphEffects(ctx)
	}
}
