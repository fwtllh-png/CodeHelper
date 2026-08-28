package engine

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

type serialBatchProbe struct {
	entered chan struct{}
	release chan struct{}
	active  atomic.Int32
	overlap atomic.Bool
}

func (p *serialBatchProbe) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "serial_batch_probe", Description: "test serial batch admission",
		Visibility: tool.VisibleModel, Capability: tool.CapabilityRead,
		AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelSerial,
		RepeatPolicy:       tool.RepeatExecute,
		SandboxRequirement: tool.SandboxNone,
		Availability:       tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object", "properties": map[string]any{},
			"additionalProperties": false,
		},
	}
}

func (p *serialBatchProbe) Execute(
	ctx context.Context,
	_ json.RawMessage,
) (tool.Result, error) {
	if p.active.Add(1) != 1 {
		p.overlap.Store(true)
	}
	defer p.active.Add(-1)
	p.entered <- struct{}{}
	select {
	case <-ctx.Done():
		return tool.Result{}, ctx.Err()
	case <-p.release:
		return tool.Result{Content: "done"}, nil
	}
}

func TestRunToolsSerialDescriptorsDoNotOverlapWithinBatch(t *testing.T) {
	probe := &serialBatchProbe{
		entered: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(probe); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, &scriptedProvider{}, registry)
	done := make(chan error, 1)
	go func() {
		_, err := engine.runTools(
			t.Context(),
			"turn-serial-batch",
			[]provider.ToolCall{
				{ID: "call-1", Name: "serial_batch_probe", Arguments: `{}`},
				{ID: "call-2", Name: "serial_batch_probe", Arguments: `{}`},
			},
			make(map[string]tool.Result),
			func(State, Event) error { return nil },
		)
		done <- err
	}()

	select {
	case <-probe.entered:
	case <-time.After(time.Second):
		t.Fatal("first serial tool did not start")
	}
	select {
	case <-probe.entered:
		close(probe.release)
		t.Fatal("second serial tool overlapped the first")
	case <-time.After(50 * time.Millisecond):
	}
	close(probe.release)
	select {
	case <-probe.entered:
	case <-time.After(time.Second):
		t.Fatal("second serial tool did not start after release")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if probe.overlap.Load() || probe.active.Load() != 0 {
		t.Fatalf(
			"serial probe overlap=%t active=%d",
			probe.overlap.Load(),
			probe.active.Load(),
		)
	}
}
