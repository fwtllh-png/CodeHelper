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
