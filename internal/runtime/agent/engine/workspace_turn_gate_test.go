package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

func TestWorkspaceTurnGateSerializesAndReleasesOnce(t *testing.T) {
	gate := NewWorkspaceTurnGate()
	releaseFirst, err := gate.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan func(), 1)
	go func() {
		release, acquireErr := gate.Acquire(t.Context())
		if acquireErr == nil {
			acquired <- release
		}
	}()
	select {
	case <-acquired:
		t.Fatal("second turn acquired a workspace that is still leased")
	case <-time.After(30 * time.Millisecond):
	}
	releaseFirst()
	releaseFirst()
	select {
	case release := <-acquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("second turn did not acquire after release")
	}
}

func TestWorkspaceTurnGateWaitIsCancelable(t *testing.T) {
	gate := NewWorkspaceTurnGate()
	release, err := gate.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := gate.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire() error = %v, want context.Canceled", err)
	}
}

func TestEngineWaitsForWorkspaceGateBeforeSampling(t *testing.T) {
	gate := NewWorkspaceTurnGate()
	release, err := gate.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	runtime := &scriptedProvider{}
	engine, err := New(Options{
		Provider: runtime, Route: testRoute(t),
		MaxOutputTokens: 128, WorkspaceTurnGate: gate,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := engine.Run(ctx, "queued turn", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want context deadline", err)
	}
	if len(runtime.requests) != 0 {
		t.Fatalf("queued engine sampled before acquiring workspace: %+v", runtime.requests)
	}
}

func TestEnginesSharingWorkspaceGateRunTurnsSerially(t *testing.T) {
	gate := NewWorkspaceTurnGate()
	parentProvider := &steerProvider{started: make(chan struct{})}
	childProvider := &gateProbeProvider{called: make(chan struct{})}
	parent, err := New(Options{
		Provider: parentProvider, Route: testRoute(t),
		MaxOutputTokens: 128, WorkspaceTurnGate: gate,
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := New(Options{
		Provider: childProvider, Route: testRoute(t),
		MaxOutputTokens: 128, WorkspaceTurnGate: gate,
	})
	if err != nil {
		t.Fatal(err)
	}
	parentCtx, cancelParent := context.WithCancel(t.Context())
	parentDone := make(chan error, 1)
	go func() {
		_, runErr := parent.Run(parentCtx, "parent turn", nil)
		parentDone <- runErr
	}()
	select {
	case <-parentProvider.started:
	case <-time.After(time.Second):
		t.Fatal("parent turn did not start")
	}
	childDone := make(chan error, 1)
	go func() {
		_, runErr := child.Run(t.Context(), "child turn", nil)
		childDone <- runErr
	}()
	select {
	case <-childProvider.called:
		t.Fatal("child sampled while parent still held the shared workspace")
	case <-time.After(30 * time.Millisecond):
	}
	cancelParent()
	select {
	case err := <-parentDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("parent Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("parent turn did not release workspace after cancellation")
	}
	select {
	case <-childProvider.called:
	case <-time.After(time.Second):
		t.Fatal("child did not start after parent released workspace")
	}
	select {
	case err := <-childDone:
		if err != nil {
			t.Fatalf("child Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("child turn did not finish")
	}
}

type gateProbeProvider struct {
	called chan struct{}
	once   sync.Once
}

func (p *gateProbeProvider) Stream(
	context.Context,
	provider.ModelRequest,
) (provider.Stream, error) {
	p.once.Do(func() { close(p.called) })
	return &provider.SliceStream{Events: []provider.StreamEvent{
		{Type: provider.EventMessageStart},
		{Type: provider.EventTextDelta, Text: "done"},
		{Type: provider.EventMessageStop},
	}}, nil
}
