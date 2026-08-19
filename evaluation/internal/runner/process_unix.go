//go:build !windows

package runner

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const processGroupCleanupTimeout = 2 * time.Second

func configureProcessTree(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func attachProcessTree(command *exec.Cmd) (func() error, func() error, error) {
	processGroup := -command.Process.Pid
	kill := func() error {
		groupErr := syscall.Kill(processGroup, syscall.SIGKILL)
		processErr := command.Process.Kill()
		if errors.Is(groupErr, syscall.ESRCH) {
			groupErr = nil
		}
		if errors.Is(processErr, os.ErrProcessDone) {
			processErr = nil
		}
		return errors.Join(groupErr, processErr)
	}
	closeTree := func() error {
		deadline := time.Now().Add(processGroupCleanupTimeout)
		for {
			err := syscall.Kill(processGroup, syscall.SIGKILL)
			if errors.Is(err, syscall.ESRCH) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("kill process group: %w", err)
			}
			if time.Now().After(deadline) {
				return errors.New("process group cleanup timed out")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	return kill, closeTree, nil
}
