package d2

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

type Plan struct {
	SchemaVersion  int           `json:"schema_version"`
	CampaignID     string        `json:"campaign_id"`
	Cases          []PlannedCase `json:"cases"`
	Coverage       Coverage      `json:"coverage"`
	EvidenceDigest string        `json:"evidence_digest"`
}

type PlannedCase struct {
	ID       string            `json:"id"`
	FamilyID string            `json:"family_id"`
	Seed     uint64            `json:"seed"`
	Values   map[string]string `json:"values"`
}

type Coverage struct {
	Axes                       []AxisCoverage `json:"axes"`
	PairwiseTotal              int            `json:"pairwise_total"`
	PairwiseCovered            int            `json:"pairwise_covered"`
	RequiredCombinationTotal   int            `json:"required_combination_total"`
	RequiredCombinationCovered int            `json:"required_combination_covered"`
	BoundaryTotal              int            `json:"boundary_total"`
	BoundaryCovered            int            `json:"boundary_covered"`
	FaultTriggerTotal          int            `json:"fault_trigger_total"`
	FaultTriggerCovered        int            `json:"fault_trigger_covered"`
}

type AxisCoverage struct {
	ID         string   `json:"id"`
	Selected   []string `json:"selected"`
	Unselected []string `json:"unselected"`
}

func BuildPlan(campaign Campaign) (Plan, error) {
	if err := campaign.Validate(); err != nil {
		return Plan{}, err
	}
	axes := make(map[string]Axis, len(campaign.Axes))
	for _, axis := range campaign.Axes {
		axes[axis.ID] = axis
	}
	families := append([]Family(nil), campaign.Families...)
	slices.SortFunc(families, func(left, right Family) int {
		return strings.Compare(left.ID, right.ID)
	})
	var cases []PlannedCase
	pairwiseTotal := 0
	pairwiseCovered := 0
	requiredTotal := 0
	requiredCovered := 0
	for _, family := range families {
		selected, universe, err := selectFamilyCases(family, axes)
		if err != nil {
			return Plan{}, err
		}
		pairwiseTotal += len(universe)
		covered := coveredPairs(family.ID, selected)
		pairwiseCovered += len(covered)
		requiredTotal += len(family.RequiredCombinations)
		for _, required := range family.RequiredCombinations {
			if slices.ContainsFunc(selected, func(values map[string]string) bool {
				return matches(values, required.Values)
			}) {
				requiredCovered++
			}
		}
		for index, values := range selected {
			cases = append(cases, PlannedCase{
				ID:       fmt.Sprintf("%s-%03d", family.ID, index+1),
				FamilyID: family.ID,
				Values:   maps.Clone(values),
			})
		}
	}
	if len(cases) == 0 || len(cases) > campaign.Budgets.MaxRuns {
		return Plan{}, fmt.Errorf(
			"D2 planned inventory %d exceeds max_runs %d",
			len(cases),
			campaign.Budgets.MaxRuns,
		)
	}
	for index := range cases {
		cases[index].Seed = campaign.Seeds[index%len(campaign.Seeds)]
	}
	coverage := buildCoverage(
		campaign,
		cases,
		pairwiseTotal,
		pairwiseCovered,
		requiredTotal,
		requiredCovered,
	)
	if coverage.PairwiseCovered != coverage.PairwiseTotal ||
		coverage.RequiredCombinationCovered != coverage.RequiredCombinationTotal ||
		coverage.BoundaryCovered != coverage.BoundaryTotal ||
		coverage.FaultTriggerCovered != coverage.FaultTriggerTotal {
		return Plan{}, errors.New("D2 planner did not close required coverage")
	}
	plan := Plan{
		SchemaVersion: SchemaVersion,
		CampaignID:    campaign.ID,
		Cases:         cases,
		Coverage:      coverage,
	}
	plan.EvidenceDigest = digestPlan(plan)
	return plan, nil
}

