package wire

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/config"
)

func TestMaximumReasoningEffortUsesDeepSeekMax(t *testing.T) {
	if got := maximumReasoningEffort(
		"deepseek-v4-flash",
		"deepseek-v4-flash",
		true,
	); got != "max" {
		t.Fatalf("DeepSeek V4 effort = %q, want max", got)
	}
	if got := maximumReasoningEffort("fixture", "model", true); got != "xhigh" {
		t.Fatalf("generic reasoning effort = %q, want xhigh", got)
	}
	if got := maximumReasoningEffort("fixture", "model", false); got != "" {
		t.Fatalf("non-reasoning effort = %q, want empty", got)
	}
}

func TestReasoningAwareMaxOutputTokensPromotesOnlyDefaultBudget(t *testing.T) {
	resolver, err := model.NewResolver(model.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	route, err := resolver.Resolve(model.RouteRequest{
		ProviderID: "deepseek-v4-flash",
		ModelID:    "deepseek-v4-flash",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := reasoningAwareMaxOutputTokens(
		4096, config.SourceDefault, route, "max",
	)
	if err != nil || got != minimumReasoningOutputTokens {
		t.Fatalf("default reasoning budget = %d, error = %v", got, err)
	}
	_, err = reasoningAwareMaxOutputTokens(
		4096, config.SourceCLI, route, "max",
	)
	if err == nil || !strings.Contains(err.Error(), "from cli") {
		t.Fatalf("explicit low budget error = %v", err)
	}
	got, err = reasoningAwareMaxOutputTokens(
		4096, config.SourceCLI, route, "",
	)
	if err != nil || got != 4096 {
		t.Fatalf("non-reasoning budget = %d, error = %v", got, err)
	}
}
