package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow/checkpoint"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow/jsvm"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow/orchestrate"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state/cas"
	"github.com/spf13/cobra"
)

func newWorkflowCommand(ctx context.Context, stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	cmd := &cobra.Command{Use: "workflow", Short: "Validate and run Workflow IR specs"}
	cmd.Run = func(c *cobra.Command, args []string) {
		_, _ = fmt.Fprintln(stderr, "codehelper: workflow requires a subcommand (validate|run|status)")
		setCode(2)
	}

	validate := &cobra.Command{
		Use: "validate", Short: "Validate a workflow JSON spec",
		Run: func(cmd *cobra.Command, args []string) {
			specPath, _ := cmd.Flags().GetString("spec")
			asJSON, _ := cmd.Flags().GetBool("json")
			if specPath == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: workflow validate requires --spec")
				setCode(2)
				return
			}
			spec, err := loadWorkflowSpec(specPath)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: workflow validate: %v\n", err)
				setCode(1)
				return
			}
			if err := spec.Validate(); err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: workflow validate: %v\n", err)
				setCode(1)
				return
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{
					"ok": true, "goal": spec.Goal, "nodes": len(spec.Nodes),
				})
			} else {
				_, _ = fmt.Fprintf(stdout, "ok nodes=%d goal=%q\n", len(spec.Nodes), spec.Goal)
			}
			setCode(0)
		},
	}
	validate.Flags().String("spec", "", "workflow JSON spec path")
	validate.Flags().Bool("json", false, "emit JSON")

	run := &cobra.Command{
		Use:   "run",
		Short: "Run a workflow (RuntimeDriver by default; --driver=fake for unit)",
		Run: func(cmd *cobra.Command, args []string) {
			specPath, _ := cmd.Flags().GetString("spec")
			scriptPath, _ := cmd.Flags().GetString("script")
			dataDir, _ := cmd.Flags().GetString("data-dir")
			driverName, _ := cmd.Flags().GetString("driver")
			fixturePath, _ := cmd.Flags().GetString("provider-fixture")
			workspace, _ := cmd.Flags().GetString("workspace")
			asJSON, _ := cmd.Flags().GetBool("json")
			if specPath == "" && scriptPath == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: workflow run requires --spec or --script")
				setCode(2)
				return
			}
			if specPath != "" && scriptPath != "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: workflow run accepts only one of --spec or --script")
				setCode(2)
				return
			}
			if driverName == "" {
				driverName = "runtime"
			}
			if scriptPath != "" && driverName == "runtime" {
				driverName = "fake"
			}
			var inner workflow.Driver
			var runtimeDriver *orchestrate.RuntimeDriver
			switch driverName {
			case "fake":
				inner = &jsvm.FakeDriver{}
			case "runtime":
				if fixturePath == "" && os.Getenv("CODEHELPER_ALLOW_LIVE_PROVIDER") == "" {
					_, _ = fmt.Fprintln(stderr, "codehelper: workflow run --driver=runtime requires --provider-fixture (or CODEHELPER_ALLOW_LIVE_PROVIDER)")
					setCode(2)
					return
				}
				if workspace == "" {
					workspace = "."
				}
				runtimeDriver = &orchestrate.RuntimeDriver{
					FixturePath: fixturePath, DataDir: dataDir, Workspace: workspace,
				}
				inner = runtimeDriver
			default:
				_, _ = fmt.Fprintf(stderr, "codehelper: workflow run unknown --driver %q (fake|runtime)\n", driverName)
				setCode(2)
				return
			}
			driver := inner
			var session *orchestrate.Session
			runID, _ := cmd.Flags().GetString("id")
			if runID == "" {
				runID = fmt.Sprintf("wf_%d", time.Now().UTC().UnixNano())
			}
			if dataDir != "" {
				var err error
				session, err = orchestrate.Open(dataDir, inner)
				if err != nil {
					_, _ = fmt.Fprintf(stderr, "codehelper: workflow orchestrate: %v\n", err)
					setCode(1)
					return
				}
				if err := session.Begin(ctx, runID); err != nil {
					_, _ = fmt.Fprintf(stderr, "codehelper: workflow begin: %v\n", err)
					setCode(1)
					return
				}
				driver = session.Driver()
			}
			if scriptPath != "" {
				source, err := os.ReadFile(scriptPath)
				if err != nil {
					_, _ = fmt.Fprintf(stderr, "codehelper: workflow run: %v\n", err)
					setCode(1)
					return
				}
				if workspace == "" {
					workspace = filepath.Dir(scriptPath)
				}
				raw, err := jsvm.New().RunScript(ctx, string(source), jsvm.Options{
					Driver: driver, Workspace: workspace,
				})
				if session != nil {
					status := "completed"
					if err != nil {
						status = "failed"
					}
					_ = session.Finalize(status)
				}
				if err != nil {
					_, _ = fmt.Fprintf(stderr, "codehelper: workflow run: %v\n", err)
					setCode(1)
					return
				}
				if asJSON {
					_ = json.NewEncoder(stdout).Encode(map[string]any{
						"id": runID, "status": "completed", "driver": driverName,
						"script": scriptPath, "result": json.RawMessage(raw),
					})
				} else {
					_, _ = fmt.Fprintf(stdout, "id=%s status=completed driver=%s script=%s\n",
						runID, driverName, scriptPath)
				}
				setCode(0)
				return
			}
			spec, err := loadWorkflowSpec(specPath)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: workflow run: %v\n", err)
				setCode(1)
				return
			}
			// Node outcomes are recorded only when there is a database to record
			// them in. Without --data-dir the run has no memory, and rerunning it
			// repeats every node.
			var record *workflowCheckpoint
			if dataDir != "" {
				record, err = openWorkflowCheckpoint(ctx, dataDir, runID, spec, workspace)
				if err != nil {
					_, _ = fmt.Fprintf(stderr, "codehelper: workflow checkpoint: %v\n", err)
					setCode(1)
					return
				}
				defer record.close()
			}
			result, err := workflow.NewRuntime().Run(ctx, workflow.RunOptions{
				ID: runID, Spec: spec, Driver: driver,
				Checkpoint: record.sink(),
			})
			if record != nil {
				record.settle(ctx, result, err)
			}
			if session != nil {
				status := string(result.Status)
				if status == "" {
					status = "failed"
				}
				_ = session.Finalize(status)
			}
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: workflow run: %v\n", err)
				setCode(1)
				return
			}
			if asJSON {
				payload := map[string]any{
					"id": result.ID, "status": result.Status, "goal": result.Goal,
					"driver": driverName, "nodes": result.Nodes,
				}
				if record != nil && record.resumed {
					payload["resumed"] = true
				}
				if runtimeDriver != nil && runtimeDriver.LastContent != "" {
					payload["turn_content"] = runtimeDriver.LastContent
				}
				if session != nil {
					for key, value := range session.Snapshot() {
						payload[key] = value
					}
				}
				_ = json.NewEncoder(stdout).Encode(payload)
			} else {
				_, _ = fmt.Fprintf(stdout, "id=%s status=%s driver=%s\n", result.ID, result.Status, driverName)
				if session != nil {
					_, _ = fmt.Fprintf(stdout, "fleet_run=%s lane=%s\n", session.RunID, session.LaneID)
				}
				if runtimeDriver != nil && runtimeDriver.LastContent != "" {
					_, _ = fmt.Fprintf(stdout, "turn_content=%s\n", runtimeDriver.LastContent)
				}
			}
			setCode(0)
		},
	}
	run.Flags().String("id", "", "run id; reusing one resumes that run from its checkpoint")
	run.Flags().String("spec", "", "workflow JSON spec path")
	run.Flags().String("script", "", "workflow JS script path (goja host)")
	run.Flags().String("data-dir", "", "record durable fleet+lane under this product data-dir")
	run.Flags().String("driver", "runtime", "task driver: runtime (default) or fake")
	run.Flags().String("provider-fixture", "", "HTTP provider fixture for RuntimeDriver (required unless live allowed)")
	run.Flags().String("workspace", "", "workspace root for script read()/path host")
	run.Flags().Bool("json", false, "emit JSON")

	status := newWorkflowStatusCommand(ctx, stdout, stderr, setCode)
	cmd.AddCommand(validate, run, status)
	return cmd
}

