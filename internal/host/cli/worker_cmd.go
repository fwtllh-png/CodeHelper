package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/spf13/cobra"
)

// workerReady is what a supervisor reads to know the worker is claiming. It
// names the executors on purpose: a task whose executor no worker runs waits
// forever, and this is where an operator sees which ones are covered.
type workerReady struct {
	Type        string   `json:"type"`
	PID         int      `json:"pid"`
	Owner       string   `json:"owner"`
	Executors   []string `json:"executors"`
	MaxParallel int      `json:"max_parallel"`
	DataDir     string   `json:"data_dir"`
	Workspace   string   `json:"workspace"`
}

func newWorkerCommand(
	ctx context.Context, stdout, stderr io.Writer, setCode func(int),
) *cobra.Command {
	cmd := &cobra.Command{Use: "worker", Short: "Execute durable background tasks"}
	cmd.Run = func(c *cobra.Command, args []string) {
		_, _ = fmt.Fprintln(stderr, "codehelper: worker requires a subcommand (run|enqueue|list)")
		setCode(2)
	}

	openTasks := func(dataDir string) (*taskstate.Repository, *state.Store, bool) {
		if dataDir == "" {
			_, _ = fmt.Fprintln(stderr, "codehelper: worker requires --data-dir")
			setCode(2)
			return nil, nil, false
		}
		store, err := state.Open(context.Background(), state.Options{DataDir: dataDir})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: worker: %v\n", err)
			setCode(1)
			return nil, nil, false
		}
		return taskstate.NewSQLiteRepository(store.SQLite()), store, true
	}

	run := newWorkerRunCommand(ctx, stdout, stderr, setCode)
	enqueue := newWorkerEnqueueCommand(stdout, stderr, setCode, openTasks)
	list := newWorkerListCommand(stdout, stderr, setCode, openTasks)
	cmd.AddCommand(run, enqueue, list)
	return cmd
}

// newWorkerRunCommand is the deployment form of the scheduler: a foreground
// process whose only job is to execute durable tasks. The other hosts can run one
// too, but they exist to serve a user and stop when that user leaves; background
// work needs a process that outlives them.
func newWorkerRunCommand(
	ctx context.Context, stdout, stderr io.Writer, setCode func(int),
) *cobra.Command {
	cmd := &cobra.Command{
		Use: "run", Short: "Run the scheduler in the foreground until interrupted",
	}
	cmd.Flags().String("data-dir", "", "persistent state directory (required)")
	cmd.Flags().String("config", "", "TOML configuration file")
	cmd.Flags().String("provider-fixture", "", "directory containing an HTTP provider fixture")
	cmd.Flags().String("workspace", ".", "workspace root")
	cmd.Flags().String("posture", "never", "tool permission posture")
	cmd.Flags().String("repository-rules", "", "JSON repository policy rules")
	cmd.Flags().Int("max-parallel", 0, "tasks to run at once; zero uses config")
	cmd.Run = func(c *cobra.Command, args []string) {
		flags := c.Flags()
		dataDir, _ := flags.GetString("data-dir")
		configPath, _ := flags.GetString("config")
		fixture, _ := flags.GetString("provider-fixture")
		workspace, _ := flags.GetString("workspace")
		posture, _ := flags.GetString("posture")
		repositoryRules, _ := flags.GetString("repository-rules")
		parallel, _ := flags.GetInt("max-parallel")
		if len(args) != 0 {
			_, _ = fmt.Fprintln(stderr, "codehelper: worker run accepts flags only")
			setCode(2)
			return
		}
		if dataDir == "" {
			// Without a shared database this process would claim from a scratch file
			// nobody else writes to, which looks like a working worker that never
			// finds work.
			_, _ = fmt.Fprintln(stderr, "codehelper: worker run --data-dir is required")
			setCode(2)
			return
		}
		// A background turn has nobody to ask, so approvals are denied rather than
		// queued. "never" is the posture that says so out loud; the looser postures
		// are allowed because an operator may accept them for trusted automation.
		if !oneOf(posture, "suggest", "auto", "bypass", "never") {
			_, _ = fmt.Fprintln(
				stderr, "codehelper: worker run --posture must be suggest, auto, bypass, or never",
			)
			setCode(2)
			return
		}
		enabled, tools := true, true
		overrides := config.Overrides{
			StateDataDir: &dataDir, Workspace: &workspace,
			Tools: &tools, WorkerEnabled: &enabled,
		}
		if flags.Changed("max-parallel") {
			if parallel <= 0 {
				_, _ = fmt.Fprintln(stderr, "codehelper: worker run --max-parallel must be positive")
				setCode(2)
				return
			}
			overrides.WorkerMaxParallel = &parallel
		}
		setCode(runWorker(ctx, workerRunOptions{
			DataDir: dataDir, ConfigPath: configPath, Fixture: fixture,
			Posture: posture, RepositoryRules: repositoryRules, Overrides: overrides,
		}, stdout, stderr))
	}
	return cmd
}

