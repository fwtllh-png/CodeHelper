package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

func TestSignatureReplayIsBoundToAnthropicAssistant(t *testing.T) {
	route := replayTestRoute(t)
	replay, err := anthropicReplayState(map[int]string{0: "signed-thinking"})
	if err != nil {
		t.Fatal(err)
	}
	request := provider.ModelRequest{
		Route: route,
		Messages: []provider.Message{
			provider.ProducedAssistant(
				route,
				[]provider.ContentBlock{{
					Type: provider.ContentReasoning, Text: "inspect",
				}},
				1,
				replay,
			),
			provider.TextMessage(provider.RoleUser, "continue"),
		},
		MaxOutputTokens: 128,
	}
	call, err := NewAdapter().Prepare(request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(call.Body), `"signature":"signed-thinking"`) {
		t.Fatalf("signature replay missing: %s", call.Body)
	}
	if strings.Contains(string(call.Body), `"provenance"`) {
		t.Fatalf("provenance leaked onto provider wire: %s", call.Body)
	}
}

func TestSignatureReplayRejectsMalformedStateBeforeIO(t *testing.T) {
	route := replayTestRoute(t)
	message := provider.ProducedAssistant(
		route,
		[]provider.ContentBlock{{
			Type: provider.ContentReasoning, Text: "inspect",
		}},
		1,
		&provider.ReplayState{
			Version: provider.ReplayVersion,
			Data: json.RawMessage(
				`{"signatures":[{"block":0,"value":"a"},` +
					`{"block":0,"value":"b"}]}`,
			),
		},
	)
	request := provider.ModelRequest{
		Route: route,
		Messages: []provider.Message{
			message,
			provider.TextMessage(provider.RoleUser, "continue"),
		},
		MaxOutputTokens: 128,
	}
	if _, err := NewAdapter().Prepare(request); err == nil ||
		!strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("malformed replay error = %v", err)
	}
}

func replayTestRoute(t *testing.T) model.ReadyRoute {
	t.Helper()
	catalog, err := model.NewCatalog(model.Provider{
		ID: "anthropic-test", Adapter: model.AdapterAnthropic,
		Endpoint:   "https://api.anthropic.test",
		Protocol:   model.ProtocolAnthropic,
		Provenance: model.ProvenanceFixture,
		Models: map[string]model.Model{"model": {
			ID: "model", CanonicalID: "model", WireID: "model",
			Limits: model.Limits{
				ContextTokens: 4096, MaxOutputTokens: 1024,
			},
			Capabilities: model.Capabilities{
				Streaming: true, Reasoning: true, ToolCalls: true,
			},
			Pricing:    model.Pricing{Provenance: model.ProvenanceFixture},
			Provenance: model.ProvenanceFixture,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := model.NewResolver(catalog)
	if err != nil {
		t.Fatal(err)
	}
	route, err := resolver.Resolve(model.RouteRequest{
		ProviderID: "anthropic-test", ModelID: "model",
	})
	if err != nil {
		t.Fatal(err)
	}
	return route
}
