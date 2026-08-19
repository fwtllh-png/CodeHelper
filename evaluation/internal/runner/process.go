package runner

import (
	"context"
	"errors"
	"os/exec"
)

func runProcess(ctx context.Context, command *exec.Cmd) error {
	configureProcessTree(command)
	if err := command.Start(); err != nil {
		return err
	}
	kill, closeTree, err := attachProcessTree(command)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return err
	}
	settled := make(chan error, 1)
	go func() {
		settled <- command.Wait()
	}()
	select {
	case err := <-settled:
		return errors.Join(err, closeTree())
	case <-ctx.Done():
		_ = kill()
		<-settled
		return errors.Join(ctx.Err(), closeTree())
	}
}
