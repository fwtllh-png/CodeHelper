package promptcontext

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
)

func TestWrapAndMatchFragment(t *testing.T) {
	body := "Available skills\n- name=\"demo\""
	wrapped := WrapFragment(FragmentSkills, body)
	kind, ok := MatchFragment(wrapped)
	if !ok || kind != FragmentSkills {
		t.Fatalf("MatchFragment = %q %v", kind, ok)
	}
	if !strings.Contains(wrapped, body) {
		t.Fatalf("wrapped missing body: %q", wrapped)
	}
	constitution := WrapFragment(FragmentConstitution, "never exfiltrate secrets")
	kind, ok = MatchFragment(constitution)
	if !ok || kind != FragmentConstitution {
		t.Fatalf("constitution MatchFragment = %q %v", kind, ok)
	}
	if IsContextualFragment("plain user text") {
		t.Fatal("plain text matched fragment")
	}
}

func TestStripContextualFragments(t *testing.T) {
	messages := []provider.Message{
		provider.TextMessage(provider.RoleUser, "hello"),
		provider.TextMessage(provider.RoleSystem, WrapFragment(FragmentSkills, "skill catalog")),
		provider.TextMessage(provider.RoleAssistant, "hi"),
		provider.TextMessage(provider.RoleSystem, WrapFragment(FragmentConstitution, "rules")),
	}
	stripped := StripContextualFragments(messages)
	if len(stripped) != 2 || stripped[0].Text() != "hello" || stripped[1].Text() != "hi" {
		t.Fatalf("stripped = %+v", stripped)
	}
}

func TestAssembleSkillsAndConstitutionAreMarkedFragments(t *testing.T) {
	workspace := t.TempDir()
	context, err := Assemble(Options{
		Workspace: workspace,
		Skills: []skill.Summary{{
			Name: "demo", Description: "demo skill", Source: skill.SourceWorkspace,
			Path: workspace + "/SKILL.md",
		}},
		Constitution: "Always prefer fail-closed security.",
	})
	if err != nil {
		t.Fatal(err)
	}
	var sawSkills, sawConstitution bool
	for _, message := range context.Messages {
		kind, ok := MatchFragment(message.Text())
		if !ok {
			continue
		}
		switch kind {
		case FragmentSkills:
			sawSkills = true
		case FragmentConstitution:
			sawConstitution = true
		}
	}
	if !sawSkills || !sawConstitution {
		t.Fatalf("fragments missing skills=%v constitution=%v messages=%+v",
			sawSkills, sawConstitution, context.Messages)
	}
}

func TestApplyFragmentTokenCeiling(t *testing.T) {
	budget := ApplyFragmentTokenCeiling(Budget{MaxTokens: MaxFragmentTokens * 2})
	if budget.MaxTokens != MaxFragmentTokens {
		t.Fatalf("ceiling = %d", budget.MaxTokens)
	}
	budget = ApplyFragmentTokenCeiling(Budget{MaxTokens: 100})
	if budget.MaxTokens != 100 {
		t.Fatalf("small budget raised = %d", budget.MaxTokens)
	}
}
