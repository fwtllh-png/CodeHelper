package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/lane"
	"github.com/spf13/cobra"
)

func newLaneCommand(ctx context.Context, stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	cmd := &cobra.Command{Use: "lane", Short: "Manage inline/tmux worker lanes"}
	cmd.Run = func(c *cobra.Command, args []string) {
		_, _ = fmt.Fprintln(stderr, "codehelper: lane requires a subcommand (start|list|status|stop|log|attach)")
		setCode(2)
	}

	requireRoot := func(dataDir string) (*lane.Registry, bool) {
		if dataDir == "" {
			_, _ = fmt.Fprintln(stderr, "codehelper: lane requires --data-dir")
			setCode(2)
			return nil, false
		}
		registry, err := lane.Open(dataDir)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: lane: %v\n", err)
			setCode(1)
			return nil, false
		}
		return registry, true
	}

	start := &cobra.Command{
		Use: "start --data-dir DIR --id ID -- COMMAND...", Short: "Start a lane process",
		DisableFlagParsing: false,
		Run: func(cmd *cobra.Command, args []string) {
			dataDir, _ := cmd.Flags().GetString("data-dir")
			id, _ := cmd.Flags().GetString("id")
			backend, _ := cmd.Flags().GetString("backend")
			worktree, _ := cmd.Flags().GetBool("worktree")
			asJSON, _ := cmd.Flags().GetBool("json")
			if id == "" || len(args) == 0 {
				_, _ = fmt.Fprintln(stderr, "codehelper: lane start requires --id and a command")
				setCode(2)
				return
			}
			registry, ok := requireRoot(dataDir)
			if !ok {
				return
			}
			record, err := registry.Start(ctx, id, lane.StartSpec{
				Command: args, Backend: lane.Backend(backend), Worktree: worktree,
			})
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: lane start: %v\n", err)
				setCode(1)
				return
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(record)
			} else {
				_, _ = fmt.Fprintf(stdout, "id=%s status=%s backend=%s\n", record.ID, record.Status, record.Backend)
			}
			setCode(0)
		},
	}
	start.Flags().String("data-dir", "", "lane store root")
	start.Flags().String("id", "", "lane id")
	start.Flags().String("backend", string(lane.BackendInline), "inline or tmux")
	start.Flags().Bool("worktree", false, "bind an isolated worktree under data-dir")
	start.Flags().Bool("json", false, "emit JSON")

	list := &cobra.Command{
		Use: "list", Short: "List durable lane records",
		Run: func(cmd *cobra.Command, args []string) {
			dataDir, _ := cmd.Flags().GetString("data-dir")
			asJSON, _ := cmd.Flags().GetBool("json")
			registry, ok := requireRoot(dataDir)
			if !ok {
				return
			}
			records := registry.List()
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{"lanes": records, "data_dir": dataDir})
			} else {
				for _, record := range records {
					_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n", record.ID, record.Status, record.Backend)
				}
			}
			setCode(0)
		},
	}
	list.Flags().String("data-dir", "", "lane store root")
	list.Flags().Bool("json", false, "emit JSON")

	status := &cobra.Command{
		Use: "status", Short: "Show one lane record",
		Run: func(cmd *cobra.Command, args []string) {
			dataDir, _ := cmd.Flags().GetString("data-dir")
			id, _ := cmd.Flags().GetString("id")
			asJSON, _ := cmd.Flags().GetBool("json")
			if id == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: lane status requires --id")
				setCode(2)
				return
			}
			registry, ok := requireRoot(dataDir)
			if !ok {
				return
			}
			record, err := registry.Status(id)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: lane status: %v\n", err)
				setCode(1)
				return
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(record)
			} else {
				_, _ = fmt.Fprintf(stdout, "id=%s status=%s backend=%s\n", record.ID, record.Status, record.Backend)
			}
			setCode(0)
		},
	}
	status.Flags().String("data-dir", "", "lane store root")
	status.Flags().String("id", "", "lane id")
	status.Flags().Bool("json", false, "emit JSON")

	stop := &cobra.Command{
		Use: "stop", Short: "Stop a running lane",
		Run: func(cmd *cobra.Command, args []string) {
			dataDir, _ := cmd.Flags().GetString("data-dir")
			id, _ := cmd.Flags().GetString("id")
			asJSON, _ := cmd.Flags().GetBool("json")
			if id == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: lane stop requires --id")
				setCode(2)
				return
			}
			registry, ok := requireRoot(dataDir)
			if !ok {
				return
			}
			record, err := registry.Stop(id)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: lane stop: %v\n", err)
				setCode(1)
				return
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(record)
			} else {
				_, _ = fmt.Fprintf(stdout, "id=%s status=%s\n", record.ID, record.Status)
			}
			setCode(0)
		},
	}
	stop.Flags().String("data-dir", "", "lane store root")
	stop.Flags().String("id", "", "lane id")
	stop.Flags().Bool("json", false, "emit JSON")

	logCmd := &cobra.Command{
		Use: "log", Short: "Print recent lane log lines",
		Run: func(cmd *cobra.Command, args []string) {
			dataDir, _ := cmd.Flags().GetString("data-dir")
			id, _ := cmd.Flags().GetString("id")
			limit, _ := cmd.Flags().GetInt("limit")
			if id == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: lane log requires --id")
				setCode(2)
				return
			}
			registry, ok := requireRoot(dataDir)
			if !ok {
				return
			}
			lines, err := registry.Log(id, limit)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: lane log: %v\n", err)
				setCode(1)
				return
			}
			for _, line := range lines {
				_, _ = fmt.Fprintln(stdout, string(line))
			}
			setCode(0)
		},
	}
	logCmd.Flags().String("data-dir", "", "lane store root")
	logCmd.Flags().String("id", "", "lane id")
	logCmd.Flags().Int("limit", 50, "max log lines")

	attach := &cobra.Command{
		Use: "attach", Short: "Print tmux attach command for a lane (fail-closed without tmux)",
		Run: func(cmd *cobra.Command, args []string) {
			dataDir, _ := cmd.Flags().GetString("data-dir")
			id, _ := cmd.Flags().GetString("id")
			asJSON, _ := cmd.Flags().GetBool("json")
			if id == "" {
				_, _ = fmt.Fprintln(stderr, "codehelper: lane attach requires --id")
				setCode(2)
				return
			}
			registry, ok := requireRoot(dataDir)
			if !ok {
				return
			}
			attachCmd, err := registry.Attach(id)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: lane attach: %v\n", err)
				_, _ = fmt.Fprintln(stderr, "hint: use `lane log --id` when tmux is unavailable")
				setCode(1)
				return
			}
			if asJSON {
				_ = json.NewEncoder(stdout).Encode(map[string]any{
					"id": id, "attach_command": attachCmd,
				})
			} else {
				_, _ = fmt.Fprintln(stdout, attachCmd)
			}
			setCode(0)
		},
	}
	attach.Flags().String("data-dir", "", "lane store root")
	attach.Flags().String("id", "", "lane id")
	attach.Flags().Bool("json", false, "emit JSON")

	cmd.AddCommand(start, list, status, stop, logCmd, attach)
	return cmd
}
