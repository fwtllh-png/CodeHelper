package wire

import (
	"context"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
)

func selectTurnSkills(
	catalog *skill.Catalog,
	query string,
) ([]agentengine.SkillSummary, agentengine.SkillSelectionMetrics, error) {
	if catalog == nil {
		return nil, agentengine.SkillSelectionMetrics{}, nil
	}
	selection, err := catalog.Select(
		context.Background(),
		skill.SelectionRequest{
			Query: query, Mode: skill.SelectionCandidate,
		},
	)
	if err != nil {
		return nil, agentengine.SkillSelectionMetrics{}, err
	}
	out := make([]agentengine.SkillSummary, 0, len(selection.Visible))
	for _, summary := range selection.Visible {
		out = append(out, agentengine.SkillSummary{
			Name: summary.Name, Description: summary.Description,
			Source: string(summary.Source), Path: summary.Path,
			Handle: summary.Handle, PackageHandle: summary.PackageHandle,
			ResourceHandle: summary.ResourceHandle,
		})
	}
	metrics := selection.Metrics
	return out, agentengine.SkillSelectionMetrics{
		Method: metrics.Method, CatalogSize: metrics.CatalogSize,
		CandidateSize: metrics.CandidateSize, VisibleSize: metrics.VisibleSize,
		ExplicitMatches:       metrics.ExplicitMatches,
		QueryTerms:            metrics.QueryTerms,
		QueryTruncated:        metrics.QueryTruncated,
		CandidateSetTruncated: metrics.CandidateSetTruncated,
		OriginalTokens:        metrics.OriginalTokens,
		ProjectedTokens:       metrics.ProjectedTokens,
		TokenSavings:          metrics.TokenSavings,
		Recall:                metrics.Recall,
		Precision:             metrics.Precision,
		CacheHit:              metrics.CacheHit,
	}, nil
}
