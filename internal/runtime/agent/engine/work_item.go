package engine

import (
	"encoding/json"
	"strings"

	agentcontext "github.com/fwtllh-png/QCode/internal/runtime/agent/context"
	"github.com/fwtllh-png/QCode/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func (e *Engine) workItemGoal(spec TurnSpec) string {
	prompt := strings.TrimSpace(spec.Request.Prompt)
	if spec.Request.Recovery != nil &&
		spec.Request.Recovery.Action == protocol.TurnRecoveryContinue {
		if goal := recoveryWorkItemGoal(prompt); goal != "" {
			return goal
		}
	}
	return prompt
}

func (e *Engine) continueWorkItemSeed(spec TurnSpec) turnkernel.WorkItem {
	if spec.Request.Recovery == nil ||
		spec.Request.Recovery.Action != protocol.TurnRecoveryContinue {
		return turnkernel.WorkItem{}
	}
	item := turnkernel.WorkItem{
		KnownReads: make(map[string]turnkernel.WorkItemRead),
		KnownEdits: make(map[string]turnkernel.WorkItemEdit),
	}
	reads, edits := parseRecoveryWorkItem(spec.Request.Prompt)
	if e.workingLedger() != nil {
		reads = append(reads, e.workingLedger().PathsWithSource(agentcontext.SourceRead)...)
		edits = append(edits, e.workingLedger().PathsWithSource(agentcontext.SourceEdited)...)
	}
	for _, path := range reads {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if relative, ok := agentcontext.WorkspaceRelative(e.options.Workspace, path); ok {
			path = relative
		}
		if _, exists := item.KnownReads[path]; exists {
			continue
		}
		item.KnownReads[path] = turnkernel.WorkItemRead{
			Window: "full",
			Turn:   e.turn,
		}
	}
	for _, path := range edits {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if relative, ok := agentcontext.WorkspaceRelative(e.options.Workspace, path); ok {
			path = relative
		}
		if _, exists := item.KnownEdits[path]; exists {
			continue
		}
		item.KnownEdits[path] = turnkernel.WorkItemEdit{}
	}
	return item
}

func recoveryWorkItemGoal(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}
	if goal, _, ok := strings.Cut(prompt, "\n\n<recovery_evidence>"); ok {
		if goal = strings.TrimSpace(goal); goal != "" {
			return goal
		}
	}
	return ""
}

func parseRecoveryWorkItem(prompt string) (reads []string, edits []string) {
	_, body, ok := strings.Cut(prompt, "<recovery_evidence>")
	if !ok {
		return nil, nil
	}
	section, _, ok := strings.Cut(body, "</recovery_evidence>")
	if !ok {
		return nil, nil
	}
	var capsule struct {
		WorkItem *struct {
			KnownReads []string `json:"known_reads"`
			KnownEdits []string `json:"known_edits"`
		} `json:"work_item"`
		Receipt *struct {
			ReadPaths []string `json:"read_paths"`
			Changes   []struct {
				Path string `json:"path"`
			} `json:"changes"`
		} `json:"receipt"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(section)), &capsule); err != nil {
		return nil, nil
	}
	if capsule.WorkItem != nil {
		return append([]string(nil), capsule.WorkItem.KnownReads...),
			append([]string(nil), capsule.WorkItem.KnownEdits...)
	}
	if capsule.Receipt != nil {
		reads = append(reads, capsule.Receipt.ReadPaths...)
		for _, change := range capsule.Receipt.Changes {
			edits = append(edits, change.Path)
		}
	}
	return reads, edits
}
