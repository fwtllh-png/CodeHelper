package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextstore"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func TestWorldStatePersistsAcrossTurnsAndEmitsOnlyChanges(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		textStream("one"), textStream("two"), textStream("three"),
	}}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	engine.options.RepoContext = &stubRepoContext{}

	if _, err := engine.Run(t.Context(), "first", nil); err != nil {
		t.Fatal(err)
	}
	if !contextstore.WorldBaselineValid(engine.History(), engine.world) ||
		engine.world.Revision != 1 {
		t.Fatalf("first baseline=%+v history=%+v", engine.world, engine.History())
	}
	if _, err := engine.Run(t.Context(), "second", nil); err != nil {
		t.Fatal(err)
	}
	second := runtime.requests[1].Messages
	if countWorldSection(second, "policy") != 1 ||
		countWorldMode(second, "patch") != 0 ||
		engine.world.Revision != 1 {
		t.Fatalf("unchanged request=%+v baseline=%+v", second, engine.world)
	}

	engine.observePath(workingset.SourceRead, "found.go")
	if _, err := engine.Run(t.Context(), "third", nil); err != nil {
		t.Fatal(err)
	}
	third := runtime.requests[2].Messages
	if countWorldSection(third, "working_set_ledger") != 2 ||
		countWorldMode(third, "patch") != 1 ||
		engine.world.Revision != 2 {
		t.Fatalf("patched request=%+v baseline=%+v", third, engine.world)
	}
}

func TestWorldBaselineSurvivesSessionDeltaRestart(t *testing.T) {
	sourceRuntime := &scriptedProvider{streams: []provider.Stream{textStream("one")}}
	source := newEngine(t, sourceRuntime, tool.NewRegistry(nil, nil))
	source.options.RepoContext = &stubRepoContext{}
	if _, err := source.Run(t.Context(), "first", nil); err != nil {
		t.Fatal(err)
	}
	fork := source.Fork()
	fork.ReplaceHistory(source.History())
	if !contextstore.WorldBaselineValid(fork.History(), fork.world) {
		t.Fatalf("fork baseline=%+v history=%+v", fork.world, fork.History())
	}
	delta, err := prepareSessionDelta(
		"turn-1", 0, source.History(), provider.Usage{}, 0,
		SessionStateDelta{Turn: source.turn, World: source.world},
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(delta)
	if err != nil {
		t.Fatal(err)
	}

	targetRuntime := &scriptedProvider{streams: []provider.Stream{textStream("two")}}
	target := newEngine(t, targetRuntime, tool.NewRegistry(nil, nil))
	target.options.RepoContext = &stubRepoContext{}
	if err := target.RestoreSessionDelta(raw); err != nil {
		t.Fatal(err)
	}
	if !contextstore.WorldBaselineValid(target.History(), target.world) {
		t.Fatalf("restored baseline=%+v history=%+v", target.world, target.History())
	}
	if _, err := target.Run(t.Context(), "second", nil); err != nil {
		t.Fatal(err)
	}
	request := targetRuntime.requests[0].Messages
	if countWorldSection(request, "policy") != 1 ||
		countWorldMode(request, "patch") != 0 {
		t.Fatalf("restart reinjected unchanged world state: %+v", request)
	}
}

func TestHistoryReplacementAndCompactionInvalidateWorldBaseline(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		textStream("one"), textStream("two"),
	}}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	if _, err := engine.Run(t.Context(), "first", nil); err != nil {
		t.Fatal(err)
	}
	engine.ReplaceHistory([]provider.Message{
		provider.TextMessage(provider.RoleUser, "replacement"),
	})
	if engine.world.Revision != 0 {
		t.Fatalf("replacement retained stale baseline=%+v", engine.world)
	}
	if _, err := engine.Run(t.Context(), "second", nil); err != nil {
		t.Fatal(err)
	}
	if countWorldMode(runtime.requests[1].Messages, "full") != 3 {
		t.Fatalf("replacement did not force full state: %+v", runtime.requests[1].Messages)
	}

	engine.history = append(
		engine.history,
		messageWithText(provider.RoleUser, strings.Repeat("old context ", 200), 2),
		messageWithText(provider.RoleAssistant, strings.Repeat("old answer ", 200), 2),
		messageWithText(provider.RoleUser, "current", 3),
	)
	if receipt := engine.CompactForced(); receipt == nil {
		t.Fatal("forced compaction did not produce a receipt")
	}
	if engine.world.Revision != 0 {
		t.Fatalf("compaction retained stale baseline=%+v", engine.world)
	}
	for _, message := range engine.History() {
		if _, _, ok := contextstore.InspectWorldMessage(message); ok {
			t.Fatalf("compaction retained world fragment: %+v", engine.History())
		}
	}
}

