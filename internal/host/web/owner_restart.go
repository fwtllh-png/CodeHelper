package web

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fwtllh-png/QCode/internal/buildinfo"
	"github.com/fwtllh-png/QCode/internal/platform/ownerlease"
)

const ownerReplacementPollInterval = 25 * time.Millisecond

func webOwnerKind(development bool) string {
	if development {
		return "web-dev"
	}
	return "web"
}

func webOwnerBuild(info buildinfo.Info) string {
	return info.Version + "+" + info.Commit + "@" + info.BuildDate
}

func signalWebOwner(pid int) error {
	if pid <= 0 {
		return errors.New("owner lease does not contain a valid process ID")
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find owner process %d: %w", pid, err)
	}
	if err := process.Signal(os.Interrupt); err != nil {
		return fmt.Errorf("interrupt owner process %d: %w", pid, err)
	}
	return nil
}

func replaceWebOwner(
	ctx context.Context,
	path string,
	metadata ownerlease.Metadata,
	held ownerlease.Metadata,
	interrupt func(int) error,
) (*ownerlease.Lease, error) {
	if held.OwnerKind != "web-dev" &&
		!(held.OwnerKind == "web" && strings.HasPrefix(held.Build, "dev+")) {
		return nil, fmt.Errorf("owner kind %q is not replaceable", held.OwnerKind)
	}
	if err := interrupt(held.PID); err != nil {
		return nil, err
	}

	ticker := time.NewTicker(ownerReplacementPollInterval)
	defer ticker.Stop()
	for {
		lease, err := ownerlease.Acquire(path, metadata)
		if err == nil {
			return lease, nil
		}
		var current *ownerlease.HeldError
		if !errors.As(err, &current) {
			return nil, err
		}
		if current.Metadata.PID != held.PID {
			return nil, fmt.Errorf(
				"Web owner changed from process %d to %d during restart",
				held.PID,
				current.Metadata.PID,
			)
		}
		select {
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		case <-ticker.C:
		}
	}
}
