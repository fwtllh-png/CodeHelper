package replay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/evidence"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/httpclient"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/openai"
	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
	runtimeapp "github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func ProductionExecutors() map[Level]LevelExecutor {
	return map[Level]LevelExecutor{
		LevelProvider: providerExecutor{},
		LevelRuntime:  runtimeExecutor{},
		LevelHost:     hostExecutor{},
	}
}

type providerExecutor struct{}

func (providerExecutor) Execute(
	ctx context.Context,
	events []evidence.Envelope,
) (Outcome, error) {
	if !containsSource(events, evidence.SourceProvider) {
		return Outcome{}, fmt.Errorf("Provider Replay requires provider evidence")
	}
	malformed := containsKind(events, "provider.malformed_event")
	unknown := containsKind(events, "provider.unknown_event")
	lines := []string{
		`data: {"choices":[{"delta":{"content":"hello"},"finish_reason":null}]}`,
		"",
		`data: {"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1}}`,
		"",
		"data: [DONE]",
		"",
	}
	if malformed {
		lines = []string{`data: {"choices":[`, "", "data: [DONE]", ""}
	} else if unknown {
		lines = append(
			[]string{`data: {"future_event":true}`, ""},
			lines...,
		)
	}
	payload := strings.Join(lines, "\n")
	delay := providerDelay(events)
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := writer.(http.Flusher)
		for index := 0; index < len(payload); {
			width := 1 + index%7
			if index+width > len(payload) {
				width = len(payload) - index
			}
			_, _ = io.WriteString(writer, payload[index:index+width])
			if flusher != nil {
				flusher.Flush()
			}
			if delay > 0 {
				time.Sleep(delay)
			}
			index += width
		}
	}))
	defer server.Close()

	route, err := replayRoute(server.URL)
	if err != nil {
		return Outcome{}, err
	}
	request := provider.ModelRequest{
		Route:            route,
		LogicalRequestID: "evaluation-provider-replay",
		Messages:         []provider.Message{provider.TextMessage(provider.RoleUser, "hello")},
		MaxOutputTokens:  128,
		Idempotent:       true,
	}
	adapter, err := openai.NewAdapter(model.AdapterOpenAICompatible)
	if err != nil {
		return Outcome{}, err
	}
	call, err := adapter.Prepare(request)
	if err != nil {
		return Outcome{}, err
	}
	client := httpclient.New()
	client.Credentials = replayCredentials("fixture-key")
	stream, err := client.Execute(ctx, request, call, adapter)
	if err != nil {
		return Outcome{}, err
	}
	providerEvents, err := provider.Drain(stream)
	if err != nil {
		if malformed {
			outcome, structuralErr := Execute(events)
			outcome.Level = LevelProvider
			return outcome, structuralErr
		}
		return Outcome{}, err
	}
	if malformed {
		return Outcome{}, fmt.Errorf("malformed Provider Replay was not rejected")
	}
	if len(providerEvents) < 2 {
		return Outcome{}, fmt.Errorf("production Provider Replay emitted %d events", len(providerEvents))
	}
	outcome, err := Execute(events)
	outcome.Level = LevelProvider
	return outcome, err
}

type runtimeExecutor struct{}

