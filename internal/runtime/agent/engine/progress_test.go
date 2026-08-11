package engine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestProgressSignatureCountsResearchReadsOnlyForResearchTurns(
	t *testing.T,
) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.turn = 7
	answer := newEngineTurnKernel(
		protocol.TurnIntentAnswer,
		"act",
		nil,
		0,
		nil,
		nil,
	)
	workspace := newEngineTurnKernel(
		protocol.TurnIntentWorkspaceChange,
		"act",
		nil,
		0,
		nil,
		nil,
	)
	answerBefore := engine.progressSignature(answer)
	workspaceBefore := engine.progressSignature(workspace)

	engine.working.Observe(workingset.SourceRead, engine.turn, "a.go")

	if answerAfter := engine.progressSignature(answer); answerAfter == answerBefore {
		t.Fatal("new research path did not advance answer progress")
	}
	if workspaceAfter := engine.progressSignature(workspace); workspaceAfter != workspaceBefore {
		t.Fatal("read-only exploration advanced workspace-change progress")
	}
}

func TestFinishOnlyAllowsMutationAndQualityTools(t *testing.T) {
	for _, test := range []struct {
		name       string
		capability tool.Capability
		want       bool
	}{
		{name: "file_apply", capability: tool.CapabilityWrite, want: true},
		{name: "quality_test", capability: tool.CapabilityRead, want: true},
		{name: "file_read", capability: tool.CapabilityRead, want: true},
		{name: "shell_read", capability: tool.CapabilityRead, want: false},
		{name: "search_text", capability: tool.CapabilityRead, want: false},
	} {
		if got := finishOnlyToolAllowed(test.name, tool.Descriptor{
			Name: test.name, Capability: test.capability,
		}); got != test.want {
			t.Fatalf(
				"finishOnlyToolAllowed(%q) = %v, want %v",
				test.name,
				got,
				test.want,
			)
		}
	}
}

func TestWorkspaceTurnStopsAfterFortyEightNoProgressSamples(t *testing.T) {
	streams := make([]provider.Stream, 0, 48)
	for index := range 48 {
		streams = append(streams, toolCallStream(
			fmt.Sprintf("call-%d", index),
			"echo",
			fmt.Sprintf(`{"text":"read-%d"}`, index),
		))
	}
	runtime := &scriptedProvider{streams: streams}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&echoTool{}, nil); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, runtime, registry)
	engine.options.MaxSteps = 64

	_, err := engine.RunForTurnWithIntentAndAttachments(
		t.Context(),
		"no-progress-turn",
		"modify the workspace",
		protocol.TurnIntentWorkspaceChange,
		nil,
		func(Event) error { return nil },
	)
	if err == nil ||
		protocol.CodeOf(err) != protocol.CodeResourceExhausted ||
		!strings.Contains(err.Error(), "no structured progress for 48") {
		t.Fatalf("Run() error = %v", err)
	}
	if len(runtime.requests) != 48 {
		t.Fatalf("provider requests = %d, want 48", len(runtime.requests))
	}
	assertProgressFeedback := func(requestIndex int, stage string) {
		t.Helper()
		for _, message := range runtime.requests[requestIndex].Messages {
			if message.Role == provider.RoleUser &&
				strings.Contains(message.Text(), "[no_progress]") &&
				strings.Contains(message.Text(), "stage="+stage) {
				return
			}
		}
		t.Fatalf(
			"request %d has no %s progress feedback",
			requestIndex,
			stage,
		)
	}
	assertProgressFeedback(16, "converge")
	assertProgressFeedback(32, "finish_only")
}
