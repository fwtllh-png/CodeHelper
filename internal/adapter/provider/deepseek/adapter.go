package deepseek

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/openai"
	providerwire "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/wire"
)

const reasoningPlaceholder = "(continued)"
const emptyToolOutput = "(empty tool output)"

type Adapter struct{}

func NewAdapter() *Adapter           { return &Adapter{} }
func (*Adapter) ID() model.AdapterID { return model.AdapterDeepSeek }
func (*Adapter) Supports(protocol model.WireProtocol) bool {
	return protocol == model.ProtocolOpenAIChat ||
		protocol == model.ProtocolOpenAIResponses
}
func (*Adapter) Prepare(request provider.ModelRequest) (providerwire.PreparedCall, error) {
	if request.Route.Protocol() == model.ProtocolOpenAIResponses {
		return openai.PrepareResponses(
			request, model.AdapterDeepSeek, reasoningPlaceholder, false,
		)
	}
	if request.Route.Protocol() != model.ProtocolOpenAIChat {
		return providerwire.PreparedCall{}, fmt.Errorf(
			"DeepSeek does not support protocol %q", request.Route.Protocol(),
		)
	}
	return openai.PrepareChat(
		request, model.AdapterDeepSeek, openai.ChatPolicy{
			ReasoningWithToolsOnly: true,
			RejectImages:           true,
			EmptyToolOutput:        emptyToolOutput,
			ThinkingOff:            true,
		},
	)
}
func (*Adapter) OpenStream(
	body io.ReadCloser,
	call providerwire.PreparedCall,
) (provider.Stream, error) {
	return newStream(body, call.Protocol)
}
func (*Adapter) ClassifyHTTP(failure providerwire.HTTPFailure) error {
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(failure.Body), &payload)
	message := payload.Error.Message
	if message == "" {
		message = fmt.Sprintf("DeepSeek returned HTTP %d", failure.Status)
	}
	code := classifyFailure(
		failure.Status, payload.Error.Code, payload.Error.Type, message,
	)
	return providerwire.TypedHTTPFailure(
		failure, code, message,
		providerwire.FirstHeader(
			failure.Header,
			"X-Deepseek-Request-Id",
			"X-Request-Id",
		),
	)
}
func classifyFailure(
	status int,
	code, kind, message string,
) provider.FailureCode {
	value := strings.ToLower(code + " " + kind + " " + message)
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return provider.FailureAuth
	case strings.Contains(value, "insufficient_quota") ||
		strings.Contains(value, "credits exhausted"):
		return provider.FailureQuota
	case status == http.StatusTooManyRequests:
		return provider.FailureRateLimit
	case strings.Contains(value, "context_length") ||
		strings.Contains(value, "context length"):
		return provider.FailureContextWindowExceeded
	case status == http.StatusBadRequest:
		return provider.FailureInvalidRequest
	case status >= 500:
		return provider.FailureServer
	default:
		return provider.FailureUnknown
	}
}
