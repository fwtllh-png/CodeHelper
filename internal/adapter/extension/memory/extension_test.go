package memory

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/extension/extensiontest"
)

func TestExtensionContributesRememberToolThroughTypedContract(t *testing.T) {
	value := New(config.Memory{Enabled: true, Path: t.TempDir()})
	harness := extensiontest.New(t, value)
	result := harness.Tool(t.Context(), "memory", extension.ToolInput{})
	if result.Outcome.Status != extension.OutcomeSucceeded ||
		len(result.Value.Registrations) != 5 ||
		result.Value.Registrations[0].Descriptor().Name != "remember" {
		t.Fatalf("memory contribution = %+v", result)
	}
	if value.Store() == nil {
		t.Fatal("memory extension did not retain the contributed store")
	}
}

func TestDisabledExtensionProducesTypedSkip(t *testing.T) {
	value := New(config.Memory{})
	builder := extension.NewBuilder()
	if err := builder.Register(value); err != nil {
		t.Fatal(err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	result, err := registry.ToolContributors()[0].Contribute(
		t.Context(),
		extension.ToolInput{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome.Status != extension.OutcomeSkipped ||
		result.Receipt.Code != "disabled" ||
		len(result.Value.Registrations) != 0 ||
		value.Store() != nil {
		t.Fatalf("disabled memory contribution = %+v", result)
	}
}
