package httpclient

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerwire "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/wire"
	"github.com/fwtllh-png/CodeHelper/internal/observability/tracecontext"
)

type sessionAttempt struct {
	client      *Client
	call        providerwire.PreparedCall
	credential  string
	cancel      context.CancelFunc
	traceHeader http.Header
	transferred bool
}

func (c *Client) BeginSession(
	ctx context.Context,
	route model.ReadyRoute,
	call providerwire.PreparedCall,
) (providerwire.SessionAttempt, error) {
	_, cancel, credential, err := c.begin(ctx, route)
	if err != nil {
		return nil, err
	}
	traceHeader := make(http.Header)
	tracecontext.InjectHTTP(ctx, traceHeader)
	return &sessionAttempt{
		client: c, call: call, credential: credential, cancel: cancel,
		traceHeader: traceHeader,
	}, nil
}
func (a *sessionAttempt) Dial(endpoint string) (providerwire.Socket, context.CancelFunc, error) {
	headers := a.call.Headers.Clone()
	for name, values := range a.traceHeader {
		headers[name] = append([]string(nil), values...)
	}
	applyAuth(headers, a.call.Auth, a.credential)
	dialContext, cancel := context.WithCancel(context.Background())
	conn, _, err := websocket.Dial(dialContext, endpoint, &websocket.DialOptions{
		HTTPClient: a.client.httpClient(), HTTPHeader: headers,
	})
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return socket{conn: conn}, cancel, nil
}
func (a *sessionAttempt) ProviderRequest()           {}
func (a *sessionAttempt) IdleTimeout() time.Duration { return a.client.IdleTimeout }
func (a *sessionAttempt) Wrap(stream provider.Stream, metadata provider.TransportMetadata) provider.Stream {
	a.transferred = true
	return a.client.wrapStream(stream, metadata, a.cancel)
}
func (a *sessionAttempt) Close() {
	if !a.transferred {
		a.cancel()
		a.client.release()
	}
}

type socket struct{ conn *websocket.Conn }

func (s socket) Read(ctx context.Context) ([]byte, error) {
	for {
		kind, data, err := s.conn.Read(ctx)
		if err != nil {
			return nil, err
		}
		if kind == websocket.MessageText {
			return data, nil
		}
	}
}
func (s socket) Write(ctx context.Context, data []byte) error {
	return s.conn.Write(ctx, websocket.MessageText, data)
}
func (s socket) Close() error { return s.conn.Close(websocket.StatusNormalClosure, "") }
func applyHeaders(request *http.Request, call providerwire.PreparedCall, credential string) {
	request.Header = call.Headers.Clone()
	applyAuth(request.Header, call.Auth, credential)
}
func applyAuth(header http.Header, style providerwire.AuthStyle, credential string) {
	if credential == "" {
		return
	}
	switch style {
	case providerwire.AuthBearer:
		header.Set("Authorization", "Bearer "+credential)
	case providerwire.AuthAnthropicKey:
		header.Set("x-api-key", credential)
	}
}
func (c *Client) wrapStream(
	stream provider.Stream,
	metadata provider.TransportMetadata,
	cancel context.CancelFunc,
) provider.Stream {
	return &managedStream{
		stream: &metadataStream{Stream: stream, metadata: metadata},
		cancel: cancel, release: c.release, idleTimeout: c.IdleTimeout,
		success: c.recordSuccess, failure: c.recordFailure,
	}
}

var _ providerwire.SessionTransport = (*Client)(nil)
