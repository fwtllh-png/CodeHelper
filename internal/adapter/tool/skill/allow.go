package skill

import (
	"context"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

// WithAllowedSkills freezes name-to-handle bindings for one Turn.
func WithAllowedSkills(ctx context.Context, skills map[string]string) context.Context {
	return tool.WithAllowedSkills(ctx, skills)
}

func AllowedSkillsFrom(ctx context.Context) map[string]string {
	return tool.AllowedSkillsFrom(ctx)
}
