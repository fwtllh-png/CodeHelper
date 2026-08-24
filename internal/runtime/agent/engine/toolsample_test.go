package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerfixture "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/fixture"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
)

// samplingTool stands in for image_analyze: a tool whose work is a model call.
type samplingTool struct {
	sampler provider.Provider
	route   model.ReadyRoute
}

func (samplingTool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "look", Description: "look at something", Visibility: tool.VisibleModel,
		Capability: tool.CapabilityRead, AccessMode: tool.AccessRead,
		ParallelPolicy: tool.ParallelConcurrent, SandboxRequirement: tool.SandboxNone,
		Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{}, "additionalProperties": false,
		},
	}
}

func (t samplingTool) Execute(ctx context.Context, _ json.RawMessage) (tool.Result, error) {
	stream, err := t.sampler.Stream(ctx, provider.ModelRequest{
		Route: t.route, Purpose: model.PurposeVision,
		Messages:        []provider.Message{provider.TextMessage(provider.RoleUser, "what is this")},
		MaxOutputTokens: 128, Idempotent: true,
	})
	if err != nil {
		return tool.Result{}, err
	}
	defer stream.Close()
	var text string
	for {
		event, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			return tool.Result{}, recvErr
		}
		if event.Type == provider.EventTextDelta {
			text += event.Text
		}
	}
	return tool.Result{Content: text}, nil
}

func usageStream(text string, usage provider.Usage) provider.Stream {
	return &providerfixture.SliceStream{Events: []provider.StreamEvent{
		{Type: provider.EventTextDelta, Text: text},
		{Type: provider.EventUsage, Usage: &usage},
		{Type: provider.EventMessageStop},
	}}
}

