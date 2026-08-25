package config

import "testing"

func TestCloneSnapshotCopiesNestedMapsAndSlices(t *testing.T) {
	original := Snapshot{
		Config: Config{
			Route: Route{Slots: map[string]RouteSlot{
				"plan": {Provider: "fixture", Model: "planner"},
			}},
			Diagnostics: Diagnostics{Commands: map[string]DiagnosticCommand{
				".go": {Name: "go", Args: []string{"test", "{path}"}},
			}},
		},
		Provenance: map[string]Source{fieldProvider: SourceFile},
	}
	cloned := CloneSnapshot(original)
	cloned.Config.Route.Slots["plan"] = RouteSlot{
		Provider: "other",
		Model:    "other",
	}
	command := cloned.Config.Diagnostics.Commands[".go"]
	command.Args[0] = "vet"
	cloned.Config.Diagnostics.Commands[".go"] = command
	cloned.Provenance[fieldProvider] = SourceStartup

	if original.Config.Route.Slots["plan"].Provider != "fixture" ||
		original.Config.Diagnostics.Commands[".go"].Args[0] != "test" ||
		original.Provenance[fieldProvider] != SourceFile {
		t.Fatalf("original snapshot mutated through clone: %+v", original)
	}
}
