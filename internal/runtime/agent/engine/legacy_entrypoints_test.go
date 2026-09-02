package engine

import (
	"context"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

// These helpers keep focused engine tests concise while production has one
// Turn entry point. New tests should call Execute with a TurnRequest directly.
func (e *Engine) Run(
	ctx context.Context, prompt string, emit func(Event) error,
) (Result, error) {
	return e.Execute(ctx, TurnRequest{Prompt: prompt}, emit)
}

func (e *Engine) RunForTurn(
	ctx context.Context, turnID, prompt string, emit func(Event) error,
) (Result, error) {
	return e.Execute(ctx, TurnRequest{TurnID: turnID, Prompt: prompt}, emit)
}

func (e *Engine) RunForTurnWithAttachments(
	ctx context.Context,
	turnID, prompt string,
	attachments []provider.Attachment,
	emit func(Event) error,
) (Result, error) {
	return e.Execute(ctx, TurnRequest{
		TurnID: turnID, Prompt: prompt, Intent: protocol.TurnIntentAnswer,
		Attachments: attachments,
	}, emit)
}

func (e *Engine) RunForTurnWithIntentAndAttachments(
	ctx context.Context,
	turnID, prompt string,
	intent protocol.TurnIntent,
	attachments []provider.Attachment,
	emit func(Event) error,
) (Result, error) {
	return e.Execute(ctx, TurnRequest{
		TurnID: turnID, Prompt: prompt, Intent: intent, Attachments: attachments,
	}, emit)
}

func (e *Engine) RunForTurnWithRequest(
	ctx context.Context,
	turnID string,
	request TurnRequest,
	emit func(Event) error,
) (Result, error) {
	request.TurnID = turnID
	return e.Execute(ctx, request, emit)
}