func selectFamilyCases(
	family Family,
	axes map[string]Axis,
) ([]map[string]string, map[string]struct{}, error) {
	axisIDs := append([]string(nil), family.Axes...)
	slices.Sort(axisIDs)
	candidates := []map[string]string{{}}
	for _, axisID := range axisIDs {
		var next []map[string]string
		for _, candidate := range candidates {
			for _, value := range axes[axisID].Values {
				copy := maps.Clone(candidate)
				copy[axisID] = value.ID
				next = append(next, copy)
			}
		}
		candidates = next
	}
	slices.SortFunc(candidates, func(left, right map[string]string) int {
		return strings.Compare(combinationKey(left), combinationKey(right))
	})
	universe := coveredPairs(family.ID, candidates)
	selected := make([]map[string]string, 0)
	selectedKeys := make(map[string]struct{})
	add := func(candidate map[string]string) {
		key := combinationKey(candidate)
		if _, exists := selectedKeys[key]; exists {
			return
		}
		selectedKeys[key] = struct{}{}
		selected = append(selected, maps.Clone(candidate))
	}
	for _, required := range family.RequiredCombinations {
		index := slices.IndexFunc(candidates, func(candidate map[string]string) bool {
			return matches(candidate, required.Values)
		})
		if index < 0 {
			return nil, nil, fmt.Errorf(
				"D2 family %q required combination is not selectable",
				family.ID,
			)
		}
		add(candidates[index])
	}
	covered := coveredPairs(family.ID, selected)
	for len(covered) < len(universe) {
		bestIndex := -1
		bestScore := -1
		for index, candidate := range candidates {
			if _, exists := selectedKeys[combinationKey(candidate)]; exists {
				continue
			}
			score := 0
			for key := range coveredPairs(
				family.ID,
				[]map[string]string{candidate},
			) {
				if _, exists := covered[key]; !exists {
					score++
				}
			}
			if score > bestScore {
				bestIndex = index
				bestScore = score
			}
		}
		if bestIndex < 0 || bestScore <= 0 {
			return nil, nil, fmt.Errorf(
				"D2 family %q pairwise planner did not converge",
				family.ID,
			)
		}
		add(candidates[bestIndex])
		covered = coveredPairs(family.ID, selected)
	}
	return selected, universe, nil
}

func coveredPairs(
	familyID string,
	combinations []map[string]string,
) map[string]struct{} {
	covered := make(map[string]struct{})
	for _, combination := range combinations {
		axes := slices.Sorted(maps.Keys(combination))
		for left := 0; left < len(axes); left++ {
			for right := left + 1; right < len(axes); right++ {
				key := strings.Join([]string{
					familyID,
					axes[left],
					combination[axes[left]],
					axes[right],
					combination[axes[right]],
				}, "\x00")
				covered[key] = struct{}{}
			}
		}
	}
	return covered
}

func buildCoverage(
	campaign Campaign,
	cases []PlannedCase,
	pairwiseTotal, pairwiseCovered, requiredTotal, requiredCovered int,
) Coverage {
	selected := make(map[string]map[string]struct{}, len(campaign.Axes))
	for _, planned := range cases {
		for axisID, valueID := range planned.Values {
			if selected[axisID] == nil {
				selected[axisID] = make(map[string]struct{})
			}
			selected[axisID][valueID] = struct{}{}
		}
	}
	coverage := Coverage{
		PairwiseTotal:              pairwiseTotal,
		PairwiseCovered:            pairwiseCovered,
		RequiredCombinationTotal:   requiredTotal,
		RequiredCombinationCovered: requiredCovered,
	}
	axes := append([]Axis(nil), campaign.Axes...)
	slices.SortFunc(axes, func(left, right Axis) int {
		return strings.Compare(left.ID, right.ID)
	})
	for _, axis := range axes {
		item := AxisCoverage{
			ID:         axis.ID,
			Selected:   []string{},
			Unselected: []string{},
		}
		for _, value := range axis.Values {
			if value.Boundary {
				coverage.BoundaryTotal++
			}
			if value.FaultTrigger {
				coverage.FaultTriggerTotal++
			}
			if _, exists := selected[axis.ID][value.ID]; exists {
				item.Selected = append(item.Selected, value.ID)
				if value.Boundary {
					coverage.BoundaryCovered++
				}
				if value.FaultTrigger {
					coverage.FaultTriggerCovered++
				}
			} else {
				item.Unselected = append(item.Unselected, value.ID)
			}
		}
		slices.Sort(item.Selected)
		slices.Sort(item.Unselected)
		coverage.Axes = append(coverage.Axes, item)
	}
	return coverage
}

func matches(candidate, required map[string]string) bool {
	for axisID, valueID := range required {
		if candidate[axisID] != valueID {
			return false
		}
	}
	return true
}

func combinationKey(values map[string]string) string {
	axes := slices.Sorted(maps.Keys(values))
	parts := make([]string, 0, len(axes))
	for _, axisID := range axes {
		parts = append(parts, axisID+"="+values[axisID])
	}
	return strings.Join(parts, "\x00")
}

func digestPlan(plan Plan) string {
	plan.EvidenceDigest = ""
	raw, _ := json.Marshal(plan)
	return spec.DigestString(string(raw))
}
