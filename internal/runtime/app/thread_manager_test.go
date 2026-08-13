package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerfixture "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/fixture"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestThreadManagerIsolatesHistory(t *testing.T) {
	seed, err := newTestAgentEngine(agentengine.Options{
		Provider: &threadEchoProvider{}, Route: runtimeTestRoute(t),
		Tools: tool.NewRegistry(nil, nil), Metrics: telemetry.NewMetrics(),
		MaxOutputTokens: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewThreadManager(func() (*EngineAdapter, error) {
		clone, err := seed.CloneEmpty()
		if err != nil {
			return nil, err
		}
		return AdaptEngine(clone), nil
	})
	runtime := NewRuntime(Options{Engine: manager})
	defer runtime.Close(context.Background())

	startA, err := protocol.NewOperation(&protocol.StartTurnPayload{
		ThreadID: "thread-a", TurnID: "turn-a", ItemID: "item-a", Prompt: "alpha-unique",
	})
	if err != nil {
		t.Fatal(err)
	}
	startB, err := protocol.NewOperation(&protocol.StartTurnPayload{
		ThreadID: "thread-b", TurnID: "turn-b", ItemID: "item-b", Prompt: "beta-unique",
	})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)
	go func() {
		defer wg.Done()
		errs <- runtime.Submit(context.Background(), startA)
	}()
	go func() {
		defer wg.Done()
		errs <- runtime.Submit(context.Background(), startB)
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		histA, err := manager.History("thread-a")
		if err != nil {
			t.Fatal(err)
		}
		histB, err := manager.History("thread-b")
		if err != nil {
			t.Fatal(err)
		}
		if historyContains(histA, "alpha-unique") && historyContains(histB, "beta-unique") &&
			!historyContains(histA, "beta-unique") && !historyContains(histB, "alpha-unique") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	histA, _ := manager.History("thread-a")
	histB, _ := manager.History("thread-b")
	t.Fatalf("histories not isolated:\nA=%+v\nB=%+v", histA, histB)
}

func TestThreadManagerBindsToolIdentityAndContextLookup(t *testing.T) {
	seen := make(chan tool.InvocationIdentity, 1)
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&identityCaptureTool{seen: seen}, nil); err != nil {
		t.Fatal(err)
	}
	engine, err := newTestAgentEngine(agentengine.Options{
		Provider: &identityToolProvider{}, Route: runtimeTestRoute(t),
		Tools: registry, Metrics: telemetry.NewMetrics(),
		MaxOutputTokens: 128, SessionID: "process-session",
	})
	if err != nil {
		t.Fatal(err)
	}
	created := 0
	manager := NewThreadManager(func() (*EngineAdapter, error) {
		created++
		return AdaptEngine(engine), nil
	})
	runtime := NewRuntime(Options{Engine: manager})
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	operation, err := protocol.NewOperation(&protocol.StartTurnPayload{
		ThreadID: "thread-parent", TurnID: "turn-parent",
		ItemID: "item-parent", Prompt: "capture identity",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Submit(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	select {
	case identity := <-seen:
		if identity.ThreadID != "thread-parent" ||
			identity.TurnID != "turn-parent" ||
			identity.CallID != "call-identity" {
			t.Fatalf("tool identity = %+v", identity)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("identity tool was not called")
	}
	if resolved, err := manager.ContextEngine("thread-parent"); err != nil ||
		resolved != engine {
		t.Fatalf("context engine = %p, err = %v", resolved, err)
	}
	if _, err := manager.ContextEngine("thread-missing"); err == nil {
		t.Fatal("missing context lookup created an engine")
	}
	if created != 1 {
		t.Fatalf("engine factory calls = %d, want 1", created)
	}
}

func historyContains(messages []provider.Message, needle string) bool {
	for _, message := range messages {
		if strings.Contains(message.Text(), needle) {
			return true
		}
		for _, block := range message.Blocks {
			if strings.Contains(block.Text, needle) {
				return true
			}
		}
	}
	return false
}

type identityCaptureTool struct {
	seen chan<- tool.InvocationIdentity
}

func (*identityCaptureTool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "capture_identity", Description: "capture the invocation identity",
		Visibility: tool.VisibleModel, Capability: tool.CapabilityRead,
		AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelSerial,
		SandboxRequirement: tool.SandboxNone,
		Availability:       tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{},
			"additionalProperties": false,
		},
	}
}

func (t *identityCaptureTool) Execute(
	ctx context.Context,
	_ json.RawMessage,
) (tool.Result, error) {
	t.seen <- tool.InvocationIdentityFrom(ctx)
	return tool.Result{Content: "captured"}, nil
}

type identityToolProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *identityToolProvider) Stream(
	context.Context,
	provider.ModelRequest,
) (provider.Stream, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	if call == 1 {
		return &providerfixture.SliceStream{Events: []provider.StreamEvent{
			{
				Type: provider.EventToolCallDelta,
				ToolCall: &provider.ToolCallFragment{
					Index: 0, ID: "call-identity",
					Name: "capture_identity", Arguments: `{}`,
				},
			},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonToolUse},
		}}, nil
	}
	return &providerfixture.SliceStream{Events: []provider.StreamEvent{
		{Type: provider.EventTextDelta, Text: "done"},
		{Type: provider.EventMessageStop, StopReason: provider.StopReasonEndTurn},
	}}, nil
}

type threadEchoProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *threadEchoProvider) Stream(
	_ context.Context, request provider.ModelRequest,
) (provider.Stream, error) {
	prompt := ""
	for i := len(request.Messages) - 1; i >= 0; i-- {
		if request.Messages[i].Role == provider.RoleUser {
			prompt = request.Messages[i].Text()
			break
		}
	}
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return &providerfixture.SliceStream{Events: []provider.StreamEvent{
		{Type: provider.EventTextDelta, Text: "echo:" + prompt},
		{Type: provider.EventMessageStop},
	}}, nil
}
