//go:build windows

package process

import (
	"os"
	"os/exec"
)

func configureProcessGroup(_ *exec.Cmd, _ bool) {}

func terminateProcessGroup(process *os.Process) error {
	return process.Kill()
}

func signalProcessGroup(process *os.Process, signal os.Signal) error {
	return process.Signal(signal)
}
