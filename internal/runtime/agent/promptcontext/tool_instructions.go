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
		"A turn that mutates the workspace is not complete until turn_complete is called " +
		"after the last mutation and every required quality check."
	if domain = strings.TrimSpace(domain); domain != "" {
		return base + " " + domain
	}
	return base
}
