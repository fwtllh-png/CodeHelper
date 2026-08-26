package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCapacityPathsDoNotReintroduceLegacyTiers(t *testing.T) {
	root := filepath.Clean("..")
	for path, forbidden := range map[string][]string{
		"internal/runtime/agent/context/store_window.go": {
			"limit * 55 / 100", "limit * 65 / 100", "limit * 85 / 100",
		},
		"internal/runtime/agent/engine/model_handler.go": {
			"min(modelLimit, 16_384)",
		},
		"internal/runtime/app/wire/route.go": {
			"MaxTokens: 8 << 10", "MaxTokens: 32 << 10",
			"MaxTokens: 64 << 10",
		},
		"internal/runtime/app/wire/budget_policy.go": {
			"contextWindow",
		},
		"internal/runtime/app/wire/modules_runtime.go": {
			"MaxTurnTokens: route.Model().Limits.ContextTokens",
			"execution.TurnBudgetTokens, route.Model().Limits.ContextTokens",
		},
		"internal/runtime/app/wire/orchestration_components.go": {
			"execution.TurnBudgetTokens, state.provider.route.Model().Limits.ContextTokens",
		},
		"internal/adapter/tool/tool.go": {
			`kind, tokens := "generic", 2048`,
		},
		"internal/adapter/tool/search/search.go": {
			"input.MaxFileBytes = 1 << 20", "input.MaxResults = 1000",
		},
		"internal/orchestration/subagent/context_fork.go": {
			"MaxBytes: 24 << 10", "MaxTokens: 6 << 10",
		},
		"web/src/ui/App.tsx": {
			"setInterval(refresh, 3_000)", "setInterval(() => {\n      void client.refreshTrace()",
		},
	} {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range forbidden {
			if strings.Contains(string(data), value) {
				t.Errorf("%s reintroduced legacy capacity tier %q", path, value)
			}
		}
	}
}