// newWorkflowStatusCommand shows a run's per-node checkpoint, which is what tells
// an operator what a resume would skip.
func newWorkflowStatusCommand(
	ctx context.Context, stdout, stderr io.Writer, setCode func(int),
) *cobra.Command {
	cmd := &cobra.Command{Use: "status", Short: "Show a workflow run and its node checkpoints"}
	cmd.Flags().String("data-dir", "", "persistent state directory (required)")
	cmd.Flags().String("id", "", "run id (required)")
	cmd.Flags().Bool("json", false, "emit JSON")
	cmd.Run = func(c *cobra.Command, args []string) {
		dataDir, _ := c.Flags().GetString("data-dir")
		runID, _ := c.Flags().GetString("id")
		asJSON, _ := c.Flags().GetBool("json")
		if dataDir == "" || runID == "" {
			_, _ = fmt.Fprintln(stderr, "codehelper: workflow status requires --data-dir and --id")
			setCode(2)
			return
		}
		store, err := state.Open(ctx, state.Options{DataDir: dataDir})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: workflow status: %v\n", err)
			setCode(1)
			return
		}
		defer func() { _ = store.Close(context.Background()) }()
		repository := checkpoint.NewSQLiteRepository(store.SQLite(), workflowOutputs(store))
		run, err := repository.Get(ctx, runID)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: workflow status: %v\n", err)
			setCode(1)
			return
		}
		nodes, err := repository.Nodes(ctx, runID)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: workflow status: %v\n", err)
			setCode(1)
			return
		}
		rows := make([]map[string]any, 0, len(nodes))
		for _, node := range nodes {
			row := map[string]any{
				"id": node.ID, "status": string(node.Status), "attempt": node.Attempt,
			}
			if node.Reason != "" {
				row["reason"] = node.Reason
			}
			// The output handle says where the node's result went, which is what
			// makes a resumed run able to report work it did before the restart.
			if node.OutputHandle != "" {
				row["output_handle"] = node.OutputHandle
			}
			rows = append(rows, row)
		}
		if asJSON {
			_ = json.NewEncoder(stdout).Encode(map[string]any{
				"id": run.ID, "status": string(run.Status), "goal": run.Goal,
				"spec_hash": run.SpecHash, "error": run.Error, "nodes": rows,
			})
		} else {
			_, _ = fmt.Fprintf(stdout, "id=%s status=%s nodes=%d\n",
				run.ID, run.Status, len(rows))
			for _, row := range rows {
				_, _ = fmt.Fprintf(stdout, "  %s\t%s\n", row["id"], row["status"])
			}
		}
		setCode(0)
	}
	return cmd
}