func (runtimeExecutor) Execute(
	ctx context.Context,
	events []evidence.Envelope,
) (Outcome, error) {
	if err := evidence.ValidateAll(events); err != nil {
		return Outcome{}, err
	}
	runtime := runtimeapp.NewRuntime(runtimeapp.Options{
		Engine: runtimeapp.NoopEngine{},
	})
	defer runtime.Close(context.Background())
	stream, err := runtime.Events(ctx, 0)
	if err != nil {
		return Outcome{}, err
	}
	threadID, err := protocol.NewThreadID()
	if err != nil {
		return Outcome{}, err
	}
	turnID, err := protocol.NewTurnID()
	if err != nil {
		return Outcome{}, err
	}
	itemID, err := protocol.NewItemID()
	if err != nil {
		return Outcome{}, err
	}
	operation, err := protocol.NewOperation(&protocol.StartTurnPayload{
		ThreadID: threadID,
		TurnID:   turnID,
		ItemID:   itemID,
		Prompt:   "foundation runtime replay",
	})
	if err != nil {
		return Outcome{}, err
	}
	if err := runtime.Submit(ctx, operation); err != nil {
		return Outcome{}, err
	}
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	terminal := ""
	for terminal == "" {
		select {
		case event, ok := <-stream:
			if !ok {
				return Outcome{}, fmt.Errorf("production Runtime Replay event stream closed")
			}
			if protocol.IsTerminalEvent(event.Kind) {
				terminal = string(event.Kind)
			}
		case <-timeout.C:
			return Outcome{}, fmt.Errorf("production Runtime Replay did not reach terminal")
		case <-ctx.Done():
			return Outcome{}, ctx.Err()
		}
	}
	outcome, err := Execute(events)
	if err != nil {
		return Outcome{}, err
	}
	outcome.Level = LevelRuntime
	if terminal == "" {
		return Outcome{}, fmt.Errorf("production Runtime Replay terminal is empty")
	}
	return outcome, nil
}

type hostExecutor struct{}

func (hostExecutor) Execute(
	ctx context.Context,
	events []evidence.Envelope,
) (Outcome, error) {
	if err := evidence.ValidateAll(events); err != nil {
		return Outcome{}, err
	}
	workspace, err := os.MkdirTemp("", "codehelper-eval-host-")
	if err != nil {
		return Outcome{}, err
	}
	defer os.RemoveAll(workspace)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := cli.RunContext(
		ctx,
		[]string{"quickstart", "--workspace", workspace, "--json"},
		strings.NewReader(""),
		&stdout,
		&stderr,
	)
	if code != 0 {
		return Outcome{}, fmt.Errorf("production Host Replay failed with exit %d", code)
	}
	var report struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		return Outcome{}, fmt.Errorf("decode production Host Replay result: %w", err)
	}
	if !report.OK {
		return Outcome{}, fmt.Errorf("production Host Replay report is not successful")
	}
	outcome, err := Execute(events)
	outcome.Level = LevelHost
	return outcome, err
}

func replayRoute(endpoint string) (model.ReadyRoute, error) {
	catalog, err := model.NewCatalog(model.Provider{
		ID:         "evaluation",
		Adapter:    model.AdapterOpenAICompatible,
		Endpoint:   endpoint,
		Protocol:   model.ProtocolOpenAIChat,
		Credential: model.CredentialRef{Kind: "env", Name: "EVALUATION_FIXTURE_KEY"},
		Provenance: model.ProvenanceFixture,
		Models: map[string]model.Model{
			"fixture-model": {
				ID:          "fixture-model",
				CanonicalID: "fixture-model",
				WireID:      "fixture-model",
				Limits: model.Limits{
					ContextTokens: 8192, MaxOutputTokens: 4096,
				},
				Capabilities: model.Capabilities{Streaming: true},
				Pricing: model.Pricing{
					Currency: "USD", Provenance: model.ProvenanceFixture,
				},
				Provenance: model.ProvenanceFixture,
			},
		},
	})
	if err != nil {
		return model.ReadyRoute{}, err
	}
	resolver, err := model.NewResolver(catalog)
	if err != nil {
		return model.ReadyRoute{}, err
	}
	return resolver.Resolve(model.RouteRequest{
		ProviderID: "evaluation",
		ModelID:    "fixture-model",
		Provenance: model.ProvenanceFixture,
	})
}

type replayCredentials string

func (r replayCredentials) Resolve(
	context.Context,
	model.CredentialRef,
) (string, error) {
	return string(r), nil
}

func containsSource(events []evidence.Envelope, source evidence.Source) bool {
	for _, event := range events {
		if event.Source == source {
			return true
		}
	}
	return false
}

func containsKind(events []evidence.Envelope, kind string) bool {
	for _, event := range events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}

func providerDelay(events []evidence.Envelope) time.Duration {
	var maximum int64
	for _, event := range events {
		if event.OffsetMS > maximum {
			maximum = event.OffsetMS
		}
	}
	if maximum <= 1 {
		return 0
	}
	if maximum > 5 {
		maximum = 5
	}
	return time.Duration(maximum) * time.Millisecond
}
