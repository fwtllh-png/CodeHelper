package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/fwtllh-png/CodeHelper/internal/host/review"
)

func newReviewCommand(stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review [uncommitted|base <ref>|commit <sha>|custom <text>]",
		Short: "Build a reproducible code-review prompt from git scope",
	}
	cmd.Run = func(c *cobra.Command, args []string) {
		workspace, _ := c.Flags().GetString("workspace")
		asJSON, _ := c.Flags().GetBool("json")
		start, _ := c.Flags().GetBool("start")
		if workspace == "" {
			workspace = "."
		}
		target := review.ParseArgs(args)
		prompt, err := review.BuildPrompt(workspace, target)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: review: %v\n", err)
			setCode(1)
			return
		}
		if asJSON {
			_ = json.NewEncoder(stdout).Encode(map[string]any{
				"workspace": workspace,
				"target":    target,
				"prompt":    prompt,
				"action":    "review",
				"start":     start,
			})
		} else {
			_, _ = fmt.Fprintln(stdout, prompt)
			if start {
				_, _ = fmt.Fprintln(stderr, "codehelper: review: --start is reserved; paste prompt into a turn or use TUI /review")
			}
		}
		setCode(0)
	}
	cmd.Flags().String("workspace", ".", "workspace root")
	cmd.Flags().Bool("json", false, "emit JSON")
	cmd.Flags().Bool("start", false, "hint to start a review turn (TUI /review does this)")
	return cmd
}
