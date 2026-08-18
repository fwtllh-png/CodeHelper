package engine

import (
	"io"
	"strings"
	"syscall"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerassembly "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/assembly"
	providerfixture "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/fixture"
	provideropenai "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/openai"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
)

func TestR3DisconnectAfterConfirmedChunkContinuesWithoutLoss(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&eventErrorStream{
			events: []provider.StreamEvent{
				{Type: provider.EventMessageStart},
				{Type: provider.EventTextDelta, Text: "partial"},
			},
			err: io.ErrUnexpectedEOF,
		},
		textStream(" answer"),
	}}
	engine := newEngine(t, runtime, nil)
	result, err := engine.Run(t.Context(), "review", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "partial answer" || len(runtime.requests) != 2 {
		t.Fatalf("result=%+v requests=%d", result, len(runtime.requests))
	}
	if runtime.requests[0].Projection.Retry ||
		!runtime.requests[1].Projection.Retry ||
		runtime.requests[0].LogicalRequestID == "" ||
		runtime.requests[0].LogicalRequestID !=
			runtime.requests[1].LogicalRequestID ||
		runtime.requests[0].TransportAttempt != 1 ||
		runtime.requests[1].TransportAttempt != 2 {
		t.Fatalf(
			"request attribution: first=%+v second=%+v",
			runtime.requests[0],
			runtime.requests[1],
		)
	}
	var retained, feedback bool
	for _, message := range runtime.requests[1].Messages {
		switch {
		case message.Role == provider.RoleAssistant &&
			message.Text() == "partial":
			retained = true
		case message.Role == provider.RoleUser &&
			strings.Contains(
				message.Text(),
				"[continue_after_incomplete",
			):
			feedback = true
		}
	}
	if !retained || !feedback {
		t.Fatalf(
			"continuation lost confirmed data: %+v",
			runtime.requests[1].Messages,
		)
	}
}

func TestR3IncompleteToolFragmentIsRetainedAndExecutedOnlyAfterClosure(
	t *testing.T,
) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&eventErrorStream{
			events: []provider.StreamEvent{
				{Type: provider.EventMessageStart},
				{
					Type: provider.EventToolCallDelta,
					ToolCall: &provider.ToolCallFragment{
						Index: 0, ID: "call-1", Name: "echo",
						Arguments: `{"text":`,
					},
				},
			},
			err: syscall.ECONNRESET,
		},
		toolCallStream(
			"call-1",
			"echo",
			`{"text":"evidence"}`,
		),
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "done"},
			{
				Type:       provider.EventMessageStop,
				StopReason: provider.StopReasonEndTurn,
			},
		}},
	}}
	executor := &echoTool{}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, runtime, registry)
	result, err := engine.Run(t.Context(), "review", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "done" ||
		executor.calls.Load() != 1 ||
		len(result.Tools) != 1 ||
		len(runtime.requests) != 3 {
		t.Fatalf(
			"result=%+v executions=%d requests=%d",
			result,
			executor.calls.Load(),
			len(runtime.requests),
		)
	}
	var rawFragment bool
	for _, message := range runtime.requests[1].Messages {
		if message.Role == provider.RoleUser &&
			strings.Contains(message.Text(), `"arguments":"{\"text\":"`) &&
			strings.Contains(message.Text(), "were not") {
			rawFragment = true
		}
	}
	if !rawFragment {
		t.Fatalf(
			"continuation lost raw tool fragment: %+v",
			runtime.requests[1].Messages,
		)
	}
}

func TestR3SparseProviderSequencesPreserveCompleteToolCalls(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{
				Type: provider.EventReasoningDelta, Text: "inspect",
				Sequenced: true, Sequence: 350,
			},
			{
				Type: provider.EventToolCallDelta,
				ToolCall: &provider.ToolCallFragment{
					Index: 0, ID: "call-1", Name: "echo",
					Arguments: `{"text":"evidence"}`,
				},
				Sequenced: true, Sequence: 377,
			},
			{
				Type:       provider.EventMessageStop,
				StopReason: provider.StopReasonToolUse,
				Sequenced:  true, Sequence: 380,
			},
		}},
		textStream("done"),
	}}
	executor := &echoTool{}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	result, err := newEngine(t, runtime, registry).Run(
		t.Context(),
		"review",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "done" ||
		executor.calls.Load() != 1 ||
		len(result.Tools) != 1 {
		t.Fatalf(
			"result=%+v executions=%d",
			result,
			executor.calls.Load(),
		)
	}
}

