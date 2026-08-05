package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/fleet"
	"github.com/spf13/cobra"
)

func newFleetCommand(stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	// The ledger is an audit trail, not a queue: execution moved to the tasks
	// table and `codehelper worker` (RFC-007 D10). The verbs that used to schedule
	// work here — create, enqueue, interrupt, resume — are gone rather than
	// silently doing nothing.
	cmd := &cobra.Command{Use: "fleet", Short: "Read the Fleet JSONL audit trail"}
	cmd.Run = func(c *cobra.Command, args []string) {
		_, _ = fmt.Fprintln(stderr,
			"codehelper: fleet requires a subcommand (list|status|inspect|logs|profile); "+
				"to run background work use codehelper worker")
		setCode(2)
	}

	openLedger := func(dataDir string) (*fleet.Ledger, bool) {
		if dataDir == "" {
			_, _ = fmt.Fprintln(stderr, "codehelper: fleet requires --data-dir")
			setCode(2)
			return nil, false
		}
		ledger, err := fleet.Open(dataDir)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: fleet: %v\n", err)
			setCode(1)
			return nil, false
		}
		return ledger, true
	}

	list := &cobra.Command{
		Use: "list", Short: "List runs from a fleet ledger",
		Run: func(cmd *cobra.Command, args []string) {
			dataDir, _ := cmd.Flags().GetString("data-dir")
			asJSON, _ := cmd.Flags().GetBool("json")
			ledger, ok := openLedger(dataDir)
			if !ok {
				return
			}
			state, err := ledger.Replay()
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: fleet list: %v\n", err)
				setCode(1)
				return
			}
			ids := make([]string, 0, len(state.Runs))
			for id := range state.Runs {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			type row struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}
			rows := make([]row, 0, len(ids))
			for _, id := range ids {
				rows = append(rows, row{ID: id, Status: string(state.Runs[id].Status)})
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{
					"data_dir": dataDir, "runs": rows,
				})
			} else {
				for _, item := range rows {
					_, _ = fmt.Fprintf(stdout, "%s\t%s\n", item.ID, item.Status)
				}
			}
			setCode(0)
		},
	}
	list.Flags().String("data-dir", "", "fleet ledger root directory")
	list.Flags().Bool("json", false, "emit JSON")

	status := &cobra.Command{
		Use: "status", Short: "Show one fleet run and its tasks",
		Run: func(cmd *cobra.Command, args []string) {
			dataDir, _ := cmd.Flags().GetString("data-dir")
			runID, _ := cmd.Flags().GetString("id")
			asJSON, _ := cmd.Flags().GetBool("json")
			if runID == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: fleet status requires --data-dir and --id")
				setCode(2)
				return
			}
			ledger, ok := openLedger(dataDir)
			if !ok {
				return
			}
			state, err := ledger.Replay()
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: fleet status: %v\n", err)
				setCode(1)
				return
			}
			run, found := state.Runs[runID]
			if !found {
				_, _ = fmt.Fprintf(stderr, "codehelper: fleet status: run %q not found\n", runID)
				setCode(1)
				return
			}
			tasks := make([]fleet.Task, 0)
			for _, task := range state.Tasks {
				if task.RunID == runID {
					tasks = append(tasks, *task)
				}
			}
			sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
			heartbeats := map[string]any{}
			for worker, at := range state.Heartbeats {
				heartbeats[worker] = at.UTC().Format("2006-01-02T15:04:05Z")
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{
					"run": run, "tasks": tasks, "heartbeats": heartbeats,
				})
			} else {
				_, _ = fmt.Fprintf(stdout, "run=%s status=%s tasks=%d workers=%d\n",
					run.ID, run.Status, len(tasks), len(heartbeats))
				for _, task := range tasks {
					_, _ = fmt.Fprintf(stdout, "  %s\t%s\n", task.ID, task.Status)
				}
			}
			setCode(0)
		},
	}
	status.Flags().String("data-dir", "", "fleet ledger root directory")
	status.Flags().String("id", "", "run id")
	status.Flags().Bool("json", false, "emit JSON")

	inspect := &cobra.Command{
		Use: "inspect", Short: "Inspect a run with tasks and recent events",
		Run: func(cmd *cobra.Command, args []string) {
			dataDir, _ := cmd.Flags().GetString("data-dir")
			runID, _ := cmd.Flags().GetString("id")
			limit, _ := cmd.Flags().GetInt("limit")
			asJSON, _ := cmd.Flags().GetBool("json")
			if runID == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: fleet inspect requires --data-dir and --id")
				setCode(2)
				return
			}
			ledger, ok := openLedger(dataDir)
			if !ok {
				return
			}
			view, err := ledger.Inspect(runID, limit)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: fleet inspect: %v\n", err)
				setCode(1)
				return
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(view)
			} else {
				_, _ = fmt.Fprintf(stdout, "run=%s status=%s tasks=%d events=%d\n",
					view.Run.ID, view.Run.Status, len(view.Tasks), len(view.Events))
				for _, task := range view.Tasks {
					_, _ = fmt.Fprintf(stdout, "  task %s\t%s\n", task.ID, task.Status)
				}
			}
			setCode(0)
		},
	}
	inspect.Flags().String("data-dir", "", "fleet ledger root directory")
	inspect.Flags().String("id", "", "run id")
	inspect.Flags().Int("limit", 50, "max events")
	inspect.Flags().Bool("json", false, "emit JSON")

	logs := &cobra.Command{
		Use: "logs", Short: "Print recent ledger events for a run",
		Run: func(cmd *cobra.Command, args []string) {
			dataDir, _ := cmd.Flags().GetString("data-dir")
			runID, _ := cmd.Flags().GetString("id")
			limit, _ := cmd.Flags().GetInt("limit")
			asJSON, _ := cmd.Flags().GetBool("json")
			if runID == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: fleet logs requires --data-dir and --id")
				setCode(2)
				return
			}
			ledger, ok := openLedger(dataDir)
			if !ok {
				return
			}
			records, err := ledger.Logs(runID, limit)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: fleet logs: %v\n", err)
				setCode(1)
				return
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{"run_id": runID, "records": records})
			} else {
				for _, record := range records {
					_, _ = fmt.Fprintf(stdout, "%d\t%s\t%s\t%s\n",
						record.Sequence, record.Type, record.TaskID, record.Status)
				}
			}
			setCode(0)
		},
	}
	logs.Flags().String("data-dir", "", "fleet ledger root directory")
	logs.Flags().String("id", "", "run id")
	logs.Flags().Int("limit", 50, "max records")
	logs.Flags().Bool("json", false, "emit JSON")

	profile := &cobra.Command{
		Use: "profile", Short: "Show fleet roster/profile (workers, lease, heartbeat)",
		Run: func(cmd *cobra.Command, args []string) {
			path, _ := cmd.Flags().GetString("file")
			rosterDir, _ := cmd.Flags().GetString("roster")
			name, _ := cmd.Flags().GetString("name")
			asJSON, _ := cmd.Flags().GetBool("json")
			var (
				loaded fleet.Profile
				err    error
				source string
			)
			switch {
			case path != "":
				loaded, err = fleet.LoadProfile(path)
				source = path
			case rosterDir != "":
				loaded, err = fleet.LoadRoster(rosterDir, name)
				source = rosterDir + "/" + name
			default:
				loaded = fleet.DefaultProfile()
				source = "builtin"
			}
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: fleet profile: %v\n", err)
				setCode(1)
				return
			}
			payload := map[string]any{
				"source": source, "name": loaded.Name, "max_workers": loaded.MaxWorkers,
				"lease_timeout": loaded.LeaseTimeout, "heartbeat_alert": loaded.HeartbeatAlert,
				"roles": loaded.Roles,
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(payload)
			} else {
				_, _ = fmt.Fprintf(stdout, "name=%s max_workers=%d lease=%s heartbeat=%s source=%s\n",
					loaded.Name, loaded.MaxWorkers, loaded.LeaseTimeout, loaded.HeartbeatAlert, source)
			}
			setCode(0)
		},
	}
	profile.Flags().String("file", "", "path to fleet profile TOML")
	profile.Flags().String("roster", "", "directory of named *.toml profiles")
	profile.Flags().String("name", "default", "roster profile name")
	profile.Flags().Bool("json", false, "emit JSON")

	cmd.AddCommand(list, status, inspect, logs, profile)
	return cmd
}
