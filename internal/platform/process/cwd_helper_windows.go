//go:build windows

package process

import (
	"context"
	"errors"
	"os"
	"os/exec"

	"github.com/fwtllh-png/QCode/internal/security/sandbox"
)

func commandForSpec(
	ctx context.Context,
	spec sandbox.Command,
	directory *os.File,
) (*exec.Cmd, error) {
	if directory != nil {
		return nil, errors.New("descriptor-relative child cwd is unavailable on Windows")
	}
	command := exec.CommandContext(ctx, spec.Path, spec.Args[1:]...)
	command.Dir = spec.Dir
	command.Env = spec.Env
	return command, nil
}
