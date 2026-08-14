package engine

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextstore"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
)

func (e *Engine) projectWorldState(
	ctx context.Context,
	history []provider.Message,
	catalog tool.CatalogSnapshot,
	advertised map[string]bool,
) (
	[]provider.Message,
	[]provider.Message,
	[]promptcontext.Receipt,
	contextstore.WorldProjection,
	error,
) {
	scope := e.executionScope()
	if scope == nil {
		return nil, nil, nil, contextstore.WorldProjection{},
			errors.New("turn scope is not active")
	}
	scope.mu.Lock()
	baseline := contextstore.CloneWorldBaseline(scope.state.world)
	scope.mu.Unlock()
	valid := contextstore.WorldBaselineValid(history, baseline)
	var previous []promptcontext.Receipt
	if valid {
		for _, entry := range baseline.Entries {
			previous = append(previous, promptcontext.Receipt{
				Kind: entry.ID, Digest: entry.Digest,
			})
		}
	}
	stable, sections, receipts := frozenWorldSections(scope.spec, e.turn)
	catalogMessages, catalogReceipt := promptcontext.AssembleToolCatalog(
		promptcontext.NewToolCatalogSectionFromSnapshot(catalog, advertised),
		e.options.ToolCatalogBudget,
	)
	sections = append(
		sections,
		worldSectionFromReceipt(catalogReceipt, firstMessage(catalogMessages), e.turn),
	)
	if catalogReceipt.OriginalBytes != 0 ||
		worldBaselineHas(baseline, promptcontext.PartitionToolCatalog) {
		receipts = append(receipts, catalogReceipt)
	}
	var built promptcontext.TurnContext
	if e.options.RepoContext != nil {
		snapshot := e.evidenceSet().Snapshot(e.options.EvidenceLimit)
		built = e.options.RepoContext.Build(ctx, promptcontext.TurnState{
			Turn:             e.turn,
			WorkingSet:       e.workingLedger().Select(e.turn, e.options.WorkingSetLimit),
			Evidence:         snapshot,
			PreviousReceipts: previous,
		})
		e.options.Metrics.Evidence(len(snapshot.Risks), len(snapshot.Reminders))
	}
	sections = append(sections, worldSectionsFromTurnContext(built, e.turn)...)
	receipts = append(receipts, built.Receipts...)
	e.planMu.Lock()
	plan := e.planText
	var planReceipt *promptcontext.Receipt
	if e.planReceipt != nil {
		copy := *e.planReceipt
		planReceipt = &copy
	}
	e.planMu.Unlock()
	if planReceipt != nil {
		if promptcontext.SectionDigestMap(previous)[promptcontext.PartitionPlan] ==
			planReceipt.Digest {
			planReceipt.RetainedBytes, planReceipt.RetainedTokens = 0, 0
		} else {
			message := provider.TextMessage(provider.RoleSystem, plan)
			message.Turn = e.turn
			sections = append(
				sections,
				worldSectionFromReceipt(*planReceipt, &message, e.turn),
			)
		}
		if planReceipt.RetainedBytes == 0 {
			sections = append(
				sections,
				worldSectionFromReceipt(*planReceipt, nil, e.turn),
			)
		}
		receipts = append(receipts, *planReceipt)
	}
	projection, err := contextstore.ProjectWorld(sections, baseline, history)
	if err != nil {
		return nil, nil, nil, contextstore.WorldProjection{}, err
	}
	scope.mu.Lock()
	scope.state.selections = cloneSelections(built.Selections)
	scope.state.contextSeen = append([]promptcontext.Receipt(nil), receipts...)
	scope.state.world = contextstore.CloneWorldBaseline(projection.Baseline)
	scope.mu.Unlock()
	return stable, cloneMessages(projection.Messages), receipts, projection, nil
}

