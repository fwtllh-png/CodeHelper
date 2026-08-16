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

// WithAllowedNames freezes the set of skill names loadable this turn (N10).
func WithAllowedNames(ctx context.Context, names []string) context.Context {
	set := make(map[string]string, len(names))
	for _, name := range names {
		if name != "" {
			set[name] = ""
		}
	}
	return context.WithValue(ctx, allowedSkillsKey{}, set)
}

func AllowedSkillsFrom(ctx context.Context) map[string]string {
	set, _ := ctx.Value(allowedSkillsKey{}).(map[string]string)
	return set
}

// AllowedNamesFrom preserves the legacy test surface.
func AllowedNamesFrom(ctx context.Context) map[string]struct{} {
	skills := AllowedSkillsFrom(ctx)
	if skills == nil {
		return nil
	}
	result := make(map[string]struct{}, len(skills))
	for name := range skills {
		result[name] = struct{}{}
	}
	return result
}
