//go:build !windows

package hooks

import "os/exec"

func attachProcessTree(command *exec.Cmd) (func() error, func(), error) {
	return command.Cancel, func() {}, nil
}
