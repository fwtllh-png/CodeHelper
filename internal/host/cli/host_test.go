package cli

import (
	"encoding/json"
	"testing"
)

func TestValidateHostRequest(t *testing.T) {
	acpRequest := hostRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "session/prompt",
	}
	acpRequest.Params.Prompt = "hello"
	if prompt, err := validateHostRequest("acp", acpRequest); err != nil || prompt != "hello" {
		t.Fatalf("ACP request prompt=%q err=%v", prompt, err)
	}

	if _, err := validateHostRequest("http", hostRequest{}); err == nil {
		t.Fatal("removed HTTP host envelope succeeded")
	}
	if _, err := validateHostRequest("acp", hostRequest{JSONRPC: "2.0"}); err == nil {
		t.Fatal("malformed ACP host envelope succeeded")
	}
}