func TestR3FineGrainedDeltasAreCoalescedBeforeDurableCheckpoint(t *testing.T) {
	const deltaCount = 1_000
	events := make([]provider.StreamEvent, 0, deltaCount+2)
	for range deltaCount {
		events = append(events, provider.StreamEvent{
			Type: provider.EventReasoningDelta,
			Text: "x",
		})
	}
	events = append(events,
		provider.StreamEvent{
			Type: provider.EventTextDelta,
			Text: "done",
		},
		provider.StreamEvent{
			Type:       provider.EventMessageStop,
			StopReason: provider.StopReasonEndTurn,
		},
	)
	store := turnkernel.NewMemoryTerminalEnvelopeStore(nil, nil)
	coordinators, err := turnkernel.NewStoreCoordinatorRuntime(store)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &scriptedProvider{streams: []provider.Stream{
		&providerfixture.SliceStream{Events: events},
	}}
	engine := newEngine(t, runtime, nil)
	engine.options.TurnCoordinatorRuntime = coordinators
	result, err := engine.RunForTurn(
		t.Context(),
		"turn-r3-coalesced-checkpoint",
		"review",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "done" ||
		result.Reasoning != strings.Repeat("x", deltaCount) {
		t.Fatalf(
			"text=%q reasoning_length=%d",
			result.Text,
			len(result.Reasoning),
		)
	}
	facts, err := store.LoadDomainFacts(
		t.Context(),
		"turn-r3-coalesced-checkpoint",
	)
	if err != nil {
		t.Fatal(err)
	}
	var progress int
	var assembly *providerassembly.ResponseAssembly
	for _, fact := range facts {
		if fact.Command != "model_sample_progress_recorded" {
			continue
		}
		progress++
		assembly = fact.State.SampleLedger["turn-1-step-1"].Assembly
	}
	if progress >= deltaCount/20 || assembly == nil ||
		assembly.State != providerassembly.ResponseComplete ||
		assembly.EventCount() >= deltaCount/20 ||
		len(assembly.ConfirmedBlocks()) != 2 {
		t.Fatalf(
			"progress=%d assembly=%+v facts=%d",
			progress,
			assembly,
			len(facts),
		)
	}
}

func TestR3EnginePersistsEveryConfirmedProviderIncrement(t *testing.T) {
	store := turnkernel.NewMemoryTerminalEnvelopeStore(nil, nil)
	coordinators, err := turnkernel.NewStoreCoordinatorRuntime(store)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &scriptedProvider{streams: []provider.Stream{
		textStream("durable"),
	}}
	engine := newEngine(t, runtime, nil)
	engine.options.TurnCoordinatorRuntime = coordinators
	result, err := engine.RunForTurn(
		t.Context(),
		"turn-r3-checkpoint",
		"review",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "durable" {
		t.Fatalf("result = %+v", result)
	}
	facts, err := store.LoadDomainFacts(
		t.Context(),
		"turn-r3-checkpoint",
	)
	if err != nil {
		t.Fatal(err)
	}
	var progress int
	var assembly *providerassembly.ResponseAssembly
	for _, fact := range facts {
		if fact.Command == "model_sample_progress_recorded" {
			progress++
			sample := fact.State.SampleLedger["turn-1-step-1"]
			assembly = sample.Assembly
		}
	}
	if progress < 3 || assembly == nil ||
		assembly.State != providerassembly.ResponseComplete ||
		assembly.EventCount() != 2 ||
		len(assembly.ConfirmedBlocks()) != 1 ||
		assembly.ConfirmedBlocks()[0].Text != "durable" {
		t.Fatalf(
			"progress=%d assembly=%+v facts=%d",
			progress,
			assembly,
			len(facts),
		)
	}
}

func TestR3ChatDoneWithoutFinishReasonCompletesLogicalSample(t *testing.T) {
	input := strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning_content":"think "},"finish_reason":null}]}`,
		"",
		`data: {"choices":[{"delta":{"content":"hello"},"finish_reason":null}]}`,
		"",
		`data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":1,"completion_tokens_details":{"reasoning_tokens":1}}}`,
		"",
		`data: [DONE]`,
		"",
		"",
	}, "\n")
	stream, err := provideropenai.NewStream(
		io.NopCloser(strings.NewReader(input)),
		model.ProtocolOpenAIChat,
	)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &scriptedProvider{streams: []provider.Stream{stream}}
	result, err := newEngine(t, runtime, nil).Run(
		t.Context(),
		"say hello",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "hello" || result.Reasoning != "think " ||
		len(runtime.requests) != 1 {
		t.Fatalf(
			"result=%+v requests=%d",
			result,
			len(runtime.requests),
		)
	}
}

type eventErrorStream struct {
	events []provider.StreamEvent
	err    error
}

func (s *eventErrorStream) Recv() (provider.StreamEvent, error) {
	if len(s.events) != 0 {
		event := s.events[0]
		s.events = s.events[1:]
		return event, nil
	}
	if s.err != nil {
		err := s.err
		s.err = nil
		return provider.StreamEvent{}, err
	}
	return provider.StreamEvent{}, io.EOF
}

func (*eventErrorStream) Close() error { return nil }
