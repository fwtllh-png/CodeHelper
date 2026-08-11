package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
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
	return &provider.SliceStream{Events: []provider.StreamEvent{
		{Type: provider.EventTextDelta, Text: "echo:" + prompt},
		{Type: provider.EventMessageStop},
	}}, nil
}
