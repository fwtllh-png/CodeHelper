package contextfork

import (
	"errors"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextstore"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
)

type staticEngineResolver struct {
	engine *agentengine.Engine
	err    error
}

func (r staticEngineResolver) ContextEngine(string) (*agentengine.Engine, error) {
	return r.engine, r.err
}

func TestSnapshotFailsClosedWithoutExactParentTurn(t *testing.T) {
	source := NewSource(staticEngineResolver{engine: &agentengine.Engine{}})
	if _, err := source.Snapshot(t.Context(), ContextSourceRef{
		ThreadID: "thread-parent", TurnID: "turn-parent",
	}); err == nil || !strings.Contains(err.Error(), "has no context snapshot") {
		t.Fatalf("missing parent turn error = %v", err)
	}
	source = NewSource(staticEngineResolver{err: errors.New("thread missing")})
	if _, err := source.Snapshot(t.Context(), ContextSourceRef{
		ThreadID: "thread-parent", TurnID: "turn-parent",
	}); err == nil || err.Error() != "thread missing" {
		t.Fatalf("missing parent thread error = %v", err)
	}
}

func TestProjectMessagesExcludesOpaqueParentContent(t *testing.T) {
	messages := []provider.Message{
		provider.TextMessage(provider.RoleUser, "parent goal"),
		{
			Role: provider.RoleAssistant, Turn: 1,
			Provenance: &provider.AssistantProvenance{
				Adapter: "openai", Provider: "openai", Model: "model",
				Replay: &provider.ReplayState{
					Version:       provider.ReplayVersion,
					ContentDigest: "opaque-digest",
					Data:          []byte(`{"private":"replay"}`),
				},
			},
			Blocks: []provider.ContentBlock{
				{Type: provider.ContentText, Text: "visible"},
				{Type: provider.ContentReasoning, Text: "private reasoning"},
				{Type: provider.ContentToolCall, ToolCall: &provider.ToolCall{
					ID: "call-1", Name: "file_read", Arguments: `{"path":"a.go"}`,
				}},
			},
		},
		{
			Role: provider.RoleTool, Turn: 1,
			Blocks: []provider.ContentBlock{{
				Type: provider.ContentToolResult,
				ToolResult: &provider.ToolResult{
					CallID: "call-1", Content: "file body",
				},
			}},
		},
	}
	projected := projectMessages(messages)
	if parentGoal(projected, "") != "parent goal" {
		t.Fatalf("projected = %+v", projected)
	}
	rendered := strings.Builder{}
	for _, message := range projected {
		for _, block := range message.Blocks {
			rendered.WriteString(block.Text)
			rendered.WriteString(block.Arguments)
		}
	}
	if strings.Contains(rendered.String(), "private reasoning") ||
		strings.Contains(rendered.String(), "opaque") ||
		strings.Contains(rendered.String(), "replay") ||
		!strings.Contains(rendered.String(), "visible") ||
		!strings.Contains(rendered.String(), "file body") {
		t.Fatalf("projected messages = %+v", projected)
	}
}

func TestLatestWorldTextUsesTypedMarkerAndTombstone(t *testing.T) {
	message := provider.TextMessage(provider.RoleSystem, "coding rules")
	full, err := contextstore.ProjectWorld(
		[]contextstore.WorldSection{{
			ID: "coding_policy", Digest: "digest-1",
			Present: true, Message: &message,
		}},
		contextstore.WorldBaseline{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := latestWorldText(full.Messages, "coding_policy"); got != "coding rules" {
		t.Fatalf("world text=%q", got)
	}
	history := append([]provider.Message(nil), full.Messages...)
	removed, err := contextstore.ProjectWorld(
		nil,
		full.Baseline,
		history,
	)
	if err != nil {
		t.Fatal(err)
	}
	history = append(history, removed.Messages...)
	if got := latestWorldText(history, "coding_policy"); got != "" {
		t.Fatalf("removed world text=%q", got)
	}
}
