package mcp

import (
	"sort"
)

type ProjectedHealthChange struct {
	PreviousState string
	Current       HealthSnapshot
}

func ProjectHealth(current []HealthSnapshot) []ProjectedHealthChange {
	current = append([]HealthSnapshot(nil), current...)
	sort.Slice(current, func(i, j int) bool {
		return current[i].Server < current[j].Server
	})
	changes := make([]ProjectedHealthChange, 0, len(current))
	for _, snapshot := range current {
		if snapshot.Server != "" {
			changes = append(changes, ProjectedHealthChange{
				Current: snapshot,
			})
		}
	}
	return changes
}
