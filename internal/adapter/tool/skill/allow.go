package skill

import "context"

type allowedSkillsKey struct{}

// WithAllowedSkills freezes name-to-handle bindings for one Turn.
func WithAllowedSkills(ctx context.Context, skills map[string]string) context.Context {
	copy := make(map[string]string, len(skills))
	for name, handle := range skills {
		if name != "" && handle != "" {
			copy[name] = handle
		}
	}
	return context.WithValue(ctx, allowedSkillsKey{}, copy)
}

func AllowedSkillsFrom(ctx context.Context) map[string]string {
	set, _ := ctx.Value(allowedSkillsKey{}).(map[string]string)
	return set
}
