package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func newCompletionCommand(root *cobra.Command, stdout, stderr io.Writer, setCode func(int)) *cobra.Command {
	return &cobra.Command{
		Use: "completion [bash|zsh|fish|powershell]", Short: "Generate shell completion scripts",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		Run: func(cmd *cobra.Command, args []string) {
			var err error
			switch args[0] {
			case "bash":
				err = root.GenBashCompletion(stdout)
			case "zsh":
				err = root.GenZshCompletion(stdout)
			case "fish":
				err = root.GenFishCompletion(stdout, true)
			case "powershell":
				err = root.GenPowerShellCompletionWithDesc(stdout)
			default:
				_, _ = fmt.Fprintf(stderr, "codehelper: unsupported shell %q\n", args[0])
				setCode(2)
				return
			}
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "codehelper: completion: %v\n", err)
				setCode(1)
				return
			}
			setCode(0)
		},
	}
}
