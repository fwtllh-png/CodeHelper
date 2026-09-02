package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fwtllh-png/QCode/internal/adapter/model"
	"github.com/fwtllh-png/QCode/internal/adapter/provider"
)

func TestResponsesReplayIsLosslessForSameAdapter(t *testing.T) {
	request := testRequest(
		t, "https://api.openai.test", model.ProtocolOpenAIResponses,
	)
	reasoning := json.RawMessage(
		`{"type":"reasoning","id":"rs_1","encrypted_content":"ciphertext",` +
			`"summary":[{"type":"summary_text","text":"inspect"}]}`,
	)
	request.Messages = []provider.Message{
		provider.ProducedAssistant(
			request.Route,
			[]provider.ContentBlock{{
				Type: provider.ContentReasoning, ID: "rs_1", Text: "inspect",
			}},
			1,
			mustReplayState(t, []json.RawMessage{reasoning}),
		),
		provider.TextMessage(provider.RoleUser, "continue"),
	}
	adapter, err := NewAdapter(model.AdapterOpenAI)
	if err != nil {
		t.Fatal(err)
	}
	call, err := adapter.Prepare(request)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(call.Body, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Input) < 1 || body.Input[0]["id"] != "rs_1" ||
		strings.Contains(string(call.Body), "ciphertext") {
		t.Fatalf("replayed request = %s", call.Body)
	}
}

func TestResponsesReplayRejectsMalformedNativeItemBeforeIO(t *testing.T) {
	request := testRequest(
		t, "https://api.openai.test", model.ProtocolOpenAIResponses,
	)
	request.Messages = []provider.Message{
		provider.ProducedAssistant(
			request.Route,
			[]provider.ContentBlock{{Type: provider.ContentText, Text: "answer"}},
			1,
			mustReplayState(t, []json.RawMessage{
				json.RawMessage(`{"type":"message","id":"msg_1"}`),
			}),
		),
		provider.TextMessage(provider.RoleUser, "continue"),
	}
	adapter, err := NewAdapter(model.AdapterOpenAI)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Prepare(request); err == nil ||
		!strings.Contains(err.Error(), "unsupported type") {
		t.Fatalf("malformed replay error = %v", err)
	}
}

func TestResponsesReplayDropsUnrelatedNativeItem(t *testing.T) {
	request := testRequest(
		t, "https://api.openai.test", model.ProtocolOpenAIResponses,
	)
	reasoning := json.RawMessage(
		`{"type":"reasoning","id":"rs_native",` +
			`"content":[{"type":"reasoning_text","text":"native chain"}],` +
			`"summary":[{"type":"summary_text","text":"other summary"}]}`,
	)
	request.Messages = []provider.Message{
		provider.ProducedAssistant(
			request.Route,
			[]provider.ContentBlock{{
				Type: provider.ContentReasoning,
				ID:   "rs_native",
				Text: "visible reasoning",
			}},
			1,
			mustReplayState(t, []json.RawMessage{reasoning}),
		),
		provider.TextMessage(provider.RoleUser, "continue"),
	}
	adapter, err := NewAdapter(model.AdapterOpenAI)
	if err != nil {
		t.Fatal(err)
	}
	call, err := adapter.Prepare(request)
	if err != nil {
		t.Fatal(err)
	}
	body := string(call.Body)
	if strings.Contains(body, "rs_native") ||
		strings.Contains(body, "native chain") ||
		!strings.Contains(body, "visible reasoning") {
		t.Fatalf("replayed request = %s", body)
	}
}

func mustReplayState(
	t *testing.T,
	items []json.RawMessage,
) *provider.ReplayState {
	t.Helper()
	state, err := replayState(items)
	if err != nil {
		t.Fatal(err)
	}
	return state
}
