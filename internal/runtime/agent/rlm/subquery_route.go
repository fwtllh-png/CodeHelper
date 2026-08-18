package rlm

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

// StreamProvider is the minimal surface RouteSubQuery needs from the HTTP client.
type StreamProvider interface {
	Stream(context.Context, provider.ModelRequest) (provider.Stream, error)
}

// RouteSubQuery answers sub_query calls with a one-shot chat completion on ReadyRoute.
type RouteSubQuery struct {
	Provider StreamProvider
	Route    model.ReadyRoute
	// Unavailable is why there is no route, when there is none. A locked route
	// set without a subquery slot is the case worth explaining: reporting it as
	// an absent feature would send the model looking for another way to do
	// something the operator deliberately switched off.
	Unavailable error
}

func (r RouteSubQuery) Query(ctx context.Context, prompt, slice string) (string, error) {
	if r.Unavailable != nil {
		return "", fmt.Errorf("sub_query route unavailable: %w", r.Unavailable)
	}
	if r.Provider == nil {
		return "", fmt.Errorf("%s", formatSubQueryUnavailable())
	}
	if err := r.Route.Validate(); err != nil {
		return "", fmt.Errorf("sub_query route unavailable: %w", err)
	}
	user := strings.TrimSpace(prompt)
	if slice != "" {
		if user != "" {
			user += "\n\n"
		}
		user += slice
	}
	if user == "" {
		return "", fmt.Errorf("sub_query prompt or slice is required")
	}
	maxOut := r.Route.Model().Limits.MaxOutputTokens
	stream, err := r.Provider.Stream(ctx, provider.ModelRequest{
		Route: r.Route,
		Messages: []provider.Message{
			provider.TextMessage(provider.RoleUser, user),
		},
		MaxOutputTokens: maxOut,
		// A sub-query is one self-contained question with no side effects, so a
		// retry of the same body is safe and gets the same idempotency key the
		// engine's own samples get. It was left off here only by omission, which
		// made the safest call in the runtime the one least protected from a 429.
		Idempotent: true,
	})
	if err != nil {
		return "", err
	}
	defer stream.Close()
	var text strings.Builder
	for {
		event, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			return "", recvErr
		}
		if event.Type == provider.EventTextDelta {
			text.WriteString(event.Text)
		}
	}
	return text.String(), nil
}