func TestPolicyAndSkillsChangesProduceTypedPatches(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		textStream("one"), textStream("two"), textStream("three"),
	}}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	skills := []SkillSummary{{
		Name: "review", Description: "review code", Source: "builtin",
		Path: "skills/review/SKILL.md",
	}}
	engine.options.TurnSnapshots.Skills = func() []SkillSummary {
		return append([]SkillSummary(nil), skills...)
	}
	if _, err := engine.Run(t.Context(), "first", nil); err != nil {
		t.Fatal(err)
	}

	engine.SetPermission(policy.PermissionAuto)
	if _, err := engine.Run(t.Context(), "second", nil); err != nil {
		t.Fatal(err)
	}
	second := runtime.requests[1].Messages
	if countWorldSection(second, "policy") != 2 ||
		countWorldSection(second, "skills") != 1 ||
		countWorldMode(second, "patch") != 1 {
		t.Fatalf("policy patch request=%+v", second)
	}

	skills = append(skills, SkillSummary{
		Name: "test", Description: "run tests", Source: "builtin",
		Path: "skills/test/SKILL.md",
	})
	if _, err := engine.Run(t.Context(), "third", nil); err != nil {
		t.Fatal(err)
	}
	third := runtime.requests[2].Messages
	if countWorldSection(third, "skills") != 2 ||
		countWorldMode(third, "patch") != 2 {
		t.Fatalf("skills patch request=%+v", third)
	}
}

func TestToolCatalogChangeProducesTypedPatch(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		textStream("one"), textStream("two"),
	}}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&echoTool{}, nil); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, runtime, registry)
	if _, err := engine.Run(t.Context(), "first", nil); err != nil {
		t.Fatal(err)
	}
	descriptor := echoDescriptor()
	descriptor.Name = "lookup"
	descriptor.Description = "lookup a value"
	if err := registry.Register(&countingCatalogExecutor{descriptor: descriptor}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(t.Context(), "second", nil); err != nil {
		t.Fatal(err)
	}
	request := runtime.requests[1].Messages
	if countWorldSection(request, "tool_catalog") != 2 ||
		countWorldMode(request, "patch") != 1 {
		t.Fatalf("catalog patch request=%+v", request)
	}
}

func TestSkillWorldUsesSingleBudgetedAuthority(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.ContextBudgets = map[string]promptcontext.Budget{
		promptcontext.PartitionSkills: {MaxBytes: 256, MaxTokens: 128},
	}
	spec := TurnSpec{
		Mode: policy.ModeAct,
		Policy: policy.DefaultRuntime(
			policy.ModeAct,
			policy.PermissionBypass,
		),
		Skills: []SkillSummary{{
			Name: "large", Description: strings.Repeat("description ", 100),
			Source: "workspace", Path: "skills/large/SKILL.md",
		}},
	}
	sections, receipts := engine.frozenWorldSections(spec, 1)
	var skill *contextstore.WorldSection
	for index := range sections {
		if sections[index].ID == promptcontext.PartitionSkills {
			skill = &sections[index]
			break
		}
	}
	if skill == nil || skill.Message == nil ||
		len(skill.Message.Text()) > 256 {
		t.Fatalf("skill section=%+v", skill)
	}
	var receipt *promptcontext.Receipt
	for index := range receipts {
		if receipts[index].Kind == promptcontext.PartitionSkills {
			receipt = &receipts[index]
			break
		}
	}
	if receipt == nil || !receipt.Truncated ||
		receipt.TruncationReason == "" {
		t.Fatalf("skill receipt=%+v", receipt)
	}
}

func TestSkillWorldOmitsInternalPathsAndBoundsDescriptions(t *testing.T) {
	rendered := renderSkillWorld([]SkillSummary{{
		Name: "review", Description: strings.Repeat("界", 200),
		Source: "workspace", Path: "/private/skills/review/SKILL.md",
		Plugin: "private-plugin", Handle: "skh_handle",
		PackageHandle: "skp_package", ResourceHandle: "skr_resource",
	}})
	if strings.Contains(rendered, "/private/") ||
		strings.Contains(rendered, "private-plugin") ||
		!strings.Contains(rendered, "...") ||
		!strings.Contains(rendered, "any exact advertised handle") ||
		!strings.Contains(rendered, `handle="skh_handle"`) ||
		!strings.Contains(rendered, `package="skp_package"`) ||
		!strings.Contains(rendered, `resource="skr_resource"`) {
		t.Fatalf("rendered skills=%q", rendered)
	}
}

func TestWorldReceiptsReflectOnlyProjectedChanges(t *testing.T) {
	receipts := projectWorldReceipts([]promptcontext.Receipt{
		{
			Kind:          promptcontext.PartitionMode,
			RetainedBytes: 10, RetainedTokens: 3,
		},
		{
			Kind:          promptcontext.PartitionPolicy,
			RetainedBytes: 20, RetainedTokens: 5,
		},
	}, []string{promptcontext.PartitionPolicy})
	if receipts[0].RetainedBytes != 0 ||
		receipts[0].RetainedTokens != 0 ||
		receipts[1].RetainedBytes != 20 ||
		receipts[1].RetainedTokens != 5 {
		t.Fatalf("projected receipts=%+v", receipts)
	}
}

func countWorldSection(messages []provider.Message, id string) int {
	count := 0
	for _, message := range messages {
		entry, _, ok := contextstore.InspectWorldMessage(message)
		if ok && entry.ID == id {
			count++
		}
	}
	return count
}

func countWorldMode(messages []provider.Message, mode string) int {
	count := 0
	for _, message := range messages {
		_, value, ok := contextstore.InspectWorldMessage(message)
		if ok && value == contextstore.WorldMode(mode) {
			count++
		}
	}
	return count
}
