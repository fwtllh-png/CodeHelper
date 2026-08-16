package protocol

import (
	"strings"
	"testing"
	"time"
)

func TestExtensionControlOperationValidation(t *testing.T) {
	operation := ExtensionControlOperation{
		Version: Version, ID: "operation-1",
		Kind: ExtensionControlPlugin, Action: ExtensionActionInstall,
		Name: "review", VersionValue: "1.0.0", CreatedAt: time.Now().UTC(),
	}
	if err := operation.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*ExtensionControlOperation){
		"id":         func(value *ExtensionControlOperation) { value.ID = "../bad" },
		"kind":       func(value *ExtensionControlOperation) { value.Kind = "unknown" },
		"action":     func(value *ExtensionControlOperation) { value.Action = "execute" },
		"version":    func(value *ExtensionControlOperation) { value.VersionValue = "" },
		"capability": func(value *ExtensionControlOperation) {
			value.Action = ExtensionActionCapabilityEnable
			value.Capability = ""
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := operation
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("invalid extension operation was accepted")
			}
		})
	}
}

func TestExtensionControlReducerRejectsReorder(t *testing.T) {
	projection := ExtensionProjection{
		Kind: ExtensionControlSkill, Name: "review",
		Enabled: true, Health: "active",
	}
	event := ExtensionControlEvent{
		Sequence: 1, OperationID: "operation-1",
		Action: ExtensionActionEnable, Projection: &projection,
		Receipt: ExtensionControlReceipt{
			OperationID: "operation-1", Action: ExtensionActionEnable,
			Kind: ExtensionControlSkill, Name: "review",
			Status: "committed", Digest: strings.Repeat("a", 64),
			Revision: 1, OccurredAt: time.Now().UTC(),
		},
		OccurredAt: time.Now().UTC(),
	}
	result, err := ReduceExtensionControlEvents([]ExtensionControlEvent{event})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].Name != "review" {
		t.Fatalf("projection = %+v", result)
	}
	if _, err := ReduceExtensionControlEvents(
		[]ExtensionControlEvent{event, event},
	); err == nil {
		t.Fatal("reordered extension events were accepted")
	}
}
