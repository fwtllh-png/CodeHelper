package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type progressObservation struct {
	stage             turnkernel.ProgressStage
	observedSamples   uint32
	noProgressSamples uint32
	readOnlyResearch  bool
	stageChanged      bool
}

func (e *Engine) progressSignature(kernel *engineTurnKernel) string {
	e.planMu.Lock()
	done := 0
	for _, step := range e.plan.Steps {
		if step.Done() {
			done++
		}
	}
	e.planMu.Unlock()

	evidenceDigest := ""
	if kernel.intent() == protocol.TurnIntentAnswer ||
		kernel.intent() == protocol.TurnIntentPlan {
		snapshot := e.EvidenceSnapshot()
		keys := make([]string, 0, len(snapshot.Facts))
		for _, fact := range snapshot.Facts {
			keys = append(keys, fmt.Sprintf(
				"%s\x00%s\x00%d\x00%s",
				fact.Kind,
				fact.Path,
				fact.Line,
				fact.Symbol,
			))
		}
		for _, path := range e.workingLedger().PathsObservedAt(
			workingset.SourceRead,
			e.turn,
		) {
			keys = append(keys, "read\x00"+path)
		}
		sort.Strings(keys)
		sum := sha256.Sum256([]byte(strings.Join(keys, "\n")))
		evidenceDigest = hex.EncodeToString(sum[:])
	}
	return kernel.progressSignature(done, evidenceDigest)
}

func noProgressFeedback(
	turn uint64,
	observation progressObservation,
) provider.Message {
	message := provider.TextMessage(
		provider.RoleUser,
		fmt.Sprintf(
			"[no_progress]\n"+
				"steps_without_structured_progress=%d\n"+
				"stage=%s\n"+
				"required_action=converge\n"+
				"Stop broad exploration and repeated inventory. Execute the smallest "+
				"coherent batch now (at most 5 files), verify it, and update the plan. "+
				"A workspace-change turn advances only through observed mutations, "+
				"completed plan steps, verification, or completion. If the remaining "+
				"work cannot be completed, call turn_complete with status=incomplete "+
				"and concrete pending_actions.",
			observation.noProgressSamples,
			observation.stage,
		),
	)
	message.Turn = turn
	return message
}

func withFinishOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, finishOnlyContextKey{}, true)
}

type finishOnlyContextKey struct{}

func finishOnlyEnabled(ctx context.Context) bool {
	enabled, _ := ctx.Value(finishOnlyContextKey{}).(bool)
	return enabled
}

func finishOnlyToolAllowed(name string, descriptor tool.Descriptor) bool {
	if descriptor.Capability == tool.CapabilityWrite {
		return true
	}
	switch name {
	case "turn_complete",
		"update_plan",
		"request_user_input",
		"file_read",
		"exec_command",
		"write_stdin",
		"git_diff",
		"git_status",
		"quality_test",
		"quality_diagnostics",
		"quality_review",
		"quality_verify":
		return true
	default:
		return false
	}
}

func convergenceDefinitionAllowed(definition provider.ToolDefinition) bool {
	switch definition.Name {
	case "turn_complete", "request_user_input":
		return true
	default:
		return false
	}
}

func finishOnlyDefinitionAllowed(
	catalog tool.CatalogSnapshot,
	definition provider.ToolDefinition,
) bool {
	entry, ok := catalog.Lookup(definition.Name)
	return ok && finishOnlyToolAllowed(definition.Name, entry.Descriptor)
}
