package engine

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
)

func TestContextWindowThresholdsDerivePrepareFromExplicitCompactLimit(t *testing.T) {
	prepare, compact, emergency := agentcontext.WindowThresholds(
		CompactWindowPolicy{AutoTokens: 512},
		4096,
	)
	if prepare != 433 || compact != 512 || emergency != 3481 {
		t.Fatalf(
			"thresholds = (%d, %d, %d), want (433, 512, 3481)",
			prepare,
			compact,
			emergency,
		)
	}
}

func TestStatelessProviderUsesEconomicCompactionThresholds(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	engine.options.Route = reasoningRoute(t)
	engine.options.Routes, _ = model.NewRouteSet(engine.options.Route, nil, false)

	prepare, compact, emergency := agentcontext.WindowThresholds(
		engine.effectiveWindowPolicy(),
		engine.activeRoute().Model().Limits.ContextTokens,
	)
	if prepare != statelessPrepareTokens ||
		compact != statelessCompactTokens ||
		emergency != statelessEmergencyTokens {
		t.Fatalf(
			"thresholds = (%d, %d, %d), want (%d, %d, %d)",
			prepare,
			compact,
			emergency,
			statelessPrepareTokens,
			statelessCompactTokens,
			statelessEmergencyTokens,
		)
	}
}

func TestEngineDefaultsToBoundedConsumedToolResultHistory(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	if engine.options.MaxToolResultHistoryBytes != 16<<10 {
		t.Fatalf(
			"max tool result history bytes = %d, want %d",
			engine.options.MaxToolResultHistoryBytes,
			16<<10,
		)
	}
}
