package httpclient

import (
	"context"
	"net/http"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerwire "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/wire"
	"github.com/fwtllh-png/CodeHelper/internal/observability/providerdump"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func (c *Client) openResponse(
	response *http.Response,
	request provider.ModelRequest,
	call providerwire.PreparedCall,
	adapter providerwire.Adapter,
	transportRequestID string,
	cancel context.CancelFunc,
	rateLimitKey string,
	cooldownWait time.Duration,
) (provider.Stream, bool, error) {
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		c.limits.Observe(rateLimitKey, c.RequestsPerSecond, response.StatusCode, response.Header, nil)
		stream, err := adapter.OpenStream(response.Body, call)
		if err != nil {
			_ = response.Body.Close()
			cancel()
			c.recordFailure(err)
			return nil, false, err
		}
		metadata := completeTransportMetadata(request, call, transportRequestID)
		metadata.RouteCooldownWaitMS = uint64(cooldownWait / time.Millisecond)
		return c.wrapStream(
			stream,
			metadata,
			cancel,
		), true, nil
	}
	errorText := boundedBody(response.Body)
	problem := adapter.ClassifyHTTP(providerwire.HTTPFailure{
		Status: response.StatusCode, Header: response.Header, Body: errorText,
	})
	problem = attributeProviderFault(
		problem,
		request,
		transportRequestID,
		protocol.FaultStageResponseHeaders,
	)
	problem = c.limits.Observe(rateLimitKey, c.RequestsPerSecond, response.StatusCode, response.Header, problem)
	if providerdump.Enabled(response.StatusCode) {
		if dumpPath, dumpErr := providerdump.Write(
			request, call.Body, call.Path, response.StatusCode, errorText,
		); dumpErr == nil && dumpPath != "" {
			if typed, ok := problem.(*protocol.Problem); ok {
				typed.Message += " [diagnostic: " + dumpPath + "]"
			}
		}
	}
	c.recordFailure(problem)
	return nil, false, problem
}
