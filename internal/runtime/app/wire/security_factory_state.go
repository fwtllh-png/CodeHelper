package wire

import (
	"time"

	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/QCode/internal/adapter/tool/guard"
	"github.com/fwtllh-png/QCode/internal/observability/diagnostics"
	"github.com/fwtllh-png/QCode/internal/persist/workspacejournal"
	"github.com/fwtllh-png/QCode/internal/security/permissions"
	"github.com/fwtllh-png/QCode/internal/security/policy"
)

type guardFactory struct {
	registry               *tool.Registry
	runtime                *policy.Runtime
	workspace, workspaceID string
	journal                *workspacejournal.Manager
	readTracker            *workspacejournal.ReadTracker
	diagnostics            diagnostics.Runner
	permissions            *permissions.Store
	onNetworkAllow         toolguard.NetworkAllow
	leaseAuthority         *toolguard.LeaseAuthority
	forceEditReview        bool
	leaseTTL               time.Duration
	approvalTTL            time.Duration
	now                    func() time.Time
}

func newLeaseAuthority() *toolguard.LeaseAuthority {
	return toolguard.NewLeaseAuthority()
}
