package engine

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
	promptcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/prompt"
)

func TestSessionStatePartitionSurvivesProjectedTailWithoutCompact(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		textStream("one"), textStream("two"), textStream("three"),
	}}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	engine.options.Workspace = t.TempDir()
	engine.turn = 1
	engine.context.Evidence().BeginTurn(1)
	if err := engine.ApplyPlan(interact.Plan{
		Objective: "teach the parser about trailing commas",
		Steps: []interact.PlanStep{{
			Title: "update the lexer", Status: interact.StepInProgress,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	engine.observeChangeEvidence(tool.WorkspaceChange{
		Path: "parser/lex.go", Kind: tool.WorkspaceModified,
	})

	var compacted int
	for _, prompt := range []string{"first", "second", "third"} {
		if _, err := engine.Run(t.Context(), prompt, func(event Event) error {
			if event.State == Compacting {
				compacted++
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if compacted != 0 {
		t.Fatalf("compact events = %d, want none", compacted)
	}
	if len(runtime.requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(runtime.requests))
	}
	last := runtime.requests[2].Messages
	joined := joinMessageText(last)
	if !strings.Contains(joined, "teach the parser about trailing commas") ||
		!strings.Contains(joined, "update the lexer") ||
		!strings.Contains(joined, "parser/lex.go") ||
		!strings.Contains(joined, agentcontext.TruthMarkerStart) {
		t.Fatalf("projected sample lost session state: %s", joined)
	}
	if countWorldSection(engine.History(), promptcontext.PartitionSessionState) == 0 {
		t.Fatal("session state never entered durable world history")
	}
}

func TestSessionStatePartitionStaysAbsentWithoutLedgerFacts(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{textStream("done")}}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	if _, err := engine.Run(t.Context(), "hello", nil); err != nil {
		t.Fatal(err)
	}
	if countWorldSection(engine.History(), promptcontext.PartitionSessionState) != 0 {
		t.Fatalf("empty ledgers still projected session state: %+v", engine.History())
	}
}

func joinMessageText(messages []provider.Message) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		if text := message.Text(); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}
