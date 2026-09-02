package engine

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
)

func (e *Engine) observationGate(
	call provider.ToolCall,
	finishOnly bool,
) *tool.Result {
	if call.Name != "file_read" {
		return nil
	}
	path, startLine, ok := parseFileReadWindow(call.Arguments)
	if !ok {
		return nil
	}
	if startLine > 0 {
		return nil
	}
	if !finishOnly && !e.pathHasLocatedSite(path) {
		return nil
	}
	return &tool.Result{
		Content: "file_read requires start_line after a path has been located " +
			"by search or after the no-progress finish-only lease. " +
			"Use a search hit window and edit; do not page the rest of the file.",
		IsError: true,
		Metadata: map[string]any{
			"error_category": "located_site_window_required",
			"path":           path,
			"retry_original": false,
		},
	}
}

func parseFileReadWindow(raw string) (string, int, bool) {
	var input struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
	}
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return "", 0, false
	}
	path := strings.TrimSpace(input.Path)
	return path, input.StartLine, path != ""
}

func (e *Engine) pathHasLocatedSite(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	if relative, ok := agentcontext.WorkspaceRelative(e.options.Workspace, path); ok {
		path = relative
	}
	if e.contextAuthority().WorkingSet().HasSource(agentcontext.SourceSearch, path) {
		return true
	}
	for _, fact := range e.EvidenceSnapshot().Facts {
		if fact.Path == path && fact.Line > 0 {
			return true
		}
	}
	return false
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
