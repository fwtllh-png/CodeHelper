package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/automation"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/spf13/cobra"
)

func newAutomationCommand(stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	cmd := &cobra.Command{Use: "automation", Short: "Manage recurring automations"}
	cmd.Run = func(c *cobra.Command, args []string) {
		_, _ = fmt.Fprintln(stderr, "codehelper: automation requires a subcommand (list|run|pause)")
		setCode(2)
	}

	openRepo := func(dataDir string) (*automation.Repository, *state.Store, bool) {
		if dataDir == "" {
			_, _ = fmt.Fprintln(stderr, "codehelper: automation requires --data-dir")
			setCode(2)
			return nil, nil, false
		}
		store, err := state.Open(context.Background(), state.Options{DataDir: dataDir})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: automation: %v\n", err)
			setCode(1)
			return nil, nil, false
		}
		return automation.NewSQLiteRepository(store.SQLite()), store, true
	}

	list := &cobra.Command{
		Use: "list", Short: "List automations under a data directory",
		Run: func(cmd *cobra.Command, args []string) {
			dataDir, _ := cmd.Flags().GetString("data-dir")
			asJSON, _ := cmd.Flags().GetBool("json")
			repo, store, ok := openRepo(dataDir)
			if !ok {
				return
			}
			defer func() { _ = store.Close(context.Background()) }()
			values, err := repo.List(context.Background(), automation.Filter{})
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: automation list: %v\n", err)
				setCode(1)
				return
			}
			rows := make([]map[string]any, 0, len(values))
			for _, value := range values {
				row := map[string]any{
					"id": value.ID, "name": value.Name, "status": string(value.Status),
					"version": value.Version, "rrule": value.RRULE,
				}
				if value.NextRunAt != nil {
					row["next_run_at"] = value.NextRunAt.UTC().Format(time.RFC3339Nano)
				}
				rows = append(rows, row)
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{
					"automations": rows, "data_dir": dataDir,
				})
			} else {
				for _, row := range rows {
					_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n", row["id"], row["status"], row["name"])
				}
			}
			setCode(0)
		},
	}
	list.Flags().String("data-dir", "", "persistent state directory")
	list.Flags().Bool("json", false, "emit JSON")

	runCmd := &cobra.Command{
		Use: "run", Short: "Manually enqueue a durable task for an automation",
		Run: func(cmd *cobra.Command, args []string) {
			dataDir, _ := cmd.Flags().GetString("data-dir")
			id, _ := cmd.Flags().GetString("id")
			asJSON, _ := cmd.Flags().GetBool("json")
			if id == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: automation run requires --data-dir and --id")
				setCode(2)
				return
			}
			repo, store, ok := openRepo(dataDir)
			if !ok {
				return
			}
			defer func() { _ = store.Close(context.Background()) }()
			current, err := repo.Get(context.Background(), id)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: automation run: %v\n", err)
				setCode(1)
				return
			}
			run, err := repo.RunNow(context.Background(), id, current.Version, time.Time{})
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: automation run: %v\n", err)
				setCode(1)
				return
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{
					"run_id": run.ID, "task_id": run.TaskID, "automation_id": run.AutomationID,
				})
			} else {
				_, _ = fmt.Fprintf(stdout, "task_id=%s\n", run.TaskID)
			}
			setCode(0)
		},
	}
	runCmd.Flags().String("data-dir", "", "persistent state directory")
	runCmd.Flags().String("id", "", "automation id")
	runCmd.Flags().Bool("json", false, "emit JSON")

	pause := &cobra.Command{
		Use: "pause", Short: "Pause an active automation",
		Run: func(cmd *cobra.Command, args []string) {
			dataDir, _ := cmd.Flags().GetString("data-dir")
			id, _ := cmd.Flags().GetString("id")
			asJSON, _ := cmd.Flags().GetBool("json")
			if id == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: automation pause requires --data-dir and --id")
				setCode(2)
				return
			}
			repo, store, ok := openRepo(dataDir)
			if !ok {
				return
			}
			defer func() { _ = store.Close(context.Background()) }()
			current, err := repo.Get(context.Background(), id)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: automation pause: %v\n", err)
				setCode(1)
				return
			}
			updated, err := repo.Pause(context.Background(), id, current.Version, time.Time{})
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: automation pause: %v\n", err)
				setCode(1)
				return
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{
					"id": updated.ID, "status": string(updated.Status), "version": updated.Version,
				})
			} else {
				_, _ = fmt.Fprintf(stdout, "status=%s\n", updated.Status)
			}
			setCode(0)
		},
	}
	pause.Flags().String("data-dir", "", "persistent state directory")
	pause.Flags().String("id", "", "automation id")
	pause.Flags().Bool("json", false, "emit JSON")

	cmd.AddCommand(list, runCmd, pause)
	return cmd
}
