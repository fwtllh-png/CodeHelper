//go:build !windows

package mcp

import (
	"os"
	"os/exec"
	"syscall"
)

func configureMCPProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func gracefulMCPProcessGroup(process *os.Process) error {
	if err := syscall.Kill(-process.Pid, syscall.SIGTERM); err == nil {
		return nil
	}
	return process.Signal(syscall.SIGTERM)
}

func killMCPProcessGroup(process *os.Process) error {
	_ = syscall.Kill(-process.Pid, syscall.SIGKILL)
	return process.Kill()
}
