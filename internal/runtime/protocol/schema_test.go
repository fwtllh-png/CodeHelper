package protocol_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// schemaPath is the committed copy consumers read. It lives in docs because it is
// published, not test data.
func schemaPath() string {
	return filepath.Join("..", "..", "..", "docs", "protocol", "runtime-protocol.schema.json")
}

// The committed schema is the artifact an external consumer reads, so it has to
// match this build. Regenerate with `make protocol-schema` when the protocol
// changes; a diff here means the two have drifted.
func TestTheCommittedSchemaMatchesThisBuild(t *testing.T) {
	want, err := protocol.MarshalSchema()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(schemaPath())
	if err != nil {
		t.Fatalf("read the committed schema: %v (run make protocol-schema)", err)
	}
	if string(got) != string(want) {
		t.Fatalf(
			"the committed protocol schema is stale; run make protocol-schema\n"+
				"committed %d bytes, generated %d bytes", len(got), len(want),
		)
	}
}

// Every kind that can be decoded must have a published shape. The two come from
// the same table, so this is a guard against the generator skipping something
// rather than against the table.
func TestEveryKindHasAPublishedShape(t *testing.T) {
	schema := protocol.GenerateSchema()
	for _, kind := range protocol.OperationKinds() {
		shape := schema.Operations[string(kind)]
		if shape == nil || shape.Type != "object" {
			t.Fatalf("operation %q shape = %+v", kind, shape)
		}
	}
	for _, kind := range protocol.EventKinds() {
		shape := schema.Events[string(kind)]
		if shape == nil || shape.Type != "object" {
			t.Fatalf("event %q shape = %+v", kind, shape)
		}
	}
	if len(schema.Operations) != len(protocol.OperationKinds()) {
		t.Fatalf("operations = %d, kinds = %d", len(schema.Operations), len(protocol.OperationKinds()))
	}
	if len(schema.Events) != len(protocol.EventKinds()) {
		t.Fatalf("events = %d, kinds = %d", len(schema.Events), len(protocol.EventKinds()))
	}
}

// The shapes have to describe the wire, which means json tag names, the fields
// decoding insists on, and the fact that unknown fields are refused.
func TestShapesDescribeTheWireAndNotTheGoStructs(t *testing.T) {
	schema := protocol.GenerateSchema()
	start := schema.Operations[string(protocol.OperationStartTurn)]
	if start.Properties["thread_id"] == nil || start.Properties["ThreadID"] != nil {
		t.Fatalf("start turn properties = %+v, want json names", start.Properties)
	}
	if start.AdditionalProperties == nil || *start.AdditionalProperties {
		t.Fatal("decoding refuses unknown fields, so the schema must say so")
	}
	required := map[string]bool{}
	for _, name := range start.Required {
		required[name] = true
	}
	if !required["prompt"] || !required["thread_id"] {
		t.Fatalf("start turn required = %v", start.Required)
	}
	// An optional field is optional on the wire too.
	if required["idle"] {
		t.Fatalf("idle is omitempty and must not be required: %v", start.Required)
	}
	context := start.Properties["context"]
	if context == nil || context.Items == nil ||
		context.Items.Properties["source"] == nil ||
		context.Items.Properties["symbol"] == nil ||
		context.Items.Properties["diagnostics"] == nil ||
		context.Items.Properties["omitted_diagnostics"] == nil {
		t.Fatalf("turn context schema lacks V2 native fields: %+v", context)
	}
	started := schema.Events[string(protocol.EventTurnStarted)]
	receipt := schema.Events[string(protocol.EventExecutionReceipt)]
	if started.Properties["editor_context"] == nil ||
		receipt.Properties["editor_context"] == nil {
		t.Fatalf(
			"editor context receipt missing from started=%+v receipt=%+v",
			started, receipt,
		)
	}

	// Timestamps carry the one semantic a consumer cannot infer from "string".
	completed := schema.Events[string(protocol.EventTurnCompleted)]
	if completed == nil {
		t.Fatal("turn.completed has no shape")
	}
	envelope := schema.Envelope["event"]
	if envelope.Properties["created_at"].Format != "date-time" {
		t.Fatalf("event envelope created_at = %+v", envelope.Properties["created_at"])
	}
	// The envelope pins the kinds, so a consumer can validate before dispatching.
	if len(envelope.Properties["kind"].Enum) != len(protocol.EventKinds()) {
		t.Fatalf("event kind enum = %v", envelope.Properties["kind"].Enum)
	}
	eventRequired := make(map[string]bool, len(envelope.Required))
	for _, name := range envelope.Required {
		eventRequired[name] = true
	}
	for _, name := range []string{
		"sequence", "operation_id", "thread_id", "turn_id", "item_id",
	} {
		if envelope.Properties[name] == nil || !eventRequired[name] {
			t.Fatalf("event envelope field %q is not required: %+v", name, envelope)
		}
	}
}

// The document has to be valid JSON that a consumer can load without knowing Go.
func TestTheSchemaLoadsAsPlainJSON(t *testing.T) {
	data, err := protocol.MarshalSchema()
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document["$schema"] != protocol.SchemaDialect {
		t.Fatalf("dialect = %v", document["$schema"])
	}
	if document["protocol_version"] != float64(protocol.Version) {
		t.Fatalf("version = %v, want %d", document["protocol_version"], protocol.Version)
	}
	if data[len(data)-1] != '\n' {
		t.Fatal("the committed form ends with a newline")
	}
}
