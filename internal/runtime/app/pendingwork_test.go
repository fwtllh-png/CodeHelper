package app

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func TestRoutePendingFourSourcesRuleTable(t *testing.T) {
	phases := []TurnPhase{
		PhaseIdle, PhaseRunning, PhaseAwaitingApproval, PhaseAwaitingInput,
	}
	type row struct {
		source      PendingSource
		triggerTurn bool
		want        map[TurnPhase]PendingDisposition
	}
	rows := []row{
		{
			source: SourceSteer,
			want: map[TurnPhase]PendingDisposition{
				PhaseIdle:             DispositionStartNewTurn,
				PhaseRunning:          DispositionInjectCurrent,
				PhaseAwaitingApproval: DispositionInjectCurrent,
				PhaseAwaitingInput:    DispositionInjectCurrent,
			},
		},
		{
			source:      SourceMailbox,
			triggerTurn: true,
			want: map[TurnPhase]PendingDisposition{
				PhaseIdle:             DispositionStartNewTurn,
				PhaseRunning:          DispositionInjectCurrent,
				PhaseAwaitingApproval: DispositionBuffer,
				PhaseAwaitingInput:    DispositionBuffer,
			},
		},
		{
			source: SourceMailbox,
			want: map[TurnPhase]PendingDisposition{
				PhaseIdle:             DispositionBuffer,
				PhaseRunning:          DispositionBuffer,
				PhaseAwaitingApproval: DispositionBuffer,
				PhaseAwaitingInput:    DispositionBuffer,
			},
		},
		{
			source: SourceApproval,
			want: map[TurnPhase]PendingDisposition{
				PhaseIdle:             DispositionReject,
				PhaseRunning:          DispositionReject,
				PhaseAwaitingApproval: DispositionResumePaused,
				PhaseAwaitingInput:    DispositionReject,
			},
		},
		{
			source: SourceInput,
			want: map[TurnPhase]PendingDisposition{
				PhaseIdle:             DispositionReject,
				PhaseRunning:          DispositionReject,
				PhaseAwaitingApproval: DispositionReject,
				PhaseAwaitingInput:    DispositionResumePaused,
			},
		},
	}
	for _, tc := range rows {
		for _, phase := range phases {
			item := PendingItem{Source: tc.source, TriggerTurn: tc.triggerTurn}
			got := RoutePending(phase, item)
			want := tc.want[phase]
			if got != want {
				t.Fatalf("%s (want %s)", ExplainPending(phase, item, got), want)
			}
		}
	}
}

func TestRoutePendingUnknownSourceRejects(t *testing.T) {
	if got := RoutePending(PhaseRunning, PendingItem{Source: "future"}); got != DispositionReject {
		t.Fatalf("got %s", got)
	}
}

func TestRuntimeTurnPhaseClassification(t *testing.T) {
	engine := &kernelPhaseTestEngine{}
	runtime := NewRuntime(Options{Engine: engine})
	thread, err := protocol.NewThreadID()
	if err != nil {
		t.Fatal(err)
	}
	turn, err := protocol.NewTurnID()
	if err != nil {
		t.Fatal(err)
	}
	if phase := runtime.turnPhase(thread, turn); phase != PhaseIdle {
		t.Fatalf("idle phase = %s", phase)
	}

	lease, err := runtime.active.Reserve(thread, turn, "op", "item")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.active.Release(lease) })
	if phase := runtime.turnPhase(thread, turn); phase != PhaseRunning {
		t.Fatalf("running phase = %s", phase)
	}

	engine.phase = PhaseAwaitingApproval
	if phase := runtime.turnPhase(thread, turn); phase != PhaseAwaitingApproval {
		t.Fatalf("approval phase = %s", phase)
	}
	if disp := runtime.RouteMailbox(thread, turn, true); disp != DispositionBuffer {
		t.Fatalf("mailbox while awaiting approval = %s", disp)
	}

	engine.phase = PhaseAwaitingInput
	if phase := runtime.turnPhase(thread, turn); phase != PhaseAwaitingInput {
		t.Fatalf("input phase = %s", phase)
	}
}

type kernelPhaseTestEngine struct {
	testEngine
	phase TurnPhase
}

func (e *kernelPhaseTestEngine) TurnPhase(protocol.TurnID) (TurnPhase, bool) {
	return e.phase, e.phase != ""
}

func TestIdleSteerStartsNewTurn(t *testing.T) {
	engine := &recordingStartEngine{}
	runtime := NewRuntime(Options{Engine: engine})
	t.Cleanup(func() { closeRuntime(t, runtime) })
	events, err := runtime.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}

	thread, err := protocol.NewThreadID()
	if err != nil {
		t.Fatal(err)
	}
	turn, err := protocol.NewTurnID()
	if err != nil {
		t.Fatal(err)
	}
	item, err := protocol.NewItemID()
	if err != nil {
		t.Fatal(err)
	}
	op, err := protocol.NewOperation(&protocol.SteerTurnPayload{
		ThreadID: thread, TurnID: turn, ItemID: item, Prompt: "wake up",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Submit(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Kind == protocol.EventOperationRejected {
				t.Fatalf("rejected: %+v", event.Data)
			}
			if event.Kind == protocol.EventTurnStarted || event.Kind == protocol.EventTurnCompleted {
				if engine.starts.Load() >= 1 && engine.prompt() == "wake up" {
					return
				}
			}
		case <-deadline:
			t.Fatalf("timed out; starts=%d prompt=%q", engine.starts.Load(), engine.prompt())
		}
	}
}

type recordingStartEngine struct {
	testEngine
	starts     atomic.Int32
	mu         sync.Mutex
	lastPrompt string
}

func (e *recordingStartEngine) StartTurn(
	ctx context.Context, payload *protocol.StartTurnPayload, sink EngineSink,
) error {
	e.starts.Add(1)
	e.mu.Lock()
	e.lastPrompt = payload.Prompt
	e.mu.Unlock()
	return e.testEngine.StartTurn(ctx, payload, sink)
}

func (e *recordingStartEngine) prompt() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastPrompt
}
