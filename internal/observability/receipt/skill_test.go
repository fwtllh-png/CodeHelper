package receipt

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	skillruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

func TestReceiptRecordsSkillsReadInvocation(t *testing.T) {
	recorder := New("review")
	recorder.Observe(toolResultEvent(
		provider.ToolCall{
			ID: "call-1", Name: "skills_read", Arguments: `{"handle":"skh"}`,
		},
		tool.Result{
			Content: "instructions",
			Metadata: map[string]any{
				"resolved_skills": []skillruntime.ResolvedSkill{{
					Name: "review", Version: "1.0.0",
					Source: skillruntime.SourceWorkspace,
					Digest: strings.Repeat("c", 64), Locked: true,
				}},
			},
		},
	))
	receipt := recorder.Build(Observations{})
	if len(receipt.Skills) != 1 || receipt.Skills[0].Name != "review" {
		t.Fatalf("skills = %+v", receipt.Skills)
	}
}
