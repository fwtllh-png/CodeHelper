package engine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	completiontool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/completion"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
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
		{name: "exec_command", capability: tool.CapabilityProcess, want: true},
		{name: "write_stdin", capability: tool.CapabilityProcess, want: true},
		{name: "request_user_input", capability: tool.CapabilityRead, want: true},
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

func TestWorkspaceTurnFinalizesAfterNoProgressBudget(t *testing.T) {
	streams := make([]provider.Stream, 0, 49)
	for index := range 48 {
		streams = append(streams, toolCallStream(
			fmt.Sprintf("call-%d", index),
			"echo",
			fmt.Sprintf(`{"text":"read-%d"}`, index),
		))
	}
	streams = append(streams, toolCallStream(
		"incomplete-1",
		completiontool.Name,
		`{"status":"incomplete","summary":"No workspace change was produced.","pending_actions":["Apply the requested workspace change."]}`,
	))
	runtime := &scriptedProvider{streams: streams}
	registry := tool.NewRegistry(nil, nil)
	for _, executor := range []tool.Executor{
		&echoTool{}, &completiontool.Tool{},
	} {
		if err := registry.Register(executor, nil); err != nil {
			t.Fatal(err)
		}
	}
	engine := newEngine(t, runtime, registry)
	engine.options.MaxSteps = 64

	var terminal Event
	_, err := engine.RunForTurnWithIntentAndAttachments(
		t.Context(),
		"no-progress-turn",
		"modify the workspace",
		protocol.TurnIntentWorkspaceChange,
		nil,
		func(event Event) error {
			if event.State == Failed {
				terminal = event
			}
			return nil
		},
	)
	if err == nil ||
		protocol.CodeOf(err) != protocol.CodeConflict ||
		terminal.Convergence == nil ||
		terminal.Convergence.Cause != string(turnkernel.ConvergenceNoProgress) {
		t.Fatalf("Run() error = %v", err)
	}
	if len(runtime.requests) != 49 {
		t.Fatalf("provider requests = %d, want 49", len(runtime.requests))
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

func TestReadOnlyTurnStopsAfterSixteenTotalSamples(t *testing.T) {
	streams := make([]provider.Stream, 0, 13)
	for index := range 12 {
		streams = append(streams, toolCallStream(
			fmt.Sprintf("call-%d", index),
			"echo",
			fmt.Sprintf(`{"text":"read-%d"}`, index),
		))
	}
	streams = append(streams, textStream("bounded final answer"))
	runtime := &scriptedProvider{streams: streams}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&echoTool{}, nil); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, runtime, registry)
	engine.options.MaxSteps = 64

	result, err := engine.RunForTurnWithIntentAndAttachments(
		t.Context(),
		"bounded-read-only",
		"analyze the repository",
		protocol.TurnIntentAnswer,
		nil,
		func(Event) error { return nil },
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Text != "bounded final answer" || len(runtime.requests) != 13 {
		t.Fatalf("result=%+v requests=%d", result, len(runtime.requests))
	}
	if len(runtime.requests[12].Tools) != 0 {
		t.Fatalf("finish-only request exposed tools: %+v", runtime.requests[12].Tools)
	}
}

func TestReadOnlyFinishOnlyCompletesCurrentProcess(t *testing.T) {
	streams := make([]provider.Stream, 0, 14)
	for index := range 12 {
		streams = append(streams, toolCallStream(
			fmt.Sprintf("read-%d", index),
			"echo",
			fmt.Sprintf(`{"text":"read-%d"}`, index),
		))
	}
	streams = append(
		streams,
		toolCallStream("exec", "exec_command", `{}`),
		textStream("process completed"),
	)
	runtime := &scriptedProvider{streams: streams}
	registry := tool.NewRegistry(nil, nil)
	for _, executor := range []tool.Executor{&echoTool{}, finishProcessTool{}} {
		if err := registry.Register(executor, nil); err != nil {
			t.Fatal(err)
		}
	}
	engine := newEngine(t, runtime, registry)
	engine.options.MaxSteps = 64

	result, err := engine.RunForTurnWithIntentAndAttachments(
		t.Context(),
		"finish-current-process",
		"analyze then complete the configured command",
		protocol.TurnIntentAnswer,
		nil,
		func(Event) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "process completed" || len(runtime.requests) != 14 {
		t.Fatalf("result=%+v requests=%d", result, len(runtime.requests))
	}
	names := make(map[string]bool)
	for _, definition := range runtime.requests[12].Tools {
		names[definition.Name] = true
	}
	if !names["exec_command"] || names["echo"] {
		t.Fatalf("finish-only tools = %+v", names)
	}
}

func TestAcceptedCompletionPublishesSummaryWithoutFinalAnswerSampleAtLimit(
	t *testing.T,
) {
	streams := make([]provider.Stream, 0, 16)
	for index := range 11 {
		streams = append(streams, toolCallStream(
			fmt.Sprintf("read-%d", index),
			"echo",
			fmt.Sprintf(`{"text":"read-%d"}`, index),
		))
	}
	for index := range 4 {
		streams = append(streams, toolCallStream(
			fmt.Sprintf("quality-%d", index),
			"quality_verify",
			`{"covered_paths":["a.go"]}`,
		))
	}
	streams = append(
		streams,
		toolCallStream("complete", "turn_complete", `{
			"status":"complete",
			"summary":"analysis complete",
			"pending_actions":[]
		}`),
	)
	runtime := &scriptedProvider{streams: streams}
	registry := declarationRegistry(t, true)
	if err := registry.Register(&echoTool{}, nil); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, runtime, registry)
	engine.options.MaxSteps = 64

	result, err := engine.RunForTurnWithIntentAndAttachments(
		t.Context(),
		"bounded-completion",
		"analyze the repository",
		protocol.TurnIntentAnswer,
		nil,
		func(Event) error { return nil },
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Text != "analysis complete" || len(runtime.requests) != 16 {
		t.Fatalf("result=%+v requests=%d", result, len(runtime.requests))
	}
}