// workflowCheckpoint keeps the state store open for the length of a run and
// records the run header around it.
type workflowCheckpoint struct {
	store      *state.Store
	repository *checkpoint.Repository
	runID      string
	resumed    bool
}

func openWorkflowCheckpoint(
	ctx context.Context, dataDir, runID string, spec workflow.Spec, workspace string,
) (*workflowCheckpoint, error) {
	store, err := state.Open(ctx, state.Options{DataDir: dataDir})
	if err != nil {
		return nil, err
	}
	repository := checkpoint.NewSQLiteRepository(store.SQLite(), workflowOutputs(store))
	sessionID := "workflow-cli"
	if err := taskstate.NewSQLiteRepository(store.SQLite()).EnsureSession(
		ctx, sessionID, firstNonEmptyString(workspace, "."),
	); err != nil {
		_ = store.Close(context.Background())
		return nil, err
	}
	run, err := repository.Ensure(ctx, checkpoint.EnsureRequest{
		ID: runID, SessionID: sessionID, Spec: spec,
	})
	if err != nil {
		_ = store.Close(context.Background())
		return nil, err
	}
	return &workflowCheckpoint{
		store: store, repository: repository, runID: runID, resumed: run.Resumed,
	}, nil
}

// workflowOutputs points node output at the data directory's content store, so a
// resumed run reports what the nodes that already finished produced instead of an
// empty summary.
func workflowOutputs(store *state.Store) checkpoint.Outputs {
	if store == nil || store.Content() == nil {
		return nil
	}
	return contentstore.NewDurable(store.Content(), cas.ErrNotFound)
}

// sink returns the interface value the runtime wants, keeping a nil checkpoint
// nil rather than a non-nil interface holding a nil pointer.
func (c *workflowCheckpoint) sink() workflow.Checkpoint {
	if c == nil {
		return nil
	}
	return c.repository
}

func (c *workflowCheckpoint) settle(ctx context.Context, run workflow.Run, cause error) {
	status := run.Status
	if status == "" || status == workflow.RunRunning {
		status = workflow.RunFailed
	}
	failure := run.Error
	if failure == "" && cause != nil {
		failure = cause.Error()
	}
	_ = c.repository.Settle(ctx, c.runID, status, failure, time.Now().UTC())
}

func (c *workflowCheckpoint) close() {
	_ = c.store.Close(context.Background())
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func loadWorkflowSpec(path string) (workflow.Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return workflow.Spec{}, err
	}
	var spec workflow.Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return workflow.Spec{}, err
	}
	return spec, nil
}
