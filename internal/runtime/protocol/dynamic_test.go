package protocol

import (
	"strings"
	"testing"
)

func TestDynamicToolSpecRoundTripAndUnknownFields(t *testing.T) {
	raw := []byte(`{
		"version":1,
		"namespace":"bench",
		"name":"lookup",
		"description":"Lookup a record",
		"input_schema":{"type":"object","properties":{"id":{"type":"string"}},"additionalProperties":false},
		"defer_loading":true
	}`)
	spec, err := DecodeDynamicToolSpec(raw)
	if err != nil {
		t.Fatal(err)
	}
	if spec.ToolName() != "bench__lookup" {
		t.Fatalf("tool name = %q", spec.ToolName())
	}
	if _, err := DecodeDynamicToolSpec([]byte(`{
		"version":1,"name":"lookup","description":"x",
		"input_schema":{"type":"object"},"extra":true
	}`)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
	if _, err := DecodeDynamicToolSpec([]byte(`{
		"version":2,"name":"lookup","description":"x","input_schema":{"type":"object"}
	}`)); err == nil {
		t.Fatal("expected version rejection")
	}
}

func TestDynamicToolResultValidation(t *testing.T) {
	result, err := DecodeDynamicToolCallResult([]byte(`{
		"version":1,
		"success":true,
		"content":[{"type":"input_text","text":"ok"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success || len(result.Content) != 1 {
		t.Fatalf("result = %+v", result)
	}
	if _, err := DecodeDynamicToolCallResult([]byte(`{
		"version":1,"success":false,"content":[{"type":"nope"}]
	}`)); err == nil {
		t.Fatal("expected content type rejection")
	}
}
