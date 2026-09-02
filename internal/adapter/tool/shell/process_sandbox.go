package shell

import (
	"context"

	"github.com/fwtllh-png/QCode/internal/adapter/tool/guard"
	"github.com/fwtllh-png/QCode/internal/security/sandbox"
)

// processSandbox selects backend strength from Guard's SandboxAttempt.
func processSandbox(ctx context.Context, backend sandbox.Backend) (sandbox.Backend, bool) {
	return guard.ProcessSandbox(ctx, backend)
}
