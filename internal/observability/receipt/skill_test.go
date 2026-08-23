package receipt

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	skillruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
)

func TestReceiptRecordsResolvedSkillIdentity(t *testing.T) {
	recorder := New("review")
	recorder.Observe(agentengine.Event{
		State: agentengine.RunningTools,
		ToolCall: &provider.ToolCall{
			ID: "call-1", Name: "load_skill", Arguments: `{"name":"review"}`,
		},
		Result: &tool.Result{
			Content: "instructions",
			Metadata: map[string]any{
				"resolved_skills": []skillruntime.ResolvedSkill{
					{
						Name: "base", Version: "2.1.0", Source: skillruntime.SourceConfigured,
						Digest: strings.Repeat("a", 64), Locked: true,
					},
					{
						Name: "review", Version: "1.0.0", Source: skillruntime.SourceConfigured,
						Digest: strings.Repeat("b", 64), Locked: true,
					},
				},
			},
		},
	})
	receipt := recorder.Build(Observations{})
	if len(receipt.Skills) != 2 || receipt.Skills[0].Name != "base" ||
		receipt.Skills[1].Version != "1.0.0" || !receipt.Skills[1].Locked {
		t.Fatalf("skills = %+v", receipt.Skills)
	}
}

func TestReceiptRecordsSkillsReadInvocation(t *testing.T) {
	recorder := New("review")
	recorder.Observe(agentengine.Event{
		State: agentengine.RunningTools,
		ToolCall: &provider.ToolCall{
			ID: "call-1", Name: "skills_read", Arguments: `{"handle":"skh"}`,
		},
		Result: &tool.Result{
			Content: "instructions",
			Metadata: map[string]any{
				"resolved_skills": []skillruntime.ResolvedSkill{{
					Name: "review", Version: "1.0.0",
					Source: skillruntime.SourcePlugin, Plugin: "fixture",
					Digest: strings.Repeat("c", 64), Locked: true,
				}},
			},
		},
	})
	receipt := recorder.Build(Observations{})
	if len(receipt.Skills) != 1 || receipt.Skills[0].Name != "review" ||
		receipt.Skills[0].Plugin != "fixture" {
		t.Fatalf("skills = %+v", receipt.Skills)
	}
}
