package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/fleet"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow/jsvm"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow/orchestrate"
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
				defer session.Close()
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
			workflowRuntime := workflow.NewRuntime()
			runOptions := workflow.RunOptions{
				ID: runID, Spec: spec, Driver: driver,
				SessionID: "workflow-cli", Workspace: workspace,
			}
			if session != nil {
				workflowRuntime = workflow.NewRuntimeWithControllerAndBudget(
					session.Fleet.Controller(),
					session.Budget,
				)
				runOptions.LaneID = session.LaneID
			}
			result, err := workflowRuntime.Run(ctx, runOptions)
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
				payload["resumed"] = resumedNodes(result.Nodes) > 0
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
	run.Flags().String("id", "", "run id; reusing one resumes its durable WorkGraph")
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

// newWorkflowStatusCommand shows the durable per-node WorkGraph projection.
func newWorkflowStatusCommand(
	ctx context.Context, stdout, stderr io.Writer, setCode func(int),
) *cobra.Command {
	cmd := &cobra.Command{Use: "status", Short: "Show a workflow run and its durable nodes"}
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
		ledger, err := fleet.Open(filepath.Join(dataDir, "fleet"))
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: workflow status: %v\n", err)
			setCode(1)
			return
		}
		defer ledger.Close()
		view, err := ledger.Inspect(runID, 50)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: workflow status: %v\n", err)
			setCode(1)
			return
		}
		rows := make([]map[string]any, 0, len(view.Run.View.Nodes))
		for _, node := range view.Run.View.Nodes {
			row := map[string]any{
				"id": node.ID, "status": string(node.State),
				"attempts":         node.AttemptCount,
				"authority_digest": node.AuthorityDigest,
			}
			if node.ResultRef != "" {
				row["output_handle"] = node.ResultRef
			}
			if node.Reason != "" {
				row["reason"] = node.Reason
			}
			rows = append(rows, row)
		}
		if asJSON {
			_ = json.NewEncoder(stdout).Encode(map[string]any{
				"id": view.Run.ID, "status": string(view.Run.Status),
				"revision": view.Run.Revision, "nodes": rows,
				"attempts": view.Run.View.Attempts, "audit": view.Audit,
			})
		} else {
			_, _ = fmt.Fprintf(stdout, "id=%s status=%s nodes=%d\n",
				view.Run.ID, view.Run.Status, len(rows))
			for _, row := range rows {
				_, _ = fmt.Fprintf(stdout, "  %s\t%s\n", row["id"], row["status"])
			}
		}
		setCode(0)
	}
	return cmd
}

func resumedNodes(nodes []workflow.NodeResult) int {
	count := 0
	for _, node := range nodes {
		if node.Resumed {
			count++
		}
	}
	return count
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
