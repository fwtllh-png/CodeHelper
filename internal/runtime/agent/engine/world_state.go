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
	sections, receipts := e.frozenWorldSections(scope.spec, e.turn)
	catalogMessages, catalogReceipt := promptcontext.AssembleToolCatalog(
		promptcontext.NewToolCatalogSectionFromSnapshot(catalog, advertised),
		e.contextBudget(promptcontext.PartitionToolCatalog),
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
			Turn:       e.turn,
			WorkingSet: e.workingLedger().Select(e.turn, e.options.WorkingSetLimit),
			Evidence:   snapshot,
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
		message := provider.TextMessage(provider.RoleSystem, plan)
		sections = append(
			sections,
			worldSectionFromReceipt(*planReceipt, &message, e.turn),
		)
		receipts = append(receipts, *planReceipt)
	}
	projection, err := contextstore.ProjectWorld(sections, baseline, history)
	if err != nil {
		return nil, nil, nil, contextstore.WorldProjection{}, err
	}
	receipts = projectWorldReceipts(receipts, projection.Changed)
	scope.mu.Lock()
	scope.state.selections = cloneSelections(built.Selections)
	scope.state.contextSeen = append([]promptcontext.Receipt(nil), receipts...)
	scope.state.world = contextstore.CloneWorldBaseline(projection.Baseline)
	scope.mu.Unlock()
	return e.promptMessages(), cloneMessages(projection.Messages), receipts, projection, nil
}

func projectWorldReceipts(
	receipts []promptcontext.Receipt,
	changed []string,
) []promptcontext.Receipt {
	changedSet := make(map[string]struct{}, len(changed))
	for _, id := range changed {
		changedSet[id] = struct{}{}
	}
	result := append([]promptcontext.Receipt(nil), receipts...)
	for index := range result {
		if _, changed := changedSet[result[index].Kind]; changed {
			continue
		}
		result[index].RetainedBytes = 0
		result[index].RetainedTokens = 0
	}
	return result
}

func (e *Engine) frozenWorldSections(
	spec TurnSpec,
	turn uint64,
) ([]contextstore.WorldSection, []promptcontext.Receipt) {
	var sections []contextstore.WorldSection
	var receipts []promptcontext.Receipt
	appendSection := func(id, source, body, digest string) {
		messages, receipt := promptcontext.AssembleWorldText(
			id,
			source,
			body,
			e.contextBudget(id),
		)
		if digest != "" {
			receipt.Digest = digest
		}
		sections = append(
			sections,
			worldSectionFromReceipt(receipt, firstMessage(messages), turn),
		)
		receipts = append(receipts, receipt)
	}
	appendSection(
		promptcontext.PartitionMode,
		"session://profile.mode",
		promptcontext.ModeInstructionPack(string(spec.Mode)),
		"",
	)
	policy := promptcontext.NewPolicySection(spec.Policy)
	appendSection(
		policy.ID(),
		"worldstate://"+policy.ID(),
		policy.Render(),
		policy.Digest(),
	)
	if e.options.CodingPolicy {
		coding := promptcontext.NewCodingPolicySection()
		appendSection(
			coding.ID(),
			"worldstate://"+coding.ID(),
			coding.Render(),
			coding.Digest(),
		)
	}
	if body := renderSkillWorld(spec.Skills); body != "" {
		appendSection(
			promptcontext.PartitionSkills,
			"skill://catalog",
			body,
			"",
		)
	}
	return sections, receipts
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
		"Selected skills (metadata only). Use load_skill by name or skills_read with any exact advertised handle (handle, package, or resource) before following instructions. Use skills_list for more.\n",
	)
	for _, value := range values {
		builder.WriteString("- name=")
		builder.WriteString(strconv.Quote(value.Name))
		builder.WriteString(" description=")
		description := strings.TrimSpace(value.Description)
		runes := []rune(description)
		if len(runes) > 160 {
			description = string(runes[:157]) + "..."
		}
		builder.WriteString(strconv.Quote(description))
		builder.WriteString(" source=")
		builder.WriteString(strconv.Quote(value.Source))
		builder.WriteString(" handle=")
		builder.WriteString(strconv.Quote(value.Handle))
		builder.WriteString(" package=")
		builder.WriteString(strconv.Quote(value.PackageHandle))
		builder.WriteString(" resource=")
		builder.WriteString(strconv.Quote(value.ResourceHandle))
		builder.WriteByte('\n')
	}
	return strings.TrimSuffix(builder.String(), "\n")
}

func (e *Engine) contextBudget(kind string) promptcontext.Budget {
	if budget, ok := e.options.ContextBudgets[kind]; ok {
		return budget
	}
	switch kind {
	case promptcontext.PartitionMode:
		return promptcontext.Budget{MaxBytes: 1 << 10, MaxTokens: 256}
	case promptcontext.PartitionPolicy,
		promptcontext.PartitionCodingPolicy:
		return promptcontext.Budget{MaxBytes: 2 << 10, MaxTokens: 512}
	case promptcontext.PartitionSkills:
		return promptcontext.Budget{
			MaxBytes:  promptcontext.MaxSkillsPromptBytes,
			MaxTokens: promptcontext.MaxFragmentTokens,
		}
	case promptcontext.PartitionToolCatalog:
		return promptcontext.Budget{MaxBytes: 16 << 10, MaxTokens: 4 << 10}
	default:
		return promptcontext.Budget{}
	}
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
