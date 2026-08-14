package engine

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

func TestEngineRejectsUnadvertisedFixedReasoningEffort(t *testing.T) {
	options := Options{
		Provider:        &scriptedProvider{},
		Route:           testRoute(t),
		Tools:           tool.NewRegistry(nil, nil),
		ReasoningEffort: "unsupported",
	}
	if _, err := New(options); err == nil ||
		!strings.Contains(err.Error(), "does not support reasoning effort") {
		t.Fatalf("New() error = %v", err)
	}
	resolver, err := model.NewResolver(model.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	options.Route, err = resolver.Resolve(model.RouteRequest{
		ProviderID: "deepseek-v4-flash",
		ModelID:    "deepseek-v4-flash",
	})
	if err != nil {
		t.Fatal(err)
	}
	options.ReasoningEffort = "off"
	engine, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if got := engine.options.ReasoningEffort; got != "off" {
		t.Fatalf("reasoning effort = %q", got)
	}
}
