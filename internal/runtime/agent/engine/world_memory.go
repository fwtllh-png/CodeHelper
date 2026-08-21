package engine

import (
	"sort"
	"strconv"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextstore"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
)

func (e *Engine) memoryWorldSection(
	spec TurnSpec,
	turn uint64,
) (contextstore.WorldSection, promptcontext.Receipt, bool) {
	if strings.TrimSpace(spec.Memory.Body) == "" {
		if spec.Memory.FailureReason != "" {
			return contextstore.WorldSection{}, promptcontext.Receipt{
				Kind:             promptcontext.PartitionUserMemory,
				SourcePath:       spec.Memory.Source,
				Truncated:        true,
				TruncationReason: spec.Memory.FailureReason,
				Generation:       spec.Memory.Generation,
			}, true
		}
		return contextstore.WorldSection{}, promptcontext.Receipt{}, false
	}
	messages, receipt := promptcontext.AssembleWorldText(
		promptcontext.PartitionUserMemory,
		spec.Memory.Source,
		spec.Memory.Body,
		e.contextBudget(promptcontext.PartitionUserMemory),
	)
	if spec.Memory.Digest != "" {
		receipt.Digest = spec.Memory.Digest
	}
	receipt.Generation = spec.Memory.Generation
	receipt.CandidateCount = spec.Memory.CandidateCount
	receipt.SelectedIDs = append([]string(nil), spec.Memory.SelectedIDs...)
	receipt.Truncated = receipt.Truncated || spec.Memory.Truncated
	return worldSectionFromReceipt(
		receipt,
		firstMessage(messages),
		turn,
	), receipt, true
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
