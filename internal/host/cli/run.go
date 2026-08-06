package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/buildinfo"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const usage = `codehelper - terminal-first AI coding agent

Usage:
  codehelper --error-format=json COMMAND [ARGS]
  codehelper help
  codehelper version [--json]
  codehelper config check|show|reload [flags]
  codehelper auth status|login|logout|list|suggestions|set|clear [flags]
  codehelper model list|resolve [--json]
  codehelper thread list|resume|fork|archive|rename|read --data-dir DIR [flags]
  codehelper fleet list|status|inspect|logs|profile --data-dir DIR [flags]
  codehelper automation list|run|pause --data-dir DIR [flags]
  codehelper workflow validate|run|status --spec PATH [--id RUN] [--data-dir DIR] [--driver runtime|fake] [--provider-fixture DIR] [--json]
  codehelper lane start|list|status|stop|log|attach --data-dir DIR [flags]
  codehelper doctor [--json]
  codehelper completion bash|zsh|fish|powershell
  codehelper tui [--config PATH]
  codehelper plugin list|trust|enable|disable|revoke|install|update|rollback|security-revoke [flags] [NAME[@VERSION]]
  codehelper skill list|enable|disable|revoke|lint|lock|verify [flags] [NAME]
  codehelper exec [flags] PROMPT
  codehelper worker run|enqueue|list --data-dir DIR [flags]
  codehelper sandbox status|probe|check [--json]
  codehelper init [--workspace DIR] [--config PATH] [--data-dir DIR]
  codehelper setup [--workspace DIR] [--config PATH] [--data-dir DIR] [--json]
  codehelper review [--workspace DIR] [--json]
  codehelper apply --plan PATH [--dry-run] [--json]
  codehelper mcp serve|list|add|test|status|enable|disable|remove|tools|validate [flags]
  codehelper host --adapter acp [flags]
  codehelper runtime-observe [--events N] [--config PATH] [--log-file PATH]
`

func Run(args []string, stdout, stderr io.Writer) int {
	return RunContext(context.Background(), args, strings.NewReader(""), stdout, stderr)
}

func RunContext(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) int {
	machineErrors := len(args) != 0 && args[0] == "--error-format=json"
	if machineErrors {
		args = args[1:]
		var captured bytes.Buffer
		code := runWithCobra(ctx, args, stdin, stdout, &captured)
		if code == 0 {
			_, _ = io.Copy(stderr, &captured)
			return 0
		}
		problem := cliProblem(code, captured.String())
		encoder := json.NewEncoder(stderr)
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(problem)
		return code
	}
	return runWithCobra(ctx, args, stdin, stdout, stderr)
}

func cliProblem(exitCode int, output string) *protocol.Problem {
	code := protocol.CodeInternal
	if exitCode == 2 {
		code = protocol.CodeInvalidArgument
	}
	for _, candidate := range []protocol.ErrorCode{
		protocol.CodeInvalidArgument,
		protocol.CodeConflict,
		protocol.CodeResourceExhausted,
		protocol.CodeUnavailable,
		protocol.CodeCanceled,
		protocol.CodeDeadlineExceeded,
		protocol.CodeInternal,
	} {
		if strings.Contains(output, "("+string(candidate)+")") {
			code = candidate
			break
		}
	}
	message := strings.TrimSpace(output)
	if line, _, found := strings.Cut(message, "\n"); found {
		message = line
	}
	message = strings.TrimPrefix(message, "codehelper: ")
	if message == "" {
		message = "command failed"
	}
	retryable := code == protocol.CodeUnavailable
	return protocol.NewProblem(code, message, retryable, nil)
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	info := buildinfo.Current()

	if len(args) == 0 {
		_, _ = fmt.Fprintf(
			stdout,
			"%s %s (commit %s, built %s, %s, %s/%s)\n",
			info.Name,
			info.Version,
			info.Commit,
			info.BuildDate,
			info.GoVersion,
			info.OS,
			info.Arch,
		)
		return 0
	}

	if len(args) == 1 && args[0] == "--json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(info); err != nil {
			_, _ = fmt.Fprintf(stderr, "codehelper: encode version: %v\n", err)
			return 1
		}
		return 0
	}

	_, _ = fmt.Fprintf(stderr, "codehelper: version accepts only --json\n")
	return 2
}
