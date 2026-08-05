package cli

import (
	"encoding/json"
	"testing"
)

func TestValidateHostRequest(t *testing.T) {
	httpRequest := hostRequest{Method: "POST", Path: "/v1/turns", Prompt: "hello"}
	if prompt, err := validateHostRequest("http", httpRequest); err != nil || prompt != "hello" {
		t.Fatalf("HTTP request prompt=%q err=%v", prompt, err)
	}

	acpRequest := hostRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "session/prompt",
	}
	acpRequest.Params.Prompt = "hello"
	if prompt, err := validateHostRequest("acp", acpRequest); err != nil || prompt != "hello" {
		t.Fatalf("ACP request prompt=%q err=%v", prompt, err)
	}

	if _, err := validateHostRequest("http", hostRequest{Method: "GET"}); err == nil {
		t.Fatal("malformed HTTP host envelope succeeded")
	}
	if _, err := validateHostRequest("acp", hostRequest{JSONRPC: "2.0"}); err == nil {
		t.Fatal("malformed ACP host envelope succeeded")
	}
}
