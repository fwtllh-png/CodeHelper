package skill

import "context"

type allowedSkillsKey struct{}

// WithAllowedNames freezes the set of skill names loadable this turn (N10).
func WithAllowedNames(ctx context.Context, names []string) context.Context {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name != "" {
			set[name] = struct{}{}
		}
	}
	return context.WithValue(ctx, allowedSkillsKey{}, set)
}

// AllowedNamesFrom returns the turn allowlist, or nil when unrestricted.
func AllowedNamesFrom(ctx context.Context) map[string]struct{} {
	set, _ := ctx.Value(allowedSkillsKey{}).(map[string]struct{})
	return set
}
