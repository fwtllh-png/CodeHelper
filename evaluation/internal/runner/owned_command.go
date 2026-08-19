package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

type OwnedCommandResult struct {
	ExitCode     int
	TimedOut     bool
	StdoutBytes  int64
	StderrBytes  int64
	StdoutDigest string
	StderrDigest string
	Truncated    bool
}

func RunOwnedCommand(
	ctx context.Context,
	directory string,
	arguments []string,
	environment []string,
	maxOutputBytes int64,
) (OwnedCommandResult, error) {
	if len(arguments) == 0 {
		return OwnedCommandResult{}, errors.New("owned command is empty")
	}
	if maxOutputBytes < 1 {
		return OwnedCommandResult{}, errors.New("owned command output budget is invalid")
	}
	command := exec.Command(arguments[0], arguments[1:]...)
	command.Dir = directory
	if environment == nil {
		command.Env = os.Environ()
	} else {
		command.Env = append(os.Environ(), environment...)
	}
	limit := &sharedOutputLimit{remaining: maxOutputBytes}
	stdout := &boundedBuffer{limit: limit}
	stderr := &boundedBuffer{limit: limit}
	command.Stdout = stdout
	command.Stderr = stderr
	err := runProcess(ctx, command)
	result := OwnedCommandResult{
		StdoutBytes: stdout.BytesSeen(), StderrBytes: stderr.BytesSeen(),
		StdoutDigest: stdout.Digest(), StderrDigest: stderr.Digest(),
		Truncated: limit.Truncated(),
	}
	if err == nil {
		return result, nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.TimedOut = true
		return result, ctx.Err()
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		result.ExitCode = exitError.ExitCode()
	}
	return result, err
}
