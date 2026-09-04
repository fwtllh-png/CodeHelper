package turnkernel

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func TestSecondFullReadDoesNotChangeWorkItemSignature(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	state = apply(t, state, BindWorkItem{
		Goal: "fix the deadlock",
		KnownReads: map[string]WorkItemRead{
			"socket_transport.cpp": {Window: "full"},
		},
	}).State
	before := FormatProgressSignature(state, 0, false)
	state = apply(t, state, ToolCallsProposed{
		Calls: []ToolCallState{{
			ID: "read-2", Name: "file_read",
			Arguments: `{"path":"socket_transport.cpp"}`,
		}},
	}).State
	state = apply(t, state, ToolResultReceived{
		CallID: "read-2",
		Observation: WorkItemObservation{
			ReadPath:   "socket_transport.cpp",
			ReadWindow: "full",
		},
	}).State
	if after := FormatProgressSignature(state, 0, false); after != before {
		t.Fatalf("second full read renewed signature: before=%q after=%q", before, after)
	}
}

func TestSamePathEditDoesNotRenewWorkItemSignature(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentWorkspaceChange)
	state = apply(t, state, ToolCallsProposed{
		Calls: []ToolCallState{{ID: "edit-1", Name: "file_edit"}},
	}).State
	state = apply(t, state, ToolResultReceived{
		CallID:  "edit-1",
		Changes: []ObservedChange{{Path: "socket_transport.cpp", Kind: "modified"}},
	}).State
	first := FormatProgressSignature(state, 0, false)
	if !strings.Contains(first, "edits=socket_transport.cpp") {
		t.Fatalf("first edit missing path set: %q", first)
	}
	state = apply(t, state, ToolCallsProposed{
		Calls: []ToolCallState{{ID: "edit-2", Name: "file_edit"}},
	}).State
	state = apply(t, state, ToolResultReceived{
		CallID:  "edit-2",
		Changes: []ObservedChange{{Path: "socket_transport.cpp", Kind: "modified"}},
	}).State
	if after := FormatProgressSignature(state, 0, false); after != first {
		t.Fatalf("same-path edit renewed signature: first=%q after=%q", first, after)
	}
	if state.MutationRevision != 2 {
		t.Fatalf("mutation revision = %d, want 2", state.MutationRevision)
	}
	state = apply(t, state, ObserveProgress{
		Signature: first, CompletedSamples: 0,
	}).State
	stale := apply(t, state, ObserveProgress{
		Signature: first, CompletedSamples: 1,
	}).State
	if stale.Progress.NoProgressSamples != 1 {
		t.Fatalf("same-path edit cleared no-progress: %+v", stale.Progress)
	}
}

func TestAnswerTurnKnownWorkItemFinishOnlyAtImplementLease(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	state.Policy.Convergence = ConvergencePolicyForStepLimit(64)
	state.Policy.ImplementNoProgressSamples = 6
	state = apply(t, state, BindWorkItem{
		Goal: "再试一下能不能跑通 socket_transport_test？",
		KnownReads: map[string]WorkItemRead{
			"socket_transport.cpp": {Window: "full"},
		},
	}).State
	signature := FormatProgressSignature(state, 0, false)
	state = apply(t, state, ObserveProgress{
		Signature: signature, CompletedSamples: 0,
	}).State
	for _, test := range []struct {
		samples uint32
		want    ProgressStage
	}{
		{samples: 2, want: ProgressStageNone},
		{samples: 3, want: ProgressStageConverge},
		{samples: 6, want: ProgressStageFinishOnly},
	} {
		state = apply(t, state, ObserveProgress{
			Signature: signature, CompletedSamples: test.samples,
		}).State
		if state.Progress.Stage != test.want ||
			state.Progress.NoProgressSamples != test.samples {
			t.Fatalf(
				"samples=%d progress=%+v, want stage=%s",
				test.samples,
				state.Progress,
				test.want,
			)
		}
	}
}

func TestObserveWorkItemResultRecordsReadAndSession(t *testing.T) {
	observation := ObserveWorkItemResult(
		ToolCallState{
			Name:      "file_read",
			Arguments: `{"path":"socket_transport.cpp","start_line":40}`,
		},
		tool.Result{Outcome: &tool.Outcome{Facts: &tool.OutcomeFacts{
			WorkspaceRead: &tool.WorkspaceReadFact{
				Path: "socket_transport.cpp", Digest: "sha256:abc",
			},
		}}},
	)
	if observation.ReadPath != "socket_transport.cpp" ||
		observation.ReadWindow != "40" ||
		observation.ContentDigest != "sha256:abc" {
		t.Fatalf("read observation = %+v", observation)
	}
	running := ObserveWorkItemResult(
		ToolCallState{Name: "exec_command"},
		tool.Result{Outcome: &tool.Outcome{Facts: &tool.OutcomeFacts{
			ProcessSession: &tool.ProcessSessionFact{
				SessionID: "sess-1", Running: true,
			},
		}}},
	)
	if running.OpenSession != "sess-1" || running.CloseSession != "" {
		t.Fatalf("running session = %+v", running)
	}
}

func TestBindWorkItemSeedsContinueKnownReads(t *testing.T) {
	kernel, err := NewRuntimeKernel(
		KernelIdentity{
			TurnID:          "continue-work-item",
			ProfileRevision: 1,
			Goal:            "你上一轮socket_transport_test测试时死锁了",
			WorkItem: WorkItem{
				KnownReads: map[string]WorkItemRead{
					"socket_transport.cpp": {Window: "full"},
				},
			},
		},
		protocol.TurnIntentAnswer,
		"act",
		&protocol.TurnRecoveryContext{
			Action:       protocol.TurnRecoveryContinue,
			SourceTurnID: "turn-source",
		},
		false,
		nil,
		nil,
		nil,
		nil,
		nil,
		DefaultPolicy(),
		NewEphemeralCoordinatorRuntime(),
	)
	if err != nil {
		t.Fatal(err)
	}
	item := kernel.WorkItem()
	if item.GoalDigest != GoalDigest("你上一轮socket_transport_test测试时死锁了") {
		t.Fatalf("goal digest = %q", item.GoalDigest)
	}
	if _, ok := item.KnownReads["socket_transport.cpp"]; !ok {
		t.Fatalf("known reads = %+v", item.KnownReads)
	}
	if !kernel.RecoveryContinue() {
		t.Fatal("continue relation was not bound")
	}
	before := kernel.ProgressSignature(0, false)
	call := provider.ToolCall{
		ID: "read-again", Name: "file_read",
		Arguments: `{"path":"socket_transport.cpp"}`,
	}
	if err := kernel.StartTools([]provider.ToolCall{call}); err != nil {
		t.Fatal(err)
	}
	if err := kernel.StartTool(call.ID); err != nil {
		t.Fatal(err)
	}
	if err := kernel.CloseTool(call, tool.Result{
		Outcome: &tool.Outcome{Facts: &tool.OutcomeFacts{
			WorkspaceRead: &tool.WorkspaceReadFact{Path: "socket_transport.cpp"},
		}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	if after := kernel.ProgressSignature(0, false); after != before {
		t.Fatalf("seeded re-read renewed signature: before=%q after=%q", before, after)
	}
}