type workerRunOptions struct {
	DataDir         string
	ConfigPath      string
	Fixture         string
	Posture         string
	RepositoryRules string
	Overrides       config.Overrides
}

func runWorker(
	ctx context.Context, options workerRunOptions, stdout, stderr io.Writer,
) int {
	loaded, err := config.Load(config.LoadOptions{
		Path: options.ConfigPath, Overrides: options.Overrides,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: worker config: %v\n", err)
		return 1
	}
	store, err := state.Open(ctx, state.Options{
		DataDir: options.DataDir, BusyTimeout: loaded.Config.State.BusyTimeout,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: worker state: %v\n", err)
		return 1
	}
	application, err := wire.NewExec(ctx, wire.ExecOptions{
		ConfigPath: options.ConfigPath, ConfigOverrides: options.Overrides,
		FixturePath: options.Fixture, Permission: options.Posture,
		RepositoryRulesPath: options.RepositoryRules, PersistentStore: store,
	})
	if err != nil {
		_ = store.CloseAll(context.Background())
		_, _ = fmt.Fprintf(stderr, "codehelper: worker setup: %v\n", err)
		return 1
	}
	scheduler := application.Scheduler()
	if scheduler == nil {
		closeWorker(application, stderr)
		_, _ = fmt.Fprintln(stderr, "codehelper: worker started no scheduler")
		return 1
	}
	ready := workerReady{
		Type: "ready", PID: os.Getpid(), Owner: scheduler.Owner(),
		Executors: scheduler.Executors(), MaxParallel: scheduler.MaxParallel(),
		DataDir: options.DataDir, Workspace: loaded.Config.Execution.Workspace,
	}
	if err := json.NewEncoder(stdout).Encode(ready); err != nil {
		closeWorker(application, stderr)
		_, _ = fmt.Fprintf(stderr, "codehelper: worker readiness: %v\n", err)
		return 1
	}

	<-ctx.Done()
	// Close drains: in-flight tasks go back to the queue with their attempt
	// returned, so a restart is not a failed task.
	closeContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	closeErr := application.Close(closeContext)
	cancel()
	if closeErr != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: worker close: %v\n", closeErr)
		return 1
	}
	return 0
}

// newWorkerEnqueueCommand is how an operator creates executable work. It is
// separate from the task tools on purpose: those write the model's own board,
// which must never become live turns, so an executable task is something a person
// asks for explicitly.
func newWorkerEnqueueCommand(
	stdout, stderr io.Writer, setCode func(int),
	openTasks func(string) (*taskstate.Repository, *state.Store, bool),
) *cobra.Command {
	cmd := &cobra.Command{Use: "enqueue", Short: "Queue a task for a worker to execute"}
	cmd.Flags().String("data-dir", "", "persistent state directory (required)")
	cmd.Flags().String("workspace", ".", "workspace root recorded with the session")
	cmd.Flags().String("session-id", "worker-cli", "session the task belongs to")
	cmd.Flags().String(
		"executor", taskstate.ExecutorAgentTurn,
		"executor: agent_turn, workflow_run, or shell_command",
	)
	cmd.Flags().String("prompt", "", "agent prompt or shell command description")
	cmd.Flags().String("role", "general", "agent role: general, explore, plan, review, implementer, verifier")
	cmd.Flags().String("command", "", "shell_command command")
	cmd.Flags().String("cwd", "", "shell_command workspace-relative cwd")
	cmd.Flags().Duration("timeout", 0, "shell_command timeout")
	cmd.Flags().String("workflow-spec", "", "workflow_run JSON spec path")
	cmd.Flags().Bool("idempotent", false, "allow workflow_run or shell_command task-level retry")
	cmd.Flags().Int("max-attempts", 1, "attempts before the task is failed")
	cmd.Flags().Bool("json", false, "emit JSON")
	cmd.Run = func(c *cobra.Command, args []string) {
		flags := c.Flags()
		dataDir, _ := flags.GetString("data-dir")
		workspace, _ := flags.GetString("workspace")
		sessionID, _ := flags.GetString("session-id")
		executor, _ := flags.GetString("executor")
		prompt, _ := flags.GetString("prompt")
		role, _ := flags.GetString("role")
		command, _ := flags.GetString("command")
		cwd, _ := flags.GetString("cwd")
		timeout, _ := flags.GetDuration("timeout")
		workflowSpec, _ := flags.GetString("workflow-spec")
		idempotent, _ := flags.GetBool("idempotent")
		attempts, _ := flags.GetInt("max-attempts")
		asJSON, _ := flags.GetBool("json")
		if attempts < 1 {
			_, _ = fmt.Fprintln(stderr, "codehelper: worker enqueue --max-attempts must be positive")
			setCode(2)
			return
		}
		if attempts > 1 && executor != taskstate.ExecutorAgentTurn && !idempotent {
			_, _ = fmt.Fprintln(
				stderr,
				"codehelper: worker enqueue retries for workflow_run or shell_command require --idempotent",
			)
			setCode(2)
			return
		}
		var (
			payloadValue any
			kind         string
		)
		switch executor {
		case taskstate.ExecutorAgentTurn:
			if prompt == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: worker enqueue agent_turn requires --prompt")
				setCode(2)
				return
			}
			// The role decides whether the child may write, so an unrecognized one
			// is refused here rather than by whichever worker claims it.
			if _, err := subagent.ParseRole(role); err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: worker enqueue --role: %v\n", err)
				setCode(2)
				return
			}
			kind = "agent"
			payloadValue = wire.AgentTurnPayload{
				Version: wire.AgentTurnPayloadVersion, Prompt: prompt, Role: role,
			}
		case taskstate.ExecutorShellCommand:
			if command == "" || timeout < 0 {
				_, _ = fmt.Fprintln(
					stderr,
					"codehelper: worker enqueue shell_command requires --command and a non-negative --timeout",
				)
				setCode(2)
				return
			}
			kind = "shell"
			payloadValue = wire.ShellCommandPayload{
				Version: wire.ShellCommandPayloadVersion,
				Command: command, CWD: cwd, TimeoutMS: timeout.Milliseconds(),
				Description: prompt, Idempotent: idempotent,
			}
		case taskstate.ExecutorWorkflowRun:
			if workflowSpec == "" {
				_, _ = fmt.Fprintln(
					stderr, "codehelper: worker enqueue workflow_run requires --workflow-spec",
				)
				setCode(2)
				return
			}
			spec, err := loadWorkflowSpec(workflowSpec)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: worker enqueue workflow spec: %v\n", err)
				setCode(2)
				return
			}
			if err := spec.Validate(); err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: worker enqueue workflow spec: %v\n", err)
				setCode(2)
				return
			}
			kind = "workflow"
			payloadValue = wire.WorkflowRunPayload{
				Version: wire.WorkflowRunPayloadVersion,
				Spec:    spec, Idempotent: idempotent,
			}
		default:
			_, _ = fmt.Fprintf(stderr, "codehelper: worker enqueue unknown executor %q\n", executor)
			setCode(2)
			return
		}
		payload, err := json.Marshal(payloadValue)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: worker enqueue: %v\n", err)
			setCode(1)
			return
		}
		repo, store, ok := openTasks(dataDir)
		if !ok {
			return
		}
		defer func() { _ = store.Close(context.Background()) }()
		if err := repo.EnsureSession(context.Background(), sessionID, workspace); err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: worker enqueue session: %v\n", err)
			setCode(1)
			return
		}
		id, err := taskIdentifier()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: worker enqueue: %v\n", err)
			setCode(1)
			return
		}
		created, err := repo.Create(context.Background(), taskstate.Task{
			ID: id, SessionID: sessionID, Kind: kind,
			Executor: executor, MaxAttempts: attempts,
			Payload: payload,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: worker enqueue: %v\n", err)
			setCode(1)
			return
		}
		if asJSON {
			_ = json.NewEncoder(stdout).Encode(map[string]any{
				"task_id": created.ID, "executor": created.Executor,
				"state": string(created.State), "max_attempts": created.MaxAttempts,
			})
		} else {
			_, _ = fmt.Fprintf(stdout, "task_id=%s\n", created.ID)
		}
		setCode(0)
	}
	return cmd
}