// TestAToolsModelCallLandsOnTheTurnsBooks is the T2 acceptance: what a tool
// spends sampling shows up as a usage event, in the turn total, in the cost at
// its own model's price, and as a span inside the tool that made it. Before this,
// a vision call cost real money and appeared in none of those places.
func TestAToolsModelCallLandsOnTheTurnsBooks(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	// The vision model is ten dollars per million input tokens; the act model in
	// testRoute is a different price, which is how the assertion can tell which
	// price was applied to which tokens.
	visionRoute := namedRoute(t, "eyes")
	scripted := &scriptedProvider{streams: []provider.Stream{
		// Step one asks for the tool.
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				ID: "call-1", Name: "look", Arguments: `{}`,
			}},
			{Type: provider.EventUsage, Usage: &provider.Usage{InputTokens: 100, OutputTokens: 10}},
			{Type: provider.EventMessageStop},
		}},
		// Step two answers with the tool's result in hand.
		usageStream("a login screen", provider.Usage{InputTokens: 200, OutputTokens: 20}),
	}}
	sampler := NewToolSampler(&scriptedProvider{streams: []provider.Stream{
		usageStream("a login screen", provider.Usage{InputTokens: 1_000_000, OutputTokens: 0}),
	}})
	if err := registry.Register(samplingTool{sampler: sampler, route: visionRoute}, nil); err != nil {
		t.Fatal(err)
	}
	engine, err := New(Options{ProviderConfig: ProviderConfig{Provider: scripted, Route: testRoute(t),
		MaxOutputTokens: 128, MaxSteps: 3}, ToolConfig: ToolConfig{Tools: registry}, SecurityConfig: SecurityConfig{Workspace: t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}

	var usageEvents []Event
	var toolResults []tool.Result
	result, err := engine.RunForTurn(t.Context(), "turn-1", "look at the screenshot",
		func(event Event) error {
			if event.Usage != nil && event.Sample != 0 {
				usageEvents = append(usageEvents, event)
			}
			if event.Result != nil {
				toolResults = append(toolResults, *event.Result)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("RunForTurn() error = %v, tool results = %+v", err, toolResults)
	}

	// The tool's tokens are in the turn total: 100+200 from the turn's own two
	// samples plus the million the tool bought.
	if result.Usage.InputTokens != 1_000_300 {
		t.Fatalf("turn input tokens = %d, want the tool's tokens included", result.Usage.InputTokens)
	}
	// A million tokens at the vision model's ten dollars per million, plus the
	// act model's price for 300 input and 30 output tokens. Pricing the whole
	// total at one model's rates is what this separation prevents.
	actCost := estimateCost(testRoute(t).Model().Pricing, provider.Usage{
		InputTokens: 300, OutputTokens: 30,
	})
	if want := actCost + 10; result.CostUSD != want {
		t.Fatalf("cost = %v, want %v (each sample at its own model's price)", result.CostUSD, want)
	}

	var toolSample *Event
	for index, event := range usageEvents {
		if event.Purpose == string(model.PurposeVision) {
			toolSample = &usageEvents[index]
		}
	}
	if toolSample == nil {
		t.Fatalf("no usage event named the vision purpose: %+v", usageEvents)
	}
	if toolSample.Model != "eyes" || toolSample.CostUSD != 10 || !toolSample.CostKnown {
		t.Fatalf("tool usage event = %+v", *toolSample)
	}
	// Samples are numbered within the turn across both kinds of call, because a
	// usage row is identified by (turn, sample) and two calls sharing a number
	// would overwrite each other.
	for _, event := range usageEvents {
		if event.Sample == 0 {
			t.Fatalf("usage event has no sample: %+v", event)
		}
	}
	if usageEvents[0].Sample == toolSample.Sample {
		t.Fatalf("the tool's sample reused number %d", toolSample.Sample)
	}

	spans := engine.TurnSpans()
	var modelCalls, nested int
	byID := make(map[uint64]trace.Record, len(spans))
	for _, span := range spans {
		byID[span.ID] = span
	}
	for _, span := range spans {
		if span.Name != trace.NameModelCall {
			continue
		}
		modelCalls++
		if span.Attributes["purpose"] == string(model.PurposeVision) {
			// The call belongs inside the tool that made it: a reader looking at
			// why the tool took four seconds should find the model call there.
			if parent := byID[span.ParentID]; parent.Name == trace.NameTool {
				nested++
			}
		}
	}
	if modelCalls != 3 {
		t.Fatalf("model call spans = %d, want the turn's two plus the tool's one", modelCalls)
	}
	if nested != 1 {
		t.Fatalf("the tool's model call was not recorded inside its tool span: %+v", spans)
	}
}

// TestASampleOutsideATurnIsStillServed pins that the wrapper is an accountant,
// not a gate: a tool used outside a turn (a test harness, a one-off) still gets
// its answer, it just has nowhere to charge it.
func TestASampleOutsideATurnIsStillServed(t *testing.T) {
	target := &scriptedProvider{streams: []provider.Stream{textStream("fine")}}
	sampler := NewToolSampler(target)

	stream, err := sampler.Stream(t.Context(), provider.ModelRequest{
		Route: namedRoute(t, "eyes"), Purpose: model.PurposeVision,
		Messages:        []provider.Message{provider.TextMessage(provider.RoleUser, "hi")},
		MaxOutputTokens: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if len(target.requests) != 1 {
		t.Fatalf("requests = %d, want the call to go through", len(target.requests))
	}
}

func TestToolSampleUsageEmitFailureStopsTheStream(t *testing.T) {
	engine, err := New(Options{ProviderConfig: ProviderConfig{Provider: &scriptedProvider{streams: []provider.Stream{textStream("unused")}},
		Route: testRoute(t), MaxOutputTokens: 128}, SecurityConfig: SecurityConfig{Workspace: t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	persistErr := errors.New("usage append failed")
	account := &toolAccount{
		engine: engine,
		emit: func(Event) error {
			return persistErr
		},
	}
	stream, err := account.stream(
		t.Context(),
		&scriptedProvider{streams: []provider.Stream{
			usageStream("seen", provider.Usage{InputTokens: 17, OutputTokens: 3}),
		}},
		provider.ModelRequest{
			Route: namedRoute(t, "eyes"), Purpose: model.PurposeVision,
			Messages: []provider.Message{provider.TextMessage(provider.RoleUser, "inspect")},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("text event: %v", err)
	}
	if _, err := stream.Recv(); !errors.Is(err, persistErr) {
		t.Fatalf("usage event error = %v, want %v", err, persistErr)
	}
	spend := engine.drainToolSpend()
	if spend.usage.InputTokens != 17 || spend.usage.OutputTokens != 3 {
		t.Fatalf("tool spend = %+v", spend.usage)
	}
}

func TestToolSampleUsageProjectionFailureIsSecondary(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	sampler := NewToolSampler(&scriptedProvider{streams: []provider.Stream{
		usageStream("an image", provider.Usage{InputTokens: 23, OutputTokens: 4}),
	}})
	if err := registry.Register(samplingTool{
		sampler: sampler, route: namedRoute(t, "eyes"),
	}, nil); err != nil {
		t.Fatal(err)
	}
	engine, err := New(Options{ProviderConfig: ProviderConfig{Provider: &scriptedProvider{streams: []provider.Stream{
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				ID: "call-usage-failure", Name: "look", Arguments: `{}`,
			}},
			{Type: provider.EventMessageStop},
		}},
		textStream("done"),
	}},
		Route:           testRoute(t),
		MaxOutputTokens: 128, MaxSteps: 2}, ToolConfig: ToolConfig{Tools: registry}, SecurityConfig: SecurityConfig{Workspace: t.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	persistErr := errors.New("usage projection failed")
	var terminal Event
	result, err := engine.RunForTurn(
		t.Context(),
		"turn-usage-failure",
		"inspect",
		func(event Event) error {
			if event.Usage != nil && event.Purpose == string(model.PurposeVision) {
				return persistErr
			}
			if event.State == Completed {
				terminal = event
			}
			return nil
		},
	)
	if err != nil || result.State != Completed {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if result.Usage.InputTokens != 23 || result.Usage.OutputTokens != 4 {
		t.Fatalf("turn usage = %+v", result.Usage)
	}
	if len(terminal.SecondaryIssues) != 1 ||
		terminal.SecondaryIssues[0].Phase != "event_projection" ||
		terminal.SecondaryIssues[0].Message != persistErr.Error() {
		t.Fatalf("terminal secondary issues = %+v", terminal.SecondaryIssues)
	}
}
