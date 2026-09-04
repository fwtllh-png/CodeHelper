package engine

import (
	"fmt"
	"strings"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	agentcontext "github.com/fwtllh-png/QCode/internal/runtime/agent/context"
	"github.com/fwtllh-png/QCode/internal/runtime/agent/turnkernel"
)

func (e *Engine) observationGate(
	call provider.ToolCall,
	finishOnly bool,
) *tool.Result {
	if call.Name == "git_status" || call.Name == "git_diff" {
		item, continueTurn := e.admissionWorkItem()
		if continueTurn || item.HasKnown() {
			return &tool.Result{
				Content: "git_status and git_diff are not admitted on a " +
					"Continue or Known Work Item. Use turn_history or " +
					"result_get for prior evidence; edit or finish the " +
					"current Work Item.",
				IsError: true,
				Metadata: map[string]any{
					"error_category":  "work_item_git_patrol_refused",
					"required_action": item.RequiredActionOr("turn_history"),
					"retry_original":  false,
				},
			}
		}
		return nil
	}
	if call.Name != "file_read" {
		return nil
	}
	path, startLine, ok := turnkernel.ParseFileReadWindow(call.Arguments)
	if !ok {
		return nil
	}
	item, _ := e.admissionWorkItem()
	read, known := e.knownWorkItemRead(item, path)
	if known && !workItemWindowIsNew(read, startLine) {
		action := item.RequiredActionOr("file_edit")
		if action == "" {
			action = "file_edit"
		}
		return &tool.Result{
			Content: fmt.Sprintf(
				"file_read of a Known Work Item path requires a new "+
					"start_line window or turn_history/result_get. "+
					"required_action=%s path=%q",
				action,
				path,
			),
			IsError: true,
			Metadata: map[string]any{
				"error_category":  "work_item_known_read_refused",
				"path":            path,
				"required_action": action,
				"retry_original":  false,
			},
		}
	}
	if startLine > 0 {
		return nil
	}
	locatedLine, located := e.locatedReadLine(path)
	if !finishOnly && !located {
		return nil
	}
	if !located {
		locatedLine = 1
	}
	return &tool.Result{
		Content: fmt.Sprintf(
			"file_read requires a bounded window. Retry with "+
				`{"path":%q,"start_line":%d}; `+
				"read the located window and edit, do not page the rest of the file.",
			path,
			locatedLine,
		),
		IsError: true,
		Metadata: map[string]any{
			"error_category":  "located_site_window_required",
			"path":            path,
			"required_action": "file_read",
			"retry_original":  false,
			"start_line":      locatedLine,
		},
	}
}

func workItemWindowIsNew(read turnkernel.WorkItemRead, startLine int) bool {
	if startLine <= 0 {
		return false
	}
	window := strings.TrimSpace(read.Window)
	if window == "" || window == "full" {
		return true
	}
	return window != fmt.Sprintf("%d", startLine)
}

func (e *Engine) admissionWorkItem() (turnkernel.WorkItem, bool) {
	kernel := e.admissionKernel
	if kernel == nil {
		if scope := e.executionScope(); scope != nil {
			scope.mu.Lock()
			kernel = scope.state.kernel
			scope.mu.Unlock()
		}
	}
	if kernel == nil {
		return turnkernel.WorkItem{}, false
	}
	return kernel.WorkItem(), kernel.RecoveryContinue()
}

func (e *Engine) knownWorkItemRead(
	item turnkernel.WorkItem,
	path string,
) (turnkernel.WorkItemRead, bool) {
	path = strings.TrimSpace(path)
	if read, ok := item.KnownRead(path); ok {
		return read, true
	}
	if relative, ok := agentcontext.WorkspaceRelative(e.options.Workspace, path); ok {
		return item.KnownRead(relative)
	}
	return turnkernel.WorkItemRead{}, false
}

func (e *Engine) locatedReadLine(path string) (int, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return 0, false
	}
	if relative, ok := agentcontext.WorkspaceRelative(e.options.Workspace, path); ok {
		path = relative
	}
	line := 0
	for _, fact := range e.EvidenceSnapshot().Facts {
		if fact.Path == path && fact.Line > 0 &&
			(line == 0 || fact.Line < line) {
			line = fact.Line
		}
	}
	return line, line > 0
}

func (e *Engine) locatedSites() []string {
	var sites []string
	seen := make(map[string]struct{})
	for _, fact := range e.EvidenceSnapshot().Facts {
		if fact.Line <= 0 || strings.TrimSpace(fact.Path) == "" {
			continue
		}
		site := fmt.Sprintf("%s:%d", fact.Path, fact.Line)
		if _, exists := seen[site]; exists {
			continue
		}
		seen[site] = struct{}{}
		sites = append(sites, site)
	}
	return sites
}