func frozenWorldSections(
	spec TurnSpec,
	turn uint64,
) ([]provider.Message, []contextstore.WorldSection, []promptcontext.Receipt) {
	var stable []provider.Message
	var sections []contextstore.WorldSection
	var receipts []promptcontext.Receipt
	for _, message := range spec.Context.Messages {
		text := strings.TrimSpace(message.Text())
		if strings.HasPrefix(text, "Policy snapshot:") {
			continue
		}
		fragment, marked := promptcontext.MatchFragment(text)
		if marked && fragment == promptcontext.FragmentSkills {
			continue
		}
		stable = append(stable, cloneMessages([]provider.Message{message})...)
	}
	policy := promptcontext.NewPolicySection(spec.Policy)
	body := policy.Render()
	message := provider.TextMessage(provider.RoleSystem, body)
	message.Turn = turn
	sections = append(sections, contextstore.WorldSection{
		ID: policy.ID(), Digest: policy.Digest(),
		Present: true, Message: &message,
	})
	receipts = append(receipts, promptcontext.Receipt{
		Kind:          policy.ID(),
		SourcePath:    "worldstate://" + policy.ID(),
		OriginalBytes: len(body),
		RetainedBytes: len(body),
		Digest:        policy.Digest(),
	})
	if body := renderSkillWorld(spec.Skills); body != "" {
		skillMessage := provider.TextMessage(
			provider.RoleSystem,
			promptcontext.WrapFragment(promptcontext.FragmentSkills, body),
		)
		skillMessage.Turn = turn
		digest := provider.MessageContentDigest(skillMessage)
		sections = append(sections, contextstore.WorldSection{
			ID: promptcontext.PartitionSkills, Digest: digest,
			Present: true, Message: &skillMessage,
		})
		receipts = append(receipts, promptcontext.Receipt{
			Kind:          promptcontext.PartitionSkills,
			SourcePath:    "worldstate://" + promptcontext.PartitionSkills,
			OriginalBytes: len(skillMessage.Text()),
			RetainedBytes: len(skillMessage.Text()), Digest: digest,
		})
	}
	return stable, sections, receipts
}

func renderSkillWorld(values []SkillSummary) string {
	if len(values) == 0 {
		return ""
	}
	values = append([]SkillSummary(nil), values...)
	sort.Slice(values, func(i, j int) bool {
		if values[i].Name != values[j].Name {
			return values[i].Name < values[j].Name
		}
		if values[i].Source != values[j].Source {
			return values[i].Source < values[j].Source
		}
		return values[i].Path < values[j].Path
	})
	var builder strings.Builder
	builder.WriteString(
		"Available skills (metadata only). Call load_skill with a name before following its instructions.\n",
	)
	for _, value := range values {
		builder.WriteString("- name=")
		builder.WriteString(strconv.Quote(value.Name))
		builder.WriteString(" description=")
		builder.WriteString(strconv.Quote(value.Description))
		builder.WriteString(" source=")
		builder.WriteString(strconv.Quote(value.Source))
		builder.WriteString(" path=")
		builder.WriteString(strconv.Quote(value.Path))
		if value.Plugin != "" {
			builder.WriteString(" plugin=")
			builder.WriteString(strconv.Quote(value.Plugin))
		}
		builder.WriteByte('\n')
	}
	return strings.TrimSuffix(builder.String(), "\n")
}

func worldSectionsFromTurnContext(
	built promptcontext.TurnContext,
	turn uint64,
) []contextstore.WorldSection {
	var result []contextstore.WorldSection
	messageIndex := 0
	for _, receipt := range built.Receipts {
		var message *provider.Message
		if receipt.RetainedBytes != 0 && messageIndex < len(built.Messages) {
			copy := cloneMessages([]provider.Message{built.Messages[messageIndex]})[0]
			messageIndex++
			message = &copy
		}
		result = append(result, worldSectionFromReceipt(receipt, message, turn))
	}
	return result
}

func worldSectionFromReceipt(
	receipt promptcontext.Receipt,
	message *provider.Message,
	turn uint64,
) contextstore.WorldSection {
	if message != nil {
		message.Turn = turn
	}
	return contextstore.WorldSection{
		ID: receipt.Kind, Digest: receipt.Digest,
		Present: receipt.OriginalBytes != 0, Message: message,
	}
}

func firstMessage(messages []provider.Message) *provider.Message {
	if len(messages) == 0 {
		return nil
	}
	message := cloneMessages(messages[:1])[0]
	return &message
}

func worldBaselineHas(
	baseline contextstore.WorldBaseline,
	id string,
) bool {
	for _, entry := range baseline.Entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}