// newWorkerListCommand shows executable tasks and who holds them. Non-executable
// tasks are excluded: they are the model's notes, and mixing them in would make
// the queue look far longer than it is.
func newWorkerListCommand(
	stdout, stderr io.Writer, setCode func(int),
	openTasks func(string) (*taskstate.Repository, *state.Store, bool),
) *cobra.Command {
	cmd := &cobra.Command{Use: "list", Short: "List executable tasks and their leases"}
	cmd.Flags().String("data-dir", "", "persistent state directory (required)")
	cmd.Flags().String("state", "", "filter by state: queued, running, completed, failed")
	cmd.Flags().Int("limit", 50, "maximum rows")
	cmd.Flags().Bool("json", false, "emit JSON")
	cmd.Run = func(c *cobra.Command, args []string) {
		flags := c.Flags()
		dataDir, _ := flags.GetString("data-dir")
		wanted, _ := flags.GetString("state")
		limit, _ := flags.GetInt("limit")
		asJSON, _ := flags.GetBool("json")
		if limit <= 0 {
			_, _ = fmt.Fprintln(stderr, "codehelper: worker list --limit must be positive")
			setCode(2)
			return
		}
		repo, store, ok := openTasks(dataDir)
		if !ok {
			return
		}
		defer func() { _ = store.Close(context.Background()) }()
		values, err := repo.List(
			context.Background(), taskstate.Filter{State: taskstate.State(wanted)}, limit,
		)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: worker list: %v\n", err)
			setCode(1)
			return
		}
		rows := make([]map[string]any, 0, len(values))
		for _, value := range values {
			if value.Executor == "" {
				continue
			}
			row := map[string]any{
				"id": value.ID, "executor": value.Executor, "state": string(value.State),
				"attempt": value.Attempt, "max_attempts": value.MaxAttempts,
			}
			if value.LeaseOwner != "" {
				row["lease_owner"] = value.LeaseOwner
			}
			if value.NextAttemptAt != nil {
				row["next_attempt_at"] = value.NextAttemptAt.UTC().Format(time.RFC3339Nano)
			}
			if value.FailureReason != "" {
				row["failure_reason"] = value.FailureReason
			}
			rows = append(rows, row)
		}
		if asJSON {
			_ = json.NewEncoder(stdout).Encode(map[string]any{
				"data_dir": dataDir, "tasks": rows,
			})
		} else {
			for _, row := range rows {
				_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\t%d/%d\n",
					row["id"], row["executor"], row["state"], row["attempt"], row["max_attempts"])
			}
		}
		setCode(0)
	}
	return cmd
}

// taskIdentifier is random rather than time-based because two operators queueing
// at once would otherwise collide on the primary key.
func taskIdentifier() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "task_" + hex.EncodeToString(raw), nil
}

func closeWorker(application *wire.Session, stderr io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := application.Close(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: worker cleanup: %v\n", err)
	}
}
