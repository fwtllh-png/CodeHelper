//go:build windows

package mcp

import (
	"os"
	"os/exec"
)

func configureMCPProcessGroup(_ *exec.Cmd) {}

func gracefulMCPProcessGroup(process *os.Process) error {
	return process.Signal(os.Interrupt)
}

func killMCPProcessGroup(process *os.Process) error {
	return process.Kill()
}
