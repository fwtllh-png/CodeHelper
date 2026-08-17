package promptcontext

import (
	"path/filepath"
	"strings"
)

func StickyCacheKey(sessionID, workspace string) string {
	if sessionID != "" {
		return "session:" + sessionID
	}
	if workspace != "" {
		return "workspace:" + filepath.Base(workspace)
	}
	return "codehelper-default"
}

// ToolInstructions builds the stable developer instruction for governed tools
// and appends an optional domain-specific contract.
func ToolInstructions(enabled bool, domain string) string {
	if !enabled {
		return ""
	}
	base := "Use only the supplied tools and honor their schemas and policy decisions. " +
		"Before ending a tool-enabled Turn, choose one structured state: call " +
		"request_user_input when progress truly requires a user answer and wait in the " +
		"same Turn, or call turn_complete. Ordinary assistant text is provisional and " +
		"cannot terminate the Turn. For status=complete, put the exact user-facing final " +
		"response in summary; the runtime publishes it without another model sample. " +
		"Call complete only after the last requested action, mutation, and required " +
		"quality check. Use status=incomplete with concrete pending_actions when work remains."
	if domain = strings.TrimSpace(domain); domain != "" {
		return base + " " + domain
	}
	return base
}
