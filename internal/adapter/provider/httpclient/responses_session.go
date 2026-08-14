package httpclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/openai"
)

type responsesSession struct {
	mu       sync.Mutex
	conn     *websocket.Conn
	cancel   context.CancelFunc
	endpoint string
	previous string
	property string
	prefix   []json.RawMessage
	lastUsed time.Time
	idle     *time.Timer
}

func (c *Client) responsesSessionStream(
	ctx context.Context,
	request provider.ModelRequest,
	logicalBody []byte,
	credential string,
) (provider.Stream, provider.TransportMetadata, error) {
	key := request.PromptCacheKey + "\x00" + request.Route.ProviderID() + "\x00" + request.Route.Model().ID
	c.sessionMu.Lock()
	if c.sessions == nil {
		c.sessions = make(map[string]*responsesSession)
	}
	session := c.sessions[key]
	if session == nil {
		session = &responsesSession{}
		c.sessions[key] = session
	}
	c.sessionMu.Unlock()

	session.mu.Lock()
	body, input, property, err := responsesSocketBody(logicalBody)
	if err != nil {
		session.mu.Unlock()
		return nil, provider.TransportMetadata{}, err
	}
	now := time.Now()
	if session.conn != nil && c.IdleTimeout > 0 && now.Sub(session.lastUsed) > c.IdleTimeout {
		session.invalidate()
	}
	endpoint, err := websocketEndpoint(request.Route.Endpoint())
	if err != nil {
		session.mu.Unlock()
		return nil, provider.TransportMetadata{}, err
	}
	if session.conn == nil || session.endpoint != endpoint {
		session.invalidate()
		headers := make(http.Header)
		if credential != "" {
			headers.Set("Authorization", "Bearer "+credential)
		}
		dialContext, cancel := context.WithCancel(context.Background())
		conn, _, dialErr := websocket.Dial(dialContext, endpoint, &websocket.DialOptions{
			HTTPClient: c.httpClient(), HTTPHeader: headers,
		})
		if dialErr != nil {
			cancel()
			session.mu.Unlock()
			return nil, provider.TransportMetadata{}, dialErr
		}
		session.conn, session.cancel, session.endpoint = conn, cancel, endpoint
	}
	incremental := session.previous != "" && session.property == property &&
		strictExtension(session.prefix, input)
	if incremental {
		body["previous_response_id"] = session.previous
		body["input"] = input[len(session.prefix):]
	}
	session.previous, session.property, session.prefix = "", "", nil
	payload, err := json.Marshal(body)
	if err != nil {
		session.mu.Unlock()
		return nil, provider.TransportMetadata{}, err
	}
	c.Metrics.ProviderRequest()
	if err := session.conn.Write(ctx, websocket.MessageText, payload); err != nil {
		session.invalidate()
		session.mu.Unlock()
		return nil, provider.TransportMetadata{}, err
	}
	session.lastUsed = now
	return &responsesSocketStream{
		ctx: ctx, session: session, input: input, property: property,
		idleTimeout: c.IdleTimeout,
		decoder:     openai.ResponsesDecoder{CaptureState: true},
	}, transportMetadata(logicalBody, payload, incremental), nil
}

func responsesSocketBody(data []byte) (map[string]any, []json.RawMessage, string, error) {
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, nil, "", err
	}
	delete(body, "stream")
	body["type"] = "response.create"
	rawInput, err := json.Marshal(body["input"])
	if err != nil {
		return nil, nil, "", err
	}
	var input []json.RawMessage
	if err := json.Unmarshal(rawInput, &input); err != nil {
		return nil, nil, "", err
	}
	properties := cloneMap(body)
	delete(properties, "input")
	delete(properties, "type")
	propertyBytes, err := json.Marshal(properties)
	if err != nil {
		return nil, nil, "", err
	}
	return body, input, digest(propertyBytes), nil
}

