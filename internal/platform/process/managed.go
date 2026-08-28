package process

import (
	"context"
	"errors"
	"os/exec"
	"sync"
)

type Managed struct {
	command *exec.Cmd
	stdout  *observedBuffer
	stderr  *observedBuffer
	done    chan struct{}

	mu     sync.Mutex
	result Result
	err    error
}

func StartManaged(ctx context.Context, options Options) (*Managed, error) {
	if options.PTY {
		return nil, errors.New("managed process does not support PTY")
	}
	if options.OutputLimitBytes < 0 {
		return nil, errors.New("process output limit must not be negative")
	}
	command, err := NewCommand(ctx, options)
	if err != nil {
		return nil, err
	}
	limit := options.OutputLimitBytes
	if limit == 0 {
		limit = DefaultOutputLimitBytes
	}
	stdout := newObservedBuffer(StreamStdout, limit, options.OnOutput, nil)
	stderr := newObservedBuffer(StreamStderr, limit, options.OnOutput, nil)
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Start(); err != nil {
		return nil, err
	}
	managed := &Managed{
		command: command, stdout: stdout, stderr: stderr,
		done: make(chan struct{}),
	}
	go managed.wait()
	return managed, nil
}

func (p *Managed) PID() int {
	if p == nil || p.command == nil || p.command.Process == nil {
		return 0
	}
	return p.command.Process.Pid
}

func (p *Managed) Cancel() error {
	if p == nil || p.command == nil || p.command.Cancel == nil {
		return errors.New("managed process is unavailable")
	}
	return p.command.Cancel()
}

func (p *Managed) Wait(ctx context.Context) (Result, error) {
	if p == nil {
		return Result{}, errors.New("managed process is required")
	}
	select {
	case <-p.done:
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.result, p.err
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

func (p *Managed) wait() {
	err := p.command.Wait()
	p.mu.Lock()
	p.result = Result{
		Stdout: p.stdout.String(), Stderr: p.stderr.String(),
		ExitCode: ExitCode(err),
		OutputReceipt: OutputReceipt{
			Stdout: p.stdout.Receipt(), Stderr: p.stderr.Receipt(),
		},
	}
	p.err = err
	p.mu.Unlock()
	close(p.done)
}
