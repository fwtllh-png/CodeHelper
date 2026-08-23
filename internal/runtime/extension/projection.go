package extension

import (
	"sort"
	"strings"
	"time"
)

type Snapshot struct {
	Kind       string
	Name       string
	Version    string
	Source     string
	Publisher  string
	Trust      string
	Digest     string
	Generation uint64
	Enabled    bool
	LastAction string
	ChangedAt  time.Time
}

type LifecycleChange struct {
	Action          string
	PreviousVersion string
	Current         Snapshot
}

func ProjectLifecycle(plan Plan) []LifecycleChange {
	current := append([]Candidate(nil), plan.Extensions...)
	sort.Slice(current, func(i, j int) bool {
		if current[i].Kind != current[j].Kind {
			return current[i].Kind < current[j].Kind
		}
		return current[i].Name < current[j].Name
	})
	changes := make([]LifecycleChange, 0, len(current))
	for _, candidate := range current {
		if !candidate.Observable ||
			candidate.Kind == "" ||
			candidate.Name == "" {
			continue
		}
		action := "active"
		if !candidate.Enabled {
			action = "disabled"
		}
		changes = append(changes, LifecycleChange{
			Action: action,
			Current: Snapshot{
				Kind: candidate.Kind, Name: candidate.Name,
				Version:   candidate.Version,
				Source:    strings.TrimPrefix(candidate.Source.ID, "plugin:"),
				Publisher: candidate.Publisher, Trust: candidate.Trust,
				Digest: candidate.Digest, Generation: candidate.Generation,
				Enabled: candidate.Enabled, LastAction: candidate.LastAction,
				ChangedAt: candidate.ChangedAt,
			},
		})
	}
	return changes
}