func cloneMap(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func strictExtension(prefix, current []json.RawMessage) bool {
	if len(current) <= len(prefix) {
		return false
	}
	for index := range prefix {
		if !jsonEqual(prefix[index], current[index]) {
			return false
		}
	}
	return true
}

func jsonEqual(left, right []byte) bool {
	var a, b any
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil &&
		deepEqualJSON(a, b)
}

func deepEqualJSON(left, right any) bool {
	a, _ := json.Marshal(left)
	b, _ := json.Marshal(right)
	return string(a) == string(b)
}

func websocketEndpoint(endpoint string) (string, error) {
	value, err := url.Parse(joinEndpoint(endpoint, "/responses"))
	if err != nil {
		return "", err
	}
	switch value.Scheme {
	case "https":
		value.Scheme = "wss"
	case "http":
		value.Scheme = "ws"
	default:
		return "", errors.New("Responses WebSocket requires an HTTP endpoint")
	}
	return value.String(), nil
}

func transportMetadata(logical, payload []byte, incremental bool) provider.TransportMetadata {
	return provider.TransportMetadata{
		RequestBytes: uint64(len(payload)), LogicalRequestDigest: digest(logical),
		TransportPayloadDigest: digest(payload), Incremental: incremental,
	}
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type responsesSocketStream struct {
	ctx         context.Context
	session     *responsesSession
	decoder     openai.ResponsesDecoder
	queue       []provider.StreamEvent
	started     bool
	stopped     bool
	closed      bool
	input       []json.RawMessage
	property    string
	pending     *provider.ResponseState
	idleTimeout time.Duration
}

func (s *responsesSocketStream) Recv() (provider.StreamEvent, error) {
	if s.stopped || s.closed {
		return provider.StreamEvent{}, io.EOF
	}
	if !s.started {
		s.started = true
		return provider.StreamEvent{Type: provider.EventMessageStart}, nil
	}
	for {
		if len(s.queue) != 0 {
			event := s.queue[0]
			s.queue = s.queue[1:]
			if event.Type == provider.EventResponseState {
				s.pending = event.Response
				continue
			}
			if event.Type == provider.EventMessageStop && s.pending != nil &&
				!event.StopReason.Incomplete() {
				s.session.previous = s.pending.ID
				s.session.property = s.property
				s.session.prefix = append(
					append([]json.RawMessage(nil), s.input...),
					replayOutput(s.pending.Output)...,
				)
			}
			if event.Type == provider.EventMessageStop {
				s.stopped = true
			}
			return event, nil
		}
		kind, data, err := s.session.conn.Read(s.ctx)
		if err != nil {
			s.session.invalidate()
			return provider.StreamEvent{}, err
		}
		if kind != websocket.MessageText {
			continue
		}
		events, err := s.decoder.Decode(data)
		if err != nil {
			s.session.invalidate()
			return provider.StreamEvent{}, err
		}
		s.queue = append(s.queue, events...)
	}
}

func (s *responsesSocketStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.session.lastUsed = time.Now()
	if s.session.idle != nil {
		s.session.idle.Stop()
	}
	if s.idleTimeout > 0 {
		session := s.session
		session.idle = time.AfterFunc(s.idleTimeout, func() {
			session.mu.Lock()
			defer session.mu.Unlock()
			if time.Since(session.lastUsed) >= s.idleTimeout {
				session.invalidate()
			}
		})
	}
	s.session.mu.Unlock()
	return nil
}

func (s *responsesSession) invalidate() {
	if s.conn != nil {
		_ = s.conn.Close(websocket.StatusNormalClosure, "")
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.idle != nil {
		s.idle.Stop()
	}
	s.conn, s.cancel, s.previous, s.property, s.prefix = nil, nil, "", "", nil
	s.idle = nil
}

func replayOutput(items []json.RawMessage) []json.RawMessage {
	var result []json.RawMessage
	for _, raw := range items {
		var item map[string]any
		if json.Unmarshal(raw, &item) != nil {
			continue
		}
		switch item["type"] {
		case "function_call":
			result = appendJSON(result, map[string]any{
				"type": "function_call", "call_id": item["call_id"],
				"name": item["name"], "arguments": item["arguments"],
			})
		case "reasoning":
			text := responseItemText(item["content"])
			if text == "" {
				text = responseItemText(item["summary"])
			}
			if text != "" {
				value := map[string]any{"type": "reasoning", "content": []map[string]any{{
					"type": "reasoning_text", "text": text,
				}}}
				if item["id"] != nil {
					value["id"] = item["id"]
				}
				result = appendJSON(result, value)
			}
		case "message":
			if text := responseItemText(item["content"]); text != "" {
				result = appendJSON(result, map[string]any{"role": "assistant", "content": text})
			}
		}
	}
	return result
}

func responseItemText(value any) string {
	parts, _ := value.([]any)
	var text string
	for _, raw := range parts {
		part, _ := raw.(map[string]any)
		if value, ok := part["text"].(string); ok {
			text += value
		}
	}
	return text
}

func appendJSON(items []json.RawMessage, value any) []json.RawMessage {
	raw, err := json.Marshal(value)
	if err == nil {
		items = append(items, raw)
	}
	return items
}

var _ provider.Stream = (*responsesSocketStream)(nil)
