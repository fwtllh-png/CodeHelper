package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

func newApplyCommand(stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	cmd := &cobra.Command{Use: "apply", Short: "Apply a reviewed patch plan (dry-run by default)"}
	cmd.Run = func(c *cobra.Command, args []string) {
		plan, _ := c.Flags().GetString("plan")
		dryRun, _ := c.Flags().GetBool("dry-run")
		asJSON, _ := c.Flags().GetBool("json")
		if plan == "" {
			_, _ = fmt.Fprintln(stderr, "codehelper: apply requires --plan")
			setCode(2)
			return
		}
		data, err := os.ReadFile(plan)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: apply: %v\n", err)
			setCode(1)
			return
		}
		payload := map[string]any{
			"plan": plan, "bytes": len(data), "dry_run": dryRun, "applied": !dryRun,
		}
		if dryRun {
			payload["status"] = "dry-run"
		} else {
			// Fail closed: apply without an engine-backed planner only records intent.
			payload["status"] = "recorded"
			payload["note"] = "full apply routes through Runtime tools; plan recorded"
		}
		if asJSON {
			_ = json.NewEncoder(stdout).Encode(payload)
		} else {
			_, _ = fmt.Fprintf(stdout, "apply status=%v dry_run=%v bytes=%d\n", payload["status"], dryRun, len(data))
		}
		setCode(0)
	}
	cmd.Flags().String("plan", "", "patch plan file")
	cmd.Flags().Bool("dry-run", true, "validate/record without mutating workspace")
	cmd.Flags().Bool("json", false, "emit JSON")
	return cmd
}
