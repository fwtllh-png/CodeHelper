package extension

import (
	"context"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

type ThreadInput struct {
	ThreadID string
}

type TurnInput struct {
	ThreadID string
	TurnID   string
}

type ContextInput struct {
	ThreadID string
	TurnID   string
}

type ContextItem struct {
	ID     string
	Source string
	Body   string
	Digest string
}

type ToolInput struct{}

type ToolContribution struct {
	Registrations []tool.Registration
}

type MCPInput struct {
	ThreadID string
}

type MCPContribution struct {
	ID           string
	Source       string
	ConfigDigest string
}

type ThreadLifecycleContributor interface {
	OnThreadStart(context.Context, ThreadInput) Outcome
	OnThreadResume(context.Context, ThreadInput) Outcome
	OnThreadStop(context.Context, ThreadInput) Outcome
}

type TurnLifecycleContributor interface {
	OnTurnStart(context.Context, TurnInput) Outcome
	OnTurnStop(context.Context, TurnInput) Outcome
	OnTurnAbort(context.Context, TurnInput) Outcome
}

type ContextContributor interface {
	ContributeContext(context.Context, ContextInput) ([]ContextItem, Outcome)
}

type ToolContributor interface {
	ContributeTools(context.Context, ToolInput) (ToolContribution, Outcome)
}

type MCPContributor interface {
	ContributeMCP(context.Context, MCPInput) ([]MCPContribution, Outcome)
}
