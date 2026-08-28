package process

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
)

type StreamManaged struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	stderr  io.ReadCloser
	done    chan struct{}

	mu  sync.Mutex
	err error
}

func StartStreamManaged(
	ctx context.Context,
	options Options,
) (*StreamManaged, error) {
	if options.PTY || options.Stdin != nil {
		return nil, errors.New("stream process requires broker-owned pipes")
	}
	command, err := NewCommand(ctx, options)
	if err != nil {
		return nil, err
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, err
	}
	managed := &StreamManaged{
		command: command, stdin: stdin, stdout: stdout, stderr: stderr,
		done: make(chan struct{}),
	}
	go managed.wait()
	return managed, nil
}

func (p *StreamManaged) PID() int {
	if p == nil || p.command == nil || p.command.Process == nil {
		return 0
	}
	return p.command.Process.Pid
}

func (p *StreamManaged) Stdin() io.WriteCloser { return p.stdin }
func (p *StreamManaged) Stdout() io.ReadCloser { return p.stdout }
func (p *StreamManaged) Stderr() io.ReadCloser { return p.stderr }

func (p *StreamManaged) Signal(signal os.Signal) error {
	if p == nil || p.command == nil || p.command.Process == nil {
		return errors.New("stream process is unavailable")
	}
	return signalProcessGroup(p.command.Process, signal)
}

func (p *StreamManaged) Cancel() error {
	if p == nil || p.command == nil || p.command.Cancel == nil {
		return errors.New("stream process is unavailable")
	}
	return p.command.Cancel()
}

func (p *StreamManaged) Wait(ctx context.Context) error {
	if p == nil {
		return errors.New("stream process is required")
	}
	select {
	case <-p.done:
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *StreamManaged) wait() {
	err := p.command.Wait()
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
	close(p.done)
}
