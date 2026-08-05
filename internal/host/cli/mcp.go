package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func runMCPServe(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) int {
	flags := flag.NewFlagSet("mcp serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	workspace := flags.String("workspace", ".", "workspace used by exposed tools")
	allowed := flags.String("allow", "", "comma-separated allowed model-visible tools")
	mode := flags.String("mode", "act", "tool mode: plan, act, or operate")
	posture := flags.String("posture", "auto", "tool permission posture")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "codehelper: mcp serve accepts no positional arguments")
		return 2
	}
	if !oneOf(*mode, "plan", "act", "operate") ||
		!oneOf(*posture, "suggest", "auto", "bypass", "never") {
		_, _ = fmt.Fprintln(stderr, "codehelper: mcp serve has invalid mode or posture")
		return 2
	}
	var tools []string
	for _, name := range strings.Split(*allowed, ",") {
		if name = strings.TrimSpace(name); name != "" {
			tools = append(tools, name)
		}
	}
	if len(tools) == 0 {
		_, _ = fmt.Fprintln(stderr, "codehelper: mcp serve requires --allow")
		return 2
	}
	if err := wire.ServeMCP(ctx, stdin, stdout, wire.MCPServerOptions{
		Workspace:  *workspace,
		Allowed:    tools,
		Mode:       policy.Mode(*mode),
		Permission: policy.Permission(*posture),
	}); err != nil {
		_, _ = fmt.Fprintf(stderr, "codehelper: mcp serve: %v\n", err)
		return 1
	}
	return 0
}
