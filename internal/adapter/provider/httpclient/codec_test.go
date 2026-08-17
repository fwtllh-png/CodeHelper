package httpclient

import (
	"context"
	"fmt"
	"io"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/anthropic"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/openai"
	providerwire "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const responsesReasoningPlaceholder = "(continued)"

func (c *Client) Stream(
	ctx context.Context,
	request provider.ModelRequest,
) (provider.Stream, error) {
	adapter, err := testAdapter(request.Route.Adapter())
	if err != nil {
		return nil, err
	}
	call, err := adapter.Prepare(request)
	if err != nil {
		return nil, protocol.NewProblem(
			protocol.CodeInvalidArgument, err.Error(), false, err,
		)
	}
	if sessionAdapter, ok := adapter.(providerwire.SessionAdapter); ok {
		stream, handled, err := sessionAdapter.TrySession(
			ctx, request, call, c,
		)
		if handled || err != nil {
			return stream, err
		}
	}
	return c.Execute(ctx, request, call, adapter)
}

func encodeRequest(
	request provider.ModelRequest,
) ([]byte, string, error) {
	adapter, err := testAdapter(request.Route.Adapter())
	if err != nil {
		return nil, "", err
	}
	call, err := adapter.Prepare(request)
	return call.Body, call.Path, err
}

func testAdapter(id model.AdapterID) (providerwire.Adapter, error) {
	if id == model.AdapterAnthropic {
		return anthropic.NewAdapter(), nil
	}
	if id == model.AdapterDeepSeek {
		adapter, err := openai.NewAdapter(model.AdapterOpenAI)
		if err != nil {
			return nil, err
		}
		return legacyOpenAIAdapter{
			Adapter: adapter, id: model.AdapterDeepSeek,
		}, nil
	}
	adapter, err := openai.NewAdapter(id)
	if err != nil {
		return nil, fmt.Errorf("test adapter: %w", err)
	}
	return legacyOpenAIAdapter{Adapter: adapter, id: id}, nil
}

type legacyOpenAIAdapter struct {
	*openai.Adapter
	id model.AdapterID
}

func (a legacyOpenAIAdapter) ID() model.AdapterID { return a.id }
func (a legacyOpenAIAdapter) Prepare(
	request provider.ModelRequest,
) (providerwire.PreparedCall, error) {
	var call providerwire.PreparedCall
	var err error
	if request.Route.Protocol() == model.ProtocolOpenAIResponses {
		call, err = openai.PrepareResponses(
			request,
			a.id,
			openai.ResponsesPolicy{
				ReasoningPlaceholder:      responsesReasoningPlaceholder,
				IncludeEncryptedReasoning: true,
				ReplayAdapter:             a.id,
			},
		)
	} else {
		call, err = a.Adapter.Prepare(request)
		call.Adapter = a.id
	}
	return call, err
}
func (a legacyOpenAIAdapter) OpenStream(
	body io.ReadCloser,
	call providerwire.PreparedCall,
) (provider.Stream, error) {
	return openai.NewStream(body, call.Protocol)
}
